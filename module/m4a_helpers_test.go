//go:build !wasip1

package main

// m4a_helpers_test.go builds Execute requests that pass the M4a gate by asking the
// real Plan for them, never by writing digests by hand. A test of the GATE does not
// use these to build the request it expects to be refused: it changes the built
// request (or writes a whole one) directly, so the gate is checked against the
// request that arrives, not against how the helper builds it.

import (
	"encoding/json"
	"testing"
)

const m4aHash = "abc123"

// backslash is spelled as a hex escape so that no editor or tool between this file and the
// compiler can rewrite a u-escape that a test builds on top of it (that happened once: the
// "escape spelling" cases of this package compared identical text).
const backslash = "\x5c"

func m4aPayload(id, data string) schemaPayload {
	return schemaPayload{SchemaID: id, SchemaVersion: "v0.1.0", SchemaHash: m4aHash, Encoding: encCanonicalJSON, Data: json.RawMessage(data)}
}

// m4aPlan asks Plan for the plan of desired/observed on binding b.
func m4aPlan(t *testing.T, kind, desired, observed string, b execBinding) executionPlan {
	t.Helper()
	raw, _ := json.Marshal(planRequest{
		Kind:     kind,
		Desired:  m4aPayload(kind+"-config", desired),
		Observed: m4aPayload(kind+"-state", observed),
		Target:   b,
	})
	out, err := Plan(nil, raw)
	if err != nil {
		t.Fatalf("Plan transport error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("Plan status = %q, want ok (error: %+v)", res.Status, res.Error)
	}
	var p executionPlan
	if err := json.Unmarshal(res.Result, &p); err != nil {
		t.Fatalf("plan result: %v", err)
	}
	return p
}

// m4aRequest is the consistent Execute request for desired/observed on binding b: the
// plan Plan returned, the two states it came from, and desired as the config.
func m4aRequest(t *testing.T, kind, desired, observed string, b execBinding) executeRequest {
	t.Helper()
	return executeRequest{
		Kind:     kind,
		Plan:     m4aPlan(t, kind, desired, observed, b),
		Desired:  m4aPayload(kind+"-config", desired),
		Observed: m4aPayload(kind+"-state", observed),
		Config:   m4aPayload(kind+"-config", desired),
		Binding:  b,
	}
}

// m4aForOp builds the consistent request whose plan is exactly the one provider
// operation po, so a test about one operation's outcome (a create answer, an update's
// If-Match) can still run through the whole gate. The observed state is chosen from
// the operation's role: none for a create (and no resource id then), a different value
// for every field of desired otherwise. If Plan does not derive exactly [po] the setup
// is wrong and the test stops, so the helper can never quietly test another operation.
func m4aForOp(t *testing.T, kind string, po providerOperation, desired string, b execBinding) executeRequest {
	t.Helper()
	observed := `{}`
	if resourceContracts()[kind].operations[po.Operation].role == roleCreate {
		b.ResourceID = ""
	} else {
		var d map[string]json.RawMessage
		if err := json.Unmarshal([]byte(desired), &d); err != nil {
			t.Fatalf("desired is not an object: %v", err)
		}
		o := map[string]string{}
		for k := range d {
			o[k] = "observed-before"
		}
		raw, _ := json.Marshal(o)
		observed = string(raw)
	}
	req := m4aRequest(t, kind, desired, observed, b)
	if ops := req.Plan.ProviderOperations; len(ops) != 1 || ops[0].Operation != po.Operation || ops[0].Method != po.Method || ops[0].Path != po.Path {
		t.Fatalf("test setup: Plan derived %+v, the test wants exactly [%+v]", ops, po)
	}
	return req
}
