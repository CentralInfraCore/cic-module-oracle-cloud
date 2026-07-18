package ociextract

import "testing"

func bundle(required string, props string) []byte {
	return []byte(`{"config":{"$id":"x-config","required":[` + required + `],"properties":{` + props + `}}}`)
}

func TestDiffConfigSchemas(t *testing.T) {
	old := bundle(`"a"`, `
		"a":{"type":"string","x-cic-policy":"mutable"},
		"b":{"type":"string","x-cic-policy":"create-only"},
		"c":{"type":"integer","x-cic-policy":"mutable"},
		"d":{"type":"string","x-cic-policy":"mutable"}`)
	// Changes vs old:
	//   a: required->required, unchanged
	//   b: removed                                  -> breaking
	//   c: type integer->string                     -> breaking
	//   d: policy mutable->create-only              -> compatible
	//   e: added, required                          -> breaking (added-required)
	//   f: added, optional                          -> compatible
	//   a: now also... stays required (no change)
	newB := bundle(`"a","e"`, `
		"a":{"type":"string","x-cic-policy":"mutable"},
		"c":{"type":"string","x-cic-policy":"mutable"},
		"d":{"type":"string","x-cic-policy":"create-only"},
		"e":{"type":"string","x-cic-policy":"mutable"},
		"f":{"type":"string","x-cic-policy":"mutable"}`)

	d, err := DiffConfigSchemas(old, newB)
	if err != nil {
		t.Fatalf("DiffConfigSchemas: %v", err)
	}

	breaking := map[string]string{}
	for _, c := range d.Breaking {
		breaking[c.Field] = c.Kind
	}
	compat := map[string]string{}
	for _, c := range d.Compatible {
		compat[c.Field] = c.Kind
	}

	wantBreaking := map[string]string{"b": ChangeRemoved, "c": ChangeTypeChanged, "e": ChangeAddedReq}
	for f, k := range wantBreaking {
		if breaking[f] != k {
			t.Errorf("breaking[%s] = %q, want %q (all breaking: %+v)", f, breaking[f], k, d.Breaking)
		}
	}
	if len(d.Breaking) != 3 {
		t.Errorf("breaking count = %d, want 3: %+v", len(d.Breaking), d.Breaking)
	}

	wantCompat := map[string]string{"d": ChangePolicy, "f": ChangeAdded}
	for f, k := range wantCompat {
		if compat[f] != k {
			t.Errorf("compatible[%s] = %q, want %q (all: %+v)", f, compat[f], k, d.Compatible)
		}
	}
}

func TestDiffNowRequired(t *testing.T) {
	old := bundle(``, `"a":{"type":"string","x-cic-policy":"mutable"}`)
	newB := bundle(`"a"`, `"a":{"type":"string","x-cic-policy":"mutable"}`)
	d, err := DiffConfigSchemas(old, newB)
	if err != nil {
		t.Fatalf("DiffConfigSchemas: %v", err)
	}
	if len(d.Breaking) != 1 || d.Breaking[0].Kind != ChangeNowRequired {
		t.Errorf("breaking = %+v, want [now-required a]", d.Breaking)
	}
}

func TestDiffIdentical(t *testing.T) {
	b := bundle(`"a"`, `"a":{"type":"string","x-cic-policy":"mutable"}`)
	d, err := DiffConfigSchemas(b, b)
	if err != nil {
		t.Fatalf("DiffConfigSchemas: %v", err)
	}
	if len(d.Breaking) != 0 || len(d.Compatible) != 0 {
		t.Errorf("identical schemas differ: %+v", d)
	}
}
