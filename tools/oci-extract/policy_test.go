package ociextract

import "testing"

func TestResourcePolicyVcn(t *testing.T) {
	models, err := ExtractFile("testdata/vcn.go")
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	got := ResourcePolicy(models, "Vcn")

	byField := map[string]FieldPolicy{}
	for _, fp := range got {
		byField[fp.Field] = fp
	}

	// The spec's example table (oci-schema-pipeline.md, P2.3), plus the
	// non-obvious action case that distinguishes create-only from action.
	want := map[string]string{
		"compartmentId":  PolicyAction,     // not in Update, but ChangeVcnCompartmentDetails
		"displayName":    PolicyMutable,    // in Update
		"cidrBlocks":     PolicyMutable,    // in Update
		"freeformTags":   PolicyMutable,    // in Update (not read back — still mutable)
		"dnsLabel":       PolicyCreateOnly, // create + read, no Update, no action
		"lifecycleState": PolicyOutputOnly, // read only
		"id":             PolicyOutputOnly, // read only
		"timeCreated":    PolicyOutputOnly, // read only
	}
	for field, policy := range want {
		fp, ok := byField[field]
		if !ok {
			t.Errorf("missing field %q in policy", field)
			continue
		}
		if fp.Policy != policy {
			t.Errorf("%s: policy = %q, want %q (inC=%v inU=%v inR=%v)",
				field, fp.Policy, policy, fp.InCreate, fp.InUpdate, fp.InRead)
		}
	}

	// The action classification must name the action model, not silently
	// mislabel compartmentId as create-only (the crucial P2.3 rule).
	if fp := byField["compartmentId"]; fp.Action != "ChangeVcnCompartmentDetails" {
		t.Errorf("compartmentId action = %q, want ChangeVcnCompartmentDetails", fp.Action)
	}

	// Output is sorted and complete (8 distinct fields across the models).
	if len(got) != 8 {
		t.Errorf("got %d field policies, want 8: %+v", len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i-1].Field > got[i].Field {
			t.Errorf("field policies not sorted: %q before %q", got[i-1].Field, got[i].Field)
		}
	}
}

// TestDeriveFieldPolicyNoUpdateModel covers a resource with no Update model:
// create+read fields become create-only (not mutable), unless an action applies.
func TestDeriveFieldPolicyNoUpdateModel(t *testing.T) {
	create := Model{Name: "CreateXDetails", Fields: []Field{
		{Name: "A", JSON: "a"}, {Name: "B", JSON: "b"},
	}}
	read := Model{Name: "X", Fields: []Field{
		{Name: "A", JSON: "a"}, {Name: "S", JSON: "state"},
	}}
	got := DeriveFieldPolicy(create, Model{}, read, nil)

	byField := map[string]FieldPolicy{}
	for _, fp := range got {
		byField[fp.Field] = fp
	}
	if byField["a"].Policy != PolicyCreateOnly {
		t.Errorf("a: policy = %q, want create-only", byField["a"].Policy)
	}
	if byField["b"].Policy != PolicyInputOnly { // create, never read back
		t.Errorf("b: policy = %q, want input-only", byField["b"].Policy)
	}
	if byField["state"].Policy != PolicyOutputOnly {
		t.Errorf("state: policy = %q, want output-only", byField["state"].Policy)
	}
}
