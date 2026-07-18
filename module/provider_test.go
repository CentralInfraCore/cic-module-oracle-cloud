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
}

func TestValidateWellFormed(t *testing.T) {
	req := validationRequest{
		Kind: "cic:network:vcn",
		Intent: schemaPayload{
			SchemaID: "cic:network:vcn-config", SchemaVersion: "v0.1.0",
			SchemaHash: "abc123", Encoding: encCanonicalJSON,
			Data: json.RawMessage(`{"cidr":"10.0.0.0/16"}`),
		},
	}
	data, _ := json.Marshal(req)
	out, err := Validate(nil, data)
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
	if !vr.Admissible {
		t.Errorf("admissible = false, want true (errors: %+v)", vr.Errors)
	}
	// Honesty contract: validate must report that it only checked the envelope,
	// not schema-conformance (that needs P2.3).
	if len(vr.Checked) == 0 || vr.Checked[0] != "envelope.well-formed" {
		t.Errorf("checked = %v, want [envelope.well-formed]", vr.Checked)
	}
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

func TestPlanNoop(t *testing.T) {
	payload := schemaPayload{
		SchemaID: "cic:network:vcn-config", SchemaVersion: "v0.1.0",
		SchemaHash: "abc123", Encoding: encCanonicalJSON,
		Data: json.RawMessage(`{"cidr":"10.0.0.0/16"}`),
	}
	// Same content, different key order/whitespace — canonicalEqual must see noop.
	observed := payload
	observed.Data = json.RawMessage(`{ "cidr" : "10.0.0.0/16" }`)
	req := planRequest{Kind: "cic:network:vcn", Desired: payload, Observed: observed}
	data, _ := json.Marshal(req)

	out, err := Plan(nil, data)
	if err != nil {
		t.Fatalf("Plan transport error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("Plan status = %q, want ok (raw: %s)", res.Status, out)
	}
	var p executionPlan
	json.Unmarshal(res.Result, &p)
	if p.Operation != "noop" {
		t.Errorf("operation = %q, want noop", p.Operation)
	}
	if len(p.PlanID) < len("sha256:") {
		t.Errorf("plan_id = %q, want a sha256 hash", p.PlanID)
	}
}

func TestPlanDiffIsScaffold(t *testing.T) {
	desired := schemaPayload{
		SchemaID: "cic:network:vcn-config", SchemaVersion: "v0.1.0",
		SchemaHash: "abc123", Encoding: encCanonicalJSON,
		Data: json.RawMessage(`{"cidr":"10.0.0.0/16"}`),
	}
	observed := desired
	observed.Data = json.RawMessage(`{"cidr":"10.1.0.0/16"}`) // differs
	req := planRequest{Kind: "cic:network:vcn", Desired: desired, Observed: observed}
	data, _ := json.Marshal(req)

	out, _ := Plan(nil, data)
	res := decodeResult(t, out)
	if res.Status != "error" || res.Error == nil || res.Error.ProviderCode != "PLAN_DIFF_UNAVAILABLE" {
		t.Errorf("non-noop plan: got %+v, want error/PLAN_DIFF_UNAVAILABLE", res)
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
