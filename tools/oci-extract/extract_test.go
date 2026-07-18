package ociextract

import "testing"

func models(t *testing.T) map[string]Model {
	t.Helper()
	got, err := ExtractFile("testdata/vcn.go")
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	m := make(map[string]Model, len(got))
	for _, model := range got {
		m[model.Name] = model
	}
	return m
}

func field(t *testing.T, m Model, name string) Field {
	t.Helper()
	for _, f := range m.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("%s has no field %q", m.Name, name)
	return Field{}
}

func TestExtractsAllStructs(t *testing.T) {
	m := models(t)
	for _, name := range []string{
		"CreateVcnDetails", "UpdateVcnDetails", "ChangeVcnCompartmentDetails",
		"Vcn", "CreateVcnRequest", "CreateVcnResponse",
	} {
		if _, ok := m[name]; !ok {
			t.Errorf("missing model %q", name)
		}
	}
	if len(m) != 6 {
		t.Errorf("expected 6 models, got %d", len(m))
	}
}

func TestMandatoryAndJSONName(t *testing.T) {
	details := models(t)["CreateVcnDetails"]

	comp := field(t, details, "CompartmentId")
	if !comp.Mandatory {
		t.Error("CompartmentId should be mandatory")
	}
	if comp.JSON != "compartmentId" {
		t.Errorf("CompartmentId json = %q, want compartmentId", comp.JSON)
	}
	if comp.Type != "*string" {
		t.Errorf("CompartmentId type = %q, want *string", comp.Type)
	}

	name := field(t, details, "DisplayName")
	if name.Mandatory {
		t.Error("DisplayName should be optional")
	}
}

func TestSliceAndMapTypes(t *testing.T) {
	details := models(t)["CreateVcnDetails"]
	if got := field(t, details, "CidrBlocks").Type; got != "[]string" {
		t.Errorf("CidrBlocks type = %q, want []string", got)
	}
	if got := field(t, details, "FreeformTags").Type; got != "map[string]string" {
		t.Errorf("FreeformTags type = %q, want map[string]string", got)
	}
}

func TestRequestContributesTo(t *testing.T) {
	req := models(t)["CreateVcnRequest"]

	// Embedded body model.
	body := field(t, req, "CreateVcnDetails")
	if body.ContributesTo != "body" {
		t.Errorf("embedded body contributesTo = %q, want body", body.ContributesTo)
	}

	// Header field with an explicit HTTP name.
	retry := field(t, req, "OpcRetryToken")
	if retry.ContributesTo != "header" {
		t.Errorf("OpcRetryToken contributesTo = %q, want header", retry.ContributesTo)
	}
	if retry.HTTPName != "opc-retry-token" {
		t.Errorf("OpcRetryToken httpName = %q, want opc-retry-token", retry.HTTPName)
	}
}

func TestResponsePresentIn(t *testing.T) {
	resp := models(t)["CreateVcnResponse"]

	if got := field(t, resp, "Vcn").PresentIn; got != "body" {
		t.Errorf("embedded Vcn presentIn = %q, want body", got)
	}
	etag := field(t, resp, "Etag")
	if etag.PresentIn != "header" {
		t.Errorf("Etag presentIn = %q, want header", etag.PresentIn)
	}
	if etag.HTTPName != "etag" {
		t.Errorf("Etag httpName = %q, want etag", etag.HTTPName)
	}
}

func TestDocCommentKept(t *testing.T) {
	details := models(t)["CreateVcnDetails"]
	if details.Doc == "" {
		t.Error("CreateVcnDetails doc comment was dropped")
	}
	if field(t, details, "CompartmentId").Doc == "" {
		t.Error("CompartmentId field doc was dropped")
	}
}
