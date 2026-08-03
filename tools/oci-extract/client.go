package ociextract

// client.go extracts operations from an OCI SDK *_client.go file: each public
// client method (e.g. CreateVcn) and its HTTP method + path.
//
// The OCI SDK shape this relies on (stable across the generated clients):
//
//	func (client VirtualNetworkClient) CreateVcn(ctx context.Context, request CreateVcnRequest) (response CreateVcnResponse, err error) {
//	    ... common.Retry(ctx, request, client.createVcn, policy) ...
//	}
//	func (client VirtualNetworkClient) createVcn(ctx context.Context, request common.OCIRequest, ...) (common.OCIResponse, error) {
//	    httpRequest, err := request.HTTPRequest(http.MethodPost, "/vcns", binaryReqBody, extraHeaders)
//	    ...
//	}
//
// The public method carries the request/response types; the private method
// (public name, lower-cased first letter — the SDK's convention) carries the
// wire method+path in a request.HTTPRequest(<method>, <path>, ...) call.
//
// Two shapes in the SDK do NOT fit that description, and both are handled here
// rather than silently skipped (see docs/design/specs/oci-schema-pipeline.md,
// "Service-agnostic operation resolution"):
//
//   - An operation with no request object at all. IdentityClient.ListRegions
//     takes only a context, so there is no *Request parameter, and its private
//     half builds the wire call with common.MakeDefaultHTTPRequest instead of
//     request.HTTPRequest — no request object exists to carry the method.
//   - Any other HTTP-request constructor from `common`. They all share the
//     (method, path, …) argument prefix, so one matcher covers them.
//
// A *Response result plus a resolvable method+path is therefore the operation
// test; the request object is optional.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
)

// Operation is one client method joined with its HTTP wire placement and the
// request/response model names (which ExtractFile resolves to fields).
type Operation struct {
	Name       string `json:"name"`        // CreateVcn
	Client     string `json:"client"`      // VirtualNetworkClient
	HTTPMethod string `json:"http_method"` // POST
	HTTPPath   string `json:"http_path"`   // /vcns
	Request    string `json:"request"`     // CreateVcnRequest ("" for request-less operations)
	Response   string `json:"response"`    // CreateVcnResponse
	// PathParams are the {name} placeholders of HTTPPath, in path order. A
	// resource is not always addressed by a single id: object storage's
	// /n/{namespaceName}/b/{bucketName} needs two, and 22% of the SDK's Get
	// operations have a count other than one. Emitting them makes the
	// addressing contract explicit instead of assumed.
	PathParams []string `json:"path_params,omitempty"`
	Doc        string   `json:"doc,omitempty"`
}

// httpVerb maps the net/http Method* selector the SDK uses to the wire verb. A
// bare string literal method is passed through as-is.
var httpVerb = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT",
	"MethodDelete": "DELETE", "MethodPatch": "PATCH", "MethodHead": "HEAD",
	"MethodOptions": "OPTIONS",
}

// httpRequestFuncs are the `common` constructors that build the wire request.
// All take (method, path) as their first two arguments, so one matcher covers
// them; HTTPRequest is the method form on the request object, the rest are
// package functions used by request-less operations.
var httpRequestFuncs = map[string]bool{
	"HTTPRequest":                            true,
	"MakeDefaultHTTPRequest":                 true,
	"MakeDefaultHTTPRequestWithTaggedStruct": true,
}

// ExtractClientFile parses one *_client.go and returns its operations, sorted by
// name. A public method is included when its signature names a *Response result
// and a matching private method carries a resolvable (method, path) wire call —
// i.e. a real client operation, not a helper. A *Request parameter is recorded
// when present but is not required: some operations take no request object.
func ExtractClientFile(path string) ([]Operation, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	// Index every method by name, and pull the HTTP method+path out of any
	// method that makes an HTTPRequest call (the private ones do).
	methods := map[string]*ast.FuncDecl{}
	httpOf := map[string][2]string{} // method name -> {verb, path}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		methods[fn.Name.Name] = fn
		if verb, p, ok := httpRequestCall(fn); ok {
			httpOf[fn.Name.Name] = [2]string{verb, p}
		}
	}

	var ops []Operation
	for name, fn := range methods {
		if !ast.IsExported(name) {
			continue
		}
		req, resp, ok := requestResponseTypes(fn)
		if !ok {
			continue
		}
		http, ok := httpOf[lowerFirst(name)]
		if !ok {
			continue
		}
		ops = append(ops, Operation{
			Name:       name,
			Client:     receiverType(fn),
			HTTPMethod: http[0],
			HTTPPath:   http[1],
			Request:    req,
			Response:   resp,
			PathParams: PathParams(http[1]),
			Doc:        firstSentence(docText(fn.Doc, nil)),
		})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	return ops, nil
}

// httpRequestCall finds a wire-request constructor call — `<x>.HTTPRequest(…)`
// or one of the `common.MakeDefaultHTTPRequest*` package functions — anywhere in
// fn's body and returns the resolved verb and unquoted path. All of them share
// the (method, path, …) argument prefix, so one matcher covers every shape the
// generated clients use.
func httpRequestCall(fn *ast.FuncDecl) (verb, path string, found bool) {
	if fn.Body == nil {
		return "", "", false
	}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !httpRequestFuncs[sel.Sel.Name] || len(call.Args) < 2 {
			return true
		}
		verb = httpMethodArg(call.Args[0])
		if p, ok := stringLit(call.Args[1]); ok {
			path = p
			found = verb != "" && path != ""
		}
		return !found
	})
	return verb, path, found
}

// httpMethodArg resolves the first HTTPRequest arg: an http.Method* selector, a
// bare Method* ident, or a string literal.
func httpMethodArg(e ast.Expr) string {
	switch a := e.(type) {
	case *ast.SelectorExpr:
		if v, ok := httpVerb[a.Sel.Name]; ok {
			return v
		}
		return a.Sel.Name
	case *ast.Ident:
		if v, ok := httpVerb[a.Name]; ok {
			return v
		}
		return a.Name
	case *ast.BasicLit:
		if s, ok := stringLit(a); ok {
			return s
		}
	}
	return ""
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// requestResponseTypes reads a public method's signature: a parameter whose type
// name ends in "Request" and a result whose type name ends in "Response".
//
// The *Response result is what makes a method an operation; the *Request
// parameter is optional, because an operation that needs no input carries none
// (IdentityClient.ListRegions(ctx) → ListRegionsResponse). Requiring both would
// silently drop such operations from the registry.
func requestResponseTypes(fn *ast.FuncDecl) (req, resp string, ok bool) {
	if fn.Type.Params != nil {
		for _, p := range fn.Type.Params.List {
			if t := lastIdent(typeString(p.Type)); strings.HasSuffix(t, "Request") {
				req = t
			}
		}
	}
	if fn.Type.Results != nil {
		for _, r := range fn.Type.Results.List {
			if t := lastIdent(typeString(r.Type)); strings.HasSuffix(t, "Response") {
				resp = t
			}
		}
	}
	return req, resp, resp != ""
}

// ClientAudit is the resolution coverage of one *_client.go: how many methods
// look like operations, how many got an HTTP method+path, and — by name — the
// ones that did not.
//
// This exists because the extractor's failure mode is silence. A method it
// cannot resolve is skipped, so a service can lose operations and still produce
// a registry that looks complete. Counting the denominator separately from the
// result turns "no error" into a number that can be checked.
type ClientAudit struct {
	File       string   `json:"file"`
	Client     string   `json:"client"`
	Candidates int      `json:"candidates"` // exported methods whose signature is an operation
	Resolved   int      `json:"resolved"`   // of those, with an HTTP method+path
	Unresolved []string `json:"unresolved,omitempty"`
	// OrphanPrivate are private methods carrying a wire call whose exported
	// counterpart is missing — an operation the naming convention failed to pair.
	OrphanPrivate []string `json:"orphan_private,omitempty"`
}

// AuditClientFile reports the operation-resolution coverage of one *_client.go.
func AuditClientFile(path string) (ClientAudit, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return ClientAudit{}, fmt.Errorf("parse %s: %w", path, err)
	}
	a := ClientAudit{File: path}
	methods := map[string]*ast.FuncDecl{}
	httpOf := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || len(fn.Recv.List) == 0 {
			continue
		}
		methods[fn.Name.Name] = fn
		if _, _, ok := httpRequestCall(fn); ok {
			httpOf[fn.Name.Name] = true
		}
	}
	var names []string
	for name := range methods {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fn := methods[name]
		if !ast.IsExported(name) {
			if httpOf[name] {
				continue // private methods are the wire half, paired below
			}
			continue
		}
		if _, _, ok := requestResponseTypes(fn); !ok {
			continue // client plumbing (SetRegion, ConfigurationProvider, …)
		}
		a.Candidates++
		if httpOf[lowerFirst(name)] {
			a.Resolved++
			if a.Client == "" {
				a.Client = receiverType(fn)
			}
		} else {
			a.Unresolved = append(a.Unresolved, name)
		}
	}
	for name := range httpOf {
		if ast.IsExported(name) {
			continue
		}
		if _, ok := methods[strings.ToUpper(name[:1])+name[1:]]; !ok {
			a.OrphanPrivate = append(a.OrphanPrivate, name)
		}
	}
	sort.Strings(a.OrphanPrivate)
	return a, nil
}

// PathParams returns the {name} placeholders of an HTTP path template, in path
// order. /n/{namespaceName}/b/{bucketName} → [namespaceName bucketName].
func PathParams(path string) []string {
	var out []string
	for {
		i := strings.IndexByte(path, '{')
		if i < 0 {
			return out
		}
		rest := path[i+1:]
		j := strings.IndexByte(rest, '}')
		if j < 0 {
			return out
		}
		if name := rest[:j]; name != "" {
			out = append(out, name)
		}
		path = rest[j+1:]
	}
}

// receiverType returns the method receiver's type name (VirtualNetworkClient),
// stripping a leading pointer.
func receiverType(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	return lastIdent(typeString(fn.Recv.List[0].Type))
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// firstSentence trims a doc comment to its first sentence — enough to identify
// an operation without carrying the SDK's multi-paragraph prose into the registry.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}
