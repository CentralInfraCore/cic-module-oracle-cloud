//go:build !wasip1

package main

// The noop decision of Plan rests on JSON equality. A float64 comparison called two
// different large integers equal and so planned "noop" for a real change; a duplicate
// key was silently resolved to its last value. These tests pin the exact behaviour.

import (
	"encoding/json"
	"testing"
)

const instanceKind = "cic:compute:instance"

func planInstance(t *testing.T, desired, observed string) (providerResult, executionPlan) {
	t.Helper()
	req := planRequest{
		Kind:     instanceKind,
		Desired:  schemaPayload{SchemaID: "cic:compute:instance-config", SchemaVersion: "v0.1.0", SchemaHash: "abc123", Encoding: encCanonicalJSON, Data: json.RawMessage(desired)},
		Observed: schemaPayload{SchemaID: "cic:compute:instance-state", SchemaVersion: "v0.1.0", SchemaHash: "abc124", Encoding: encCanonicalJSON, Data: json.RawMessage(observed)},
	}
	raw, _ := json.Marshal(req)
	out, err := Plan(nil, raw)
	if err != nil {
		t.Fatalf("Plan transport error: %v", err)
	}
	res := decodeResult(t, out)
	var plan executionPlan
	if res.Status == "ok" {
		if err := json.Unmarshal(res.Result, &plan); err != nil {
			t.Fatalf("plan result: %v", err)
		}
	}
	return res, plan
}

func TestPlanDoesNotCallDifferentLargeIntegersANoop(t *testing.T) {
	res, plan := planInstance(t,
		`{"shapeConfig":{"ocpus":9007199254740993},"displayName":"x"}`,
		`{"shapeConfig":{"ocpus":9007199254740992},"displayName":"x"}`)
	if res.Status != "ok" {
		t.Fatalf("status %q (%+v)", res.Status, res.Error)
	}
	if plan.Operation == "noop" || len(plan.ProviderOperations) == 0 {
		t.Fatalf("plan = %q with %d operations: two different numbers were treated as equal", plan.Operation, len(plan.ProviderOperations))
	}
}

func TestPlanStillCallsEquivalentJSONANoop(t *testing.T) {
	for _, c := range []struct{ name, desired, observed string }{
		{"number spelling", `{"shapeConfig":{"ocpus":1},"displayName":"x"}`, `{"shapeConfig":{"ocpus":1.0},"displayName":"x"}`},
		{"exponent spelling", `{"shapeConfig":{"ocpus":100},"displayName":"x"}`, `{"shapeConfig":{"ocpus":1e2},"displayName":"x"}`},
		{"key order and whitespace", `{"shapeConfig":{"ocpus":2,"memoryInGBs":16},"displayName":"x"}`, ` { "displayName":"x", "shapeConfig":{ "memoryInGBs":16, "ocpus":2 } } `},
		{"escaped text", `{"displayName":"é"}`, "{\"displayName\":\"\xc3\xa9\"}"},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, plan := planInstance(t, c.desired, c.observed)
			if res.Status != "ok" {
				t.Fatalf("status %q (%+v)", res.Status, res.Error)
			}
			if plan.Operation != "noop" || len(plan.ProviderOperations) != 0 {
				t.Fatalf("plan = %q with %d operations, want noop for equivalent JSON", plan.Operation, len(plan.ProviderOperations))
			}
		})
	}
}

func TestPlanRefusesDataWithDuplicateKeysOrBeyondTheBounds(t *testing.T) {
	good := `{"displayName":"x"}`
	cases := []struct{ name, desired, observed, field string }{
		{"duplicate key in desired", `{"displayName":"a","displayName":"b"}`, good, "desired.data"},
		{"duplicate key in observed", good, `{"displayName":"a","displayName":"b"}`, "observed.data"},
		{"nested duplicate key in desired", `{"shapeConfig":{"ocpus":1,"ocpus":2}}`, good, "desired.data"},
		{"too deep in observed", good, `{"displayName":"x","a":` + repeatStr("[", 70) + repeatStr("]", 70) + `}`, "observed.data"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, _ := planInstance(t, c.desired, c.observed)
			if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation {
				t.Fatalf("status %q error %+v, want a validation error", res.Status, res.Error)
			}
			if res.Error.FieldPath != c.field {
				t.Errorf("field_path = %q, want %q", res.Error.FieldPath, c.field)
			}
		})
	}
}

func repeatStr(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
