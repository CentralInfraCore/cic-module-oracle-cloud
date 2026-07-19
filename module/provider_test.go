//go:build !wasip1

package main

// provider_test.go unit-tests the cic:provider domain layer (provider.go)
// directly on the host — no wasm, no wazero. provider.go has no build tag, so
// these functions compile into this host test binary as well as the guest.

import (
	"encoding/base64"
	"encoding/json"
	"strings"
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

// TestPlanProviderOperations checks that plan maps changed fields to the concrete
// OCI operations (P2.2 registry names), making the plan reviewable/signable.
func TestPlanProviderOperations(t *testing.T) {
	ops := func(p executionPlan) []string {
		var names []string
		for _, o := range p.ProviderOperations {
			names = append(names, o.Operation)
		}
		return names
	}

	// mutable change -> UpdateVcn, with its concrete HTTP method+path.
	p := planVcn(t, `{"displayName":"new"}`, `{"displayName":"old"}`)
	if got := ops(p); len(got) != 1 || got[0] != "UpdateVcn" {
		t.Errorf("displayName change: provider_operations = %v, want [UpdateVcn]", got)
	}
	if po := p.ProviderOperations[0]; po.Method != "PUT" || po.Path != "/vcns/{vcnId}" {
		t.Errorf("UpdateVcn HTTP = %s %s, want PUT /vcns/{vcnId}", po.Method, po.Path)
	}
	// action-managed change -> ChangeVcnCompartment
	if got := ops(planVcn(t, `{"compartmentId":"a"}`, `{"compartmentId":"b"}`)); len(got) != 1 || got[0] != "ChangeVcnCompartment" {
		t.Errorf("compartmentId change: provider_operations = %v, want [ChangeVcnCompartment]", got)
	}
	// immutable change -> Delete + Create (replace)
	if got := ops(planVcn(t, `{"dnsLabel":"a"}`, `{"dnsLabel":"b"}`)); len(got) != 2 || got[0] != "DeleteVcn" || got[1] != "CreateVcn" {
		t.Errorf("dnsLabel change: provider_operations = %v, want [DeleteVcn CreateVcn]", got)
	}
	// combined mutable + action -> UpdateVcn then ChangeVcnCompartment
	got := ops(planVcn(t, `{"displayName":"new","compartmentId":"a"}`, `{"displayName":"old","compartmentId":"b"}`))
	if len(got) != 2 || got[0] != "UpdateVcn" || got[1] != "ChangeVcnCompartment" {
		t.Errorf("mutable+action change: provider_operations = %v, want [UpdateVcn ChangeVcnCompartment]", got)
	}
	// noop -> no provider operations
	if got := ops(planVcn(t, `{"displayName":"same"}`, `{"displayName":"same"}`)); len(got) != 0 {
		t.Errorf("noop: provider_operations = %v, want none", got)
	}
}

// TestValidateSubnet proves the pipeline generalizes past VCN: a second embedded
// contract (cic:network:subnet, two required fields) validates the same way.
func TestValidateSubnet(t *testing.T) {
	run := func(data string) validationResult {
		req := validationRequest{
			Kind:   "cic:network:subnet",
			Intent: schemaPayload{SchemaID: "cic:network:subnet-config", SchemaVersion: "v0.1.0", SchemaHash: "abc123", Encoding: encCanonicalJSON, Data: json.RawMessage(data)},
		}
		raw, _ := json.Marshal(req)
		out, err := Validate(nil, raw)
		if err != nil {
			t.Fatalf("Validate transport error: %v", err)
		}
		var vr validationResult
		json.Unmarshal(decodeResult(t, out).Result, &vr)
		return vr
	}

	if vr := run(`{"compartmentId":"ocid1..","vcnId":"ocid1.vcn..","displayName":"web"}`); !vr.Admissible {
		t.Errorf("conformant subnet intent: admissible=false, want true (errors: %+v)", vr.Errors)
	}
	// vcnId is the second required field — omitting it must fail.
	if vr := run(`{"compartmentId":"ocid1.."}`); vr.Admissible || !hasFieldError(vr.Errors, "intent.data.vcnId") {
		t.Errorf("subnet without vcnId: want required-field error on vcnId, got %+v", vr.Errors)
	}
}

// TestPlanSubnet proves plan classification works for the second resource.
func TestPlanSubnet(t *testing.T) {
	plan := func(desired, observed string) executionPlan {
		req := planRequest{
			Kind:     "cic:network:subnet",
			Desired:  schemaPayload{SchemaID: "cic:network:subnet-config", SchemaVersion: "v0.1.0", SchemaHash: "abc123", Encoding: encCanonicalJSON, Data: json.RawMessage(desired)},
			Observed: schemaPayload{SchemaID: "cic:network:subnet-state", SchemaVersion: "v0.1.0", SchemaHash: "abc123", Encoding: encCanonicalJSON, Data: json.RawMessage(observed)},
		}
		raw, _ := json.Marshal(req)
		out, err := Plan(nil, raw)
		if err != nil {
			t.Fatalf("Plan transport error: %v", err)
		}
		var p executionPlan
		json.Unmarshal(decodeResult(t, out).Result, &p)
		return p
	}

	// compartmentId is action-managed for subnet too.
	if p := plan(`{"compartmentId":"a"}`, `{"compartmentId":"b"}`); p.Operation != "action" {
		t.Errorf("subnet compartmentId change: op = %q, want action", p.Operation)
	}
	// displayName is mutable → update.
	if p := plan(`{"displayName":"new"}`, `{"displayName":"old"}`); p.Operation != "update" {
		t.Errorf("subnet displayName change: op = %q, want update", p.Operation)
	}
}

// TestObserve drives observe against injected cic-flow mocks (host build): sign
// returns a signature, actuate returns a canned VCN read. It checks that
// effective_config is the config-surface projection (settable fields only), that
// raw state keeps everything, and that the revision (etag) is surfaced.
// TestExecuteAsyncAndBasePath checks the OCI API version prefix is prepended and
// that a 202 with an opc-work-request-id yields an "accepted" result carrying the
// work-request id to poll (not a false "succeeded").
func TestExecuteAsyncAndBasePath(t *testing.T) {
	testCallHostSign = func(req []byte) ([]byte, error) { return []byte(`{"signature":"S"}`), nil }
	var gotURL string
	testCallHostActuate = func(req []byte) ([]byte, error) {
		var r struct {
			URL string `json:"url"`
		}
		json.Unmarshal(req, &r)
		gotURL = r.URL
		out, _ := json.Marshal(map[string]interface{}{"status": 202, "headers": map[string]string{"opc-work-request-id": "ocid1.wr..a"}})
		return out, nil
	}
	defer func() { testCallHostSign = nil; testCallHostActuate = nil }()

	req, _ := json.Marshal(executeRequest{
		Kind:    "cic:network:vcn",
		Plan:    executionPlan{ProviderOperations: []providerOperation{{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"}}},
		Config:  schemaPayload{Data: json.RawMessage(`{"displayName":"x"}`)},
		Binding: execBinding{Host: "h", BasePath: "/20160918", KeyID: "k", ResourceID: "ocid1.vcn..z"},
	})
	out, _ := Execute(nil, req)
	var er executionResult
	json.Unmarshal(decodeResult(t, out).Result, &er)
	if er.Status != "accepted" {
		t.Errorf("status = %q, want accepted", er.Status)
	}
	if len(er.Steps) != 1 || er.Steps[0].WorkRequestID != "ocid1.wr..a" {
		t.Errorf("steps = %+v, want work_request_id ocid1.wr..a", er.Steps)
	}
	if gotURL != "https://h/20160918/vcns/ocid1.vcn..z" {
		t.Errorf("url = %q, want the base_path-prefixed path", gotURL)
	}
}

func TestObserve(t *testing.T) {
	testCallHostSign = func(req []byte) ([]byte, error) { return []byte(`{"signature":"S"}`), nil }
	testCallHostActuate = func(req []byte) ([]byte, error) {
		vcn := `{"id":"ocid1.vcn..x","compartmentId":"ocid1.compartment..c","displayName":"prod","dnsLabel":"prod","lifecycleState":"AVAILABLE","cidrBlocks":["10.0.0.0/16"],"ipv6CidrBlocks":["2603:c020::/48"],"timeCreated":"2026-07-19T00:00:00Z"}`
		out, _ := json.Marshal(map[string]interface{}{
			"status":      200,
			"headers":     map[string]string{"etag": "etag-9", "opc-request-id": "req-9"},
			"body_base64": base64.StdEncoding.EncodeToString([]byte(vcn)),
		})
		return out, nil
	}
	defer func() { testCallHostSign = nil; testCallHostActuate = nil }()

	req, _ := json.Marshal(observeRequest{
		Kind:    "cic:network:vcn",
		Binding: execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", KeyID: "t/u/f", ResourceID: "ocid1.vcn..x"},
	})
	out, err := Observe(nil, req)
	if err != nil {
		t.Fatalf("Observe transport error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("observe status = %q, want ok (raw: %s)", res.Status, out)
	}
	var obs observation
	if err := json.Unmarshal(res.Result, &obs); err != nil {
		t.Fatalf("observation decode: %v", err)
	}

	// effective_config is the settable surface only.
	for _, f := range []string{"displayName", "compartmentId", "dnsLabel", "cidrBlocks"} {
		if _, ok := obs.EffectiveConfig[f]; !ok {
			t.Errorf("effective_config missing settable field %q", f)
		}
	}
	// provider-computed / output-only fields must NOT be in effective_config.
	for _, f := range []string{"id", "lifecycleState", "timeCreated"} {
		if _, ok := obs.EffectiveConfig[f]; ok {
			t.Errorf("effective_config must not include output-only field %q", f)
		}
		// ...but raw state keeps them.
		if _, ok := obs.State[f]; !ok {
			t.Errorf("state missing %q", f)
		}
	}
	if obs.ProviderMetadata.Etag != "etag-9" || obs.ProviderMetadata.ResourceID != "ocid1.vcn..x" {
		t.Errorf("provider_metadata = %+v, want etag-9 / ocid1.vcn..x", obs.ProviderMetadata)
	}

	// Semantic correspondence: the input-only isIpv6Enabled is derived from the
	// non-empty ipv6CidrBlocks state field, so effective_config carries it even
	// though the read never returns isIpv6Enabled directly.
	if string(obs.EffectiveConfig["isIpv6Enabled"]) != "true" {
		t.Errorf("effective_config.isIpv6Enabled = %s, want true (derived from ipv6CidrBlocks)", obs.EffectiveConfig["isIpv6Enabled"])
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

// TestSignSendOpsRequireBinding verifies every sign+send op fails cleanly (a
// validation domain-error) without a binding — before any host call.
func TestSignSendOpsRequireBinding(t *testing.T) {
	for _, h := range []struct {
		name string
		fn   func(a, d []byte) ([]byte, error)
	}{
		{"execute", Execute}, {"observe", Observe}, {"poll", Poll},
		{"invoke", Invoke}, {"destroy", Destroy},
	} {
		t.Run(h.name, func(t *testing.T) {
			out, err := h.fn(nil, []byte("{}"))
			if err != nil {
				t.Fatalf("%s transport error: %v", h.name, err)
			}
			res := decodeResult(t, out)
			if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation {
				t.Errorf("%s without binding: got %+v, want a validation error", h.name, res)
			}
		})
	}
}

func TestDestroy(t *testing.T) {
	testCallHostSign = func(req []byte) ([]byte, error) { return []byte(`{"signature":"S"}`), nil }
	var gotURL string
	testCallHostActuate = func(req []byte) ([]byte, error) {
		var r struct{ Method, URL string }
		json.Unmarshal(req, &r)
		gotURL = r.Method + " " + r.URL
		out, _ := json.Marshal(map[string]interface{}{"status": 204, "headers": map[string]string{"opc-request-id": "d-1"}})
		return out, nil
	}
	defer func() { testCallHostSign = nil; testCallHostActuate = nil }()

	req, _ := json.Marshal(destroyRequest{Kind: "cic:network:vcn", Binding: execBinding{Host: "h", KeyID: "k", ResourceID: "ocid1.vcn..z"}})
	out, _ := Destroy(nil, req)
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("destroy status %s (raw %s)", res.Status, out)
	}
	var er executionResult
	json.Unmarshal(res.Result, &er)
	if er.Status != "succeeded" || len(er.Steps) != 1 || er.Steps[0].HTTPStatus != 204 {
		t.Errorf("destroy result = %+v, want succeeded/1 step/204", er)
	}
	if gotURL != "DELETE https://h/vcns/ocid1.vcn..z" {
		t.Errorf("destroy actuated %q, want DELETE https://h/vcns/ocid1.vcn..z", gotURL)
	}
}

func TestInvoke(t *testing.T) {
	testCallHostSign = func(req []byte) ([]byte, error) { return []byte(`{"signature":"S"}`), nil }
	var gotURL, gotBody string
	testCallHostActuate = func(req []byte) ([]byte, error) {
		var r struct {
			Method     string `json:"method"`
			URL        string `json:"url"`
			BodyBase64 string `json:"body_base64"`
		}
		json.Unmarshal(req, &r)
		b, _ := base64.StdEncoding.DecodeString(r.BodyBase64)
		gotURL, gotBody = r.Method+" "+r.URL, string(b)
		out, _ := json.Marshal(map[string]interface{}{"status": 200, "headers": map[string]string{"etag": "i-1", "opc-request-id": "i-1"}})
		return out, nil
	}
	defer func() { testCallHostSign = nil; testCallHostActuate = nil }()

	req, _ := json.Marshal(invokeRequest{
		Kind:      "cic:network:vcn",
		Operation: "ChangeVcnCompartment",
		Config:    schemaPayload{Data: json.RawMessage(`{"compartmentId":"ocid1.compartment..new"}`)},
		Binding:   execBinding{Host: "h", KeyID: "k", ResourceID: "ocid1.vcn..z"},
	})
	out, _ := Invoke(nil, req)
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("invoke status %s (raw %s)", res.Status, out)
	}
	var or operationResult
	json.Unmarshal(res.Result, &or)
	if or.Status != "succeeded" || or.HTTPStatus != 200 || or.Etag != "i-1" {
		t.Errorf("invoke result = %+v, want succeeded/200/i-1", or)
	}
	if gotURL != "POST https://h/vcns/ocid1.vcn..z/actions/changeCompartment" {
		t.Errorf("invoke actuated %q", gotURL)
	}
	if !strings.Contains(gotBody, "ocid1.compartment..new") {
		t.Errorf("invoke body = %q, want the compartmentId", gotBody)
	}
}

func TestPoll(t *testing.T) {
	testCallHostSign = func(req []byte) ([]byte, error) { return []byte(`{"signature":"S"}`), nil }
	testCallHostActuate = func(req []byte) ([]byte, error) {
		wr := `{"status":"SUCCEEDED","percentComplete":100}`
		out, _ := json.Marshal(map[string]interface{}{"status": 200, "headers": map[string]string{}, "body_base64": base64.StdEncoding.EncodeToString([]byte(wr))})
		return out, nil
	}
	defer func() { testCallHostSign = nil; testCallHostActuate = nil }()

	req, _ := json.Marshal(pollRequest{Binding: execBinding{Host: "h", KeyID: "k"}, Path: "/20160918/workRequests/ocid1.wr..a"})
	out, _ := Poll(nil, req)
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("poll status %s (raw %s)", res.Status, out)
	}
	var pr pollResult
	json.Unmarshal(res.Result, &pr)
	if pr.WorkStatus != "SUCCEEDED" || pr.PercentComplete != 100 || !pr.Terminal {
		t.Errorf("poll result = %+v, want SUCCEEDED/100/terminal", pr)
	}
}
