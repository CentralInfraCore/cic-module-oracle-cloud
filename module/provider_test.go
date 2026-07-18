//go:build !wasip1

package main

// provider_test.go unit-tests the cic:provider domain layer (provider.go)
// directly on the host — no wasm, no wazero. provider.go has no build tag, so
// these functions compile into this host test binary as well as the guest.

import (
	"encoding/json"
	"testing"
)

func decodeResult(t *testing.T, raw []byte) providerResult {
	t.Helper()
	var r providerResult
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("result is not a providerResult: %v (raw: %s)", err, raw)
	}
	return r
}

func TestDescribeManifest(t *testing.T) {
	out, err := Describe(nil, nil)
	if err != nil {
		t.Fatalf("Describe returned a transport error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("Describe status = %q, want ok", res.Status)
	}
	var m moduleManifest
	if err := json.Unmarshal(res.Result, &m); err != nil {
		t.Fatalf("manifest decode: %v", err)
	}
	if m.ABIVersion != abiVersion {
		t.Errorf("abi_version = %q, want %q", m.ABIVersion, abiVersion)
	}
	if m.Provider != providerName {
		t.Errorf("provider = %q, want %q", m.Provider, providerName)
	}
	if len(m.Operations) != len(providerOps) {
		t.Errorf("operations count = %d, want %d", len(m.Operations), len(providerOps))
	}
	for i, op := range providerOps {
		if i >= len(m.Operations) || m.Operations[i] != op {
			t.Errorf("operations[%d] = %v, want %q", i, m.Operations, op)
			break
		}
	}
	// The declared import surface (P0.3) must be reported, so a host can read the
	// module's sign+send requirement from describe().
	if len(m.Imports) != 1 || m.Imports[0].Module != "cic-flow" {
		t.Fatalf("imports = %+v, want [{cic-flow [sign actuate]}]", m.Imports)
	}
	if len(m.Imports[0].Functions) != 2 {
		t.Errorf("imports[0].functions = %v, want [sign actuate]", m.Imports[0].Functions)
	}
}

// validateVcn builds a validation-request for kind cic:network:vcn with the given
// intent data and runs Validate.
func validateVcn(t *testing.T, data string) validationResult {
	t.Helper()
	req := validationRequest{
		Kind: "cic:network:vcn",
		Intent: schemaPayload{
			SchemaID: "cic:network:vcn-config", SchemaVersion: "v0.1.0",
			SchemaHash: "abc123", Encoding: encCanonicalJSON,
			Data: json.RawMessage(data),
		},
	}
	raw, _ := json.Marshal(req)
	out, err := Validate(nil, raw)
	if err != nil {
		t.Fatalf("Validate transport error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("Validate status = %q, want ok", res.Status)
	}
	var vr validationResult
	if err := json.Unmarshal(res.Result, &vr); err != nil {
		t.Fatalf("validationResult decode: %v", err)
	}
	return vr
}

func TestValidateConformantIntent(t *testing.T) {
	vr := validateVcn(t, `{"compartmentId":"ocid1.compartment.oc1..aaaa","displayName":"prod","cidrBlocks":["10.0.0.0/16"]}`)
	if !vr.Admissible {
		t.Errorf("admissible = false, want true (errors: %+v)", vr.Errors)
	}
	// Now that a generated contract exists for the kind, validate must report
	// that it checked schema-conformance, not just the envelope.
	if len(vr.Checked) != 2 || vr.Checked[1] != "schema-conformance" {
		t.Errorf("checked = %v, want [envelope.well-formed schema-conformance]", vr.Checked)
	}
}

func TestValidateRejectsUnknownField(t *testing.T) {
	// lifecycleState is provider-computed (output-only) — not in the config
	// contract — so setting it must be rejected as an unknown field.
	vr := validateVcn(t, `{"compartmentId":"ocid1..","lifecycleState":"AVAILABLE"}`)
	if vr.Admissible {
		t.Errorf("admissible = true, want false for an output-only field in the intent")
	}
	if !hasFieldError(vr.Errors, "intent.data.lifecycleState") {
		t.Errorf("expected an error on intent.data.lifecycleState, got %+v", vr.Errors)
	}
}

func TestValidateRejectsMissingRequired(t *testing.T) {
	vr := validateVcn(t, `{"displayName":"x"}`) // no compartmentId (required)
	if vr.Admissible {
		t.Errorf("admissible = true, want false when required compartmentId is missing")
	}
	if !hasFieldError(vr.Errors, "intent.data.compartmentId") {
		t.Errorf("expected a required-field error on compartmentId, got %+v", vr.Errors)
	}
}

func TestValidateRejectsTypeMismatch(t *testing.T) {
	vr := validateVcn(t, `{"compartmentId":"ocid1..","cidrBlocks":"not-an-array"}`)
	if vr.Admissible {
		t.Errorf("admissible = true, want false for a type mismatch")
	}
	if !hasFieldError(vr.Errors, "intent.data.cidrBlocks") {
		t.Errorf("expected a type error on cidrBlocks, got %+v", vr.Errors)
	}
}

func hasFieldError(errs []providerError, path string) bool {
	for _, e := range errs {
		if e.FieldPath == path {
			return true
		}
	}
	return false
}

func TestValidateRejectsMalformedEnvelope(t *testing.T) {
	// Missing schema_id/hash, unknown encoding, no kind.
	req := validationRequest{
		Intent: schemaPayload{Encoding: "xml", Data: json.RawMessage(`{}`)},
	}
	data, _ := json.Marshal(req)
	out, _ := Validate(nil, data)
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("Validate status = %q, want ok (a non-admissible result is still an ok call)", res.Status)
	}
	var vr validationResult
	json.Unmarshal(res.Result, &vr)
	if vr.Admissible {
		t.Errorf("admissible = true, want false for a malformed envelope")
	}
	if len(vr.Errors) == 0 {
		t.Errorf("expected validation errors, got none")
	}
}

func TestValidateRejectsNonJSON(t *testing.T) {
	out, _ := Validate(nil, []byte("not-json"))
	res := decodeResult(t, out)
	if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation {
		t.Errorf("non-JSON request: got %+v, want error/validation", res)
	}
}

// planVcn runs Plan for kind cic:network:vcn with the given desired (config) and
// observed (state) JSON.
func planVcn(t *testing.T, desired, observed string) executionPlan {
	t.Helper()
	req := planRequest{
		Kind:     "cic:network:vcn",
		Desired:  schemaPayload{SchemaID: "cic:network:vcn-config", SchemaVersion: "v0.1.0", SchemaHash: "abc123", Encoding: encCanonicalJSON, Data: json.RawMessage(desired)},
		Observed: schemaPayload{SchemaID: "cic:network:vcn-state", SchemaVersion: "v0.1.0", SchemaHash: "abc123", Encoding: encCanonicalJSON, Data: json.RawMessage(observed)},
	}
	raw, _ := json.Marshal(req)
	out, err := Plan(nil, raw)
	if err != nil {
		t.Fatalf("Plan transport error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("Plan status = %q, want ok (raw: %s)", res.Status, out)
	}
	var p executionPlan
	if err := json.Unmarshal(res.Result, &p); err != nil {
		t.Fatalf("executionPlan decode: %v", err)
	}
	return p
}

func TestPlanClassifiesDiff(t *testing.T) {
	cases := []struct {
		name, desired, observed, wantOp string
	}{
		{"noop", `{"displayName":"prod"}`, `{"displayName":"prod"}`, "noop"},
		{"noop-canonical", `{"displayName":"prod"}`, `{ "displayName" : "prod" }`, "noop"},
		{"mutable-update", `{"displayName":"new"}`, `{"displayName":"old"}`, "update"},
		{"create-only-replace", `{"dnsLabel":"a"}`, `{"dnsLabel":"b"}`, "replace"},
		{"action-managed", `{"compartmentId":"a"}`, `{"compartmentId":"b"}`, "action"},
		// Absent desired field = unmanaged (tri-state): observed-only differences
		// do not force an operation.
		{"unmanaged", `{"displayName":"same"}`, `{"displayName":"same","lifecycleState":"AVAILABLE"}`, "noop"},
		// Escalation: a mutable + a create-only change → replace (most disruptive).
		{"escalate-to-replace", `{"displayName":"new","dnsLabel":"a"}`, `{"displayName":"old","dnsLabel":"b"}`, "replace"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := planVcn(t, c.desired, c.observed)
			if p.Operation != c.wantOp {
				t.Errorf("operation = %q, want %q (notes: %v)", p.Operation, c.wantOp, p.Notes)
			}
			if len(p.PlanID) < len("sha256:") {
				t.Errorf("plan_id = %q, want a sha256 hash", p.PlanID)
			}
		})
	}
}

func TestSignSendOpsAreScaffold(t *testing.T) {
	for _, h := range []struct {
		name string
		fn   func(a, d []byte) ([]byte, error)
	}{
		{"observe", Observe}, {"execute", Execute}, {"poll", Poll},
		{"invoke", Invoke}, {"destroy", Destroy},
	} {
		t.Run(h.name, func(t *testing.T) {
			out, err := h.fn(nil, []byte("{}"))
			if err != nil {
				t.Fatalf("%s transport error: %v", h.name, err)
			}
			res := decodeResult(t, out)
			if res.Status != "error" || res.Error == nil {
				t.Fatalf("%s: got %+v, want an error result", h.name, res)
			}
			if res.Error.Class != classTransport || res.Error.ProviderCode != "HOST_SIGN_SEND_UNAVAILABLE" {
				t.Errorf("%s: class/code = %q/%q, want transport/HOST_SIGN_SEND_UNAVAILABLE",
					h.name, res.Error.Class, res.Error.ProviderCode)
			}
		})
	}
}
