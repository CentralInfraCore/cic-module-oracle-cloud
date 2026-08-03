package ociextract

import (
	"reflect"
	"testing"
)

func TestExtractClientFile(t *testing.T) {
	ops, err := ExtractClientFile("testdata/vcn_client.go")
	if err != nil {
		t.Fatalf("ExtractClientFile: %v", err)
	}

	// Four real operations; the helper method (no *Response, no HTTPRequest)
	// must be skipped.
	if len(ops) != 4 {
		t.Fatalf("got %d operations, want 4: %+v", len(ops), ops)
	}

	byName := map[string]Operation{}
	for _, o := range ops {
		byName[o.Name] = o
	}

	want := map[string]Operation{
		"CreateVcn": {Name: "CreateVcn", Client: "VirtualNetworkClient", HTTPMethod: "POST", HTTPPath: "/vcns", Request: "CreateVcnRequest", Response: "CreateVcnResponse"},
		"GetVcn":    {Name: "GetVcn", Client: "VirtualNetworkClient", HTTPMethod: "GET", HTTPPath: "/vcns/{vcnId}", Request: "GetVcnRequest", Response: "GetVcnResponse"},
		"DeleteVcn": {Name: "DeleteVcn", Client: "VirtualNetworkClient", HTTPMethod: "DELETE", HTTPPath: "/vcns/{vcnId}", Request: "DeleteVcnRequest", Response: "DeleteVcnResponse"},
	}
	for name, w := range want {
		got, ok := byName[name]
		if !ok {
			t.Errorf("missing operation %q", name)
			continue
		}
		if got.Client != w.Client || got.HTTPMethod != w.HTTPMethod || got.HTTPPath != w.HTTPPath ||
			got.Request != w.Request || got.Response != w.Response {
			t.Errorf("%s = %+v, want %+v", name, got, w)
		}
	}

	if _, ok := byName["helperNoOp"]; ok {
		t.Errorf("helperNoOp should be skipped (not an operation)")
	}

	// Sorted by name: ChangeVcnCompartment, CreateVcn, DeleteVcn, GetVcn.
	var got []string
	for _, o := range ops {
		got = append(got, o.Name)
	}
	if want := []string{"ChangeVcnCompartment", "CreateVcn", "DeleteVcn", "GetVcn"}; !reflect.DeepEqual(got, want) {
		t.Errorf("operations = %v, want %v (sorted by name)", got, want)
	}

	// Doc comment kept.
	if got := byName["CreateVcn"].Doc; got == "" {
		t.Errorf("CreateVcn doc not captured")
	}
}
