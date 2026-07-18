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
	Request    string `json:"request"`     // CreateVcnRequest
	Response   string `json:"response"`    // CreateVcnResponse
	Doc        string `json:"doc,omitempty"`
}

// httpVerb maps the net/http Method* selector the SDK uses to the wire verb. A
// bare string literal method is passed through as-is.
var httpVerb = map[string]string{
	"MethodGet": "GET", "MethodPost": "POST", "MethodPut": "PUT",
	"MethodDelete": "DELETE", "MethodPatch": "PATCH", "MethodHead": "HEAD",
	"MethodOptions": "OPTIONS",
}

// ExtractClientFile parses one *_client.go and returns its operations, sorted by
// name. A public method is included only when its signature names a *Request
// param and a *Response result and a matching private method carries an
// HTTPRequest(method, path) call — i.e. a real client operation, not a helper.
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
			Doc:        firstSentence(docText(fn.Doc, nil)),
		})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Name < ops[j].Name })
	return ops, nil
}

// httpRequestCall finds a `<x>.HTTPRequest(<method>, <path>, ...)` call anywhere
// in fn's body and returns the resolved verb and unquoted path.
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
		if !ok || sel.Sel.Name != "HTTPRequest" || len(call.Args) < 2 {
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
	return req, resp, req != "" && resp != ""
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
