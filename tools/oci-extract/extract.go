// Package ociextract reads OCI Go SDK source with go/ast and extracts the model
// registry: each struct's fields, their JSON names, required/optional status,
// and the request/response wire placement the SDK encodes in struct tags.
//
// The SDK's generated tags are the contract (docs/design/specs/oci-schema-pipeline.md):
//
//	mandatory:"true|false"                  required / optional
//	json:"name"                             JSON field name
//	contributesTo:"body|header|path|query"  where a request field goes
//	name:"opc-request-id"                   actual HTTP parameter name
//	presentIn:"body|header"                 where a response field is read
//
// This slice extracts models (the *Details / request / response structs). Joining
// the client method's HTTP method+path is a later P2.2 step.
package ociextract

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"sort"
	"strings"
)

// Field is one struct field with the SDK metadata that matters for the contract.
type Field struct {
	Name          string `yaml:"name"`                    // Go field name
	Type          string `yaml:"type"`                    // Go type as written
	JSON          string `yaml:"json,omitempty"`          // json tag value (name, minus options)
	Mandatory     bool   `yaml:"mandatory"`               // mandatory:"true"
	ContributesTo string `yaml:"contributesTo,omitempty"` // request placement
	PresentIn     string `yaml:"presentIn,omitempty"`     // response placement
	HTTPName      string `yaml:"httpName,omitempty"`      // name:"..." HTTP parameter name
	Doc           string `yaml:"doc,omitempty"`           // leading doc comment, trimmed
}

// Model is a named struct and its fields, in source order.
type Model struct {
	Name   string  `yaml:"name"`
	Doc    string  `yaml:"doc,omitempty"`
	Fields []Field `yaml:"fields"`
}

// ExtractFile parses one Go source file and returns its structs as Models,
// sorted by name for stable output. Comments are kept (go/doc semantics) so a
// field's leading doc survives — struct reflection alone would drop it.
func ExtractFile(path string) ([]Model, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	var models []Model
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			models = append(models, Model{
				Name:   ts.Name.Name,
				Doc:    docText(ts.Doc, gen.Doc),
				Fields: extractFields(st),
			})
		}
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

func extractFields(st *ast.StructType) []Field {
	var fields []Field
	for _, f := range st.Fields.List {
		typ := typeString(f.Type)
		tag := parseTag(f.Tag)
		// A field may declare several names (rare in the SDK) or be embedded.
		names := f.Names
		if len(names) == 0 { // embedded field: use the type's final identifier
			names = []*ast.Ident{{Name: lastIdent(typ)}}
		}
		for _, n := range names {
			fields = append(fields, Field{
				Name:          n.Name,
				Type:          typ,
				JSON:          tag.json,
				Mandatory:     tag.mandatory,
				ContributesTo: tag.contributesTo,
				PresentIn:     tag.presentIn,
				HTTPName:      tag.httpName,
				Doc:           docText(f.Doc, nil),
			})
		}
	}
	return fields
}

type structTag struct {
	json          string
	mandatory     bool
	contributesTo string
	presentIn     string
	httpName      string
}

func parseTag(lit *ast.BasicLit) structTag {
	var t structTag
	if lit == nil {
		return t
	}
	// lit.Value includes the surrounding back-quotes; strip them, then use the
	// stdlib reflect.StructTag parser so escaping matches the Go compiler.
	raw := strings.Trim(lit.Value, "`")
	st := reflect.StructTag(raw)
	if v, ok := st.Lookup("json"); ok {
		t.json = strings.Split(v, ",")[0] // drop ,omitempty and friends
	}
	t.mandatory = st.Get("mandatory") == "true"
	t.contributesTo = st.Get("contributesTo")
	t.presentIn = st.Get("presentIn")
	t.httpName = st.Get("name")
	return t
}

// typeString renders a field type back to source-like text, enough to
// distinguish scalars, pointers, slices, maps, and named/nested types.
func typeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.StarExpr:
		return "*" + typeString(e.X)
	case *ast.ArrayType:
		return "[]" + typeString(e.Elt)
	case *ast.MapType:
		return "map[" + typeString(e.Key) + "]" + typeString(e.Value)
	case *ast.SelectorExpr:
		return typeString(e.X) + "." + e.Sel.Name
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return fmt.Sprintf("%T", expr)
	}
}

func lastIdent(typ string) string {
	typ = strings.TrimLeft(typ, "*[]")
	if i := strings.LastIndex(typ, "."); i >= 0 {
		return typ[i+1:]
	}
	return typ
}

// docText joins non-empty comment groups into a single trimmed line.
func docText(groups ...*ast.CommentGroup) string {
	var parts []string
	for _, g := range groups {
		if g == nil {
			continue
		}
		if s := strings.TrimSpace(g.Text()); s != "" {
			parts = append(parts, strings.ReplaceAll(s, "\n", " "))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
