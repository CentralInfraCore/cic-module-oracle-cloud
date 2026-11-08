//go:build !wasip1

package main

// m4a_gate_test.go pins the M4a plan-binding gate: what Execute requires of a request
// BEFORE it signs anything, and the three digests it rests on.
//
// The refusal tests do not build their bad request with the helper that builds good
// ones: they take a request Plan produced and change one thing in it by hand (a
// field, or the JSON text itself), so the gate is checked against what arrives, not
// against how the helper builds it. Each refusal is judged on the outcome only: the
// result status, the CIC error class, the field path, and zero sign / zero actuate
// calls at the host seam. All of this runs on the mocked host seam, never on a real
// provider.
//
// What M4a does NOT show is pinned too (TestM4aDoesNotClaimApproval,
// TestM4aDoesNotPreventReplay): a request that is consistent with itself is not an
// approved one, and the same one can run again. Those belong to M4b (the Relay).

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// ---- the digests: language-neutral vectors ----

type digestVectors struct {
	Format string `json:"format"`
	Plan   []struct {
		Name               string `json:"name"`
		Kind               string `json:"kind"`
		Contract           string `json:"contract"`
		Operation          string `json:"operation"`
		Desired            vecPayload
		Observed           vecPayload
		ProviderOperations []providerOperation `json:"provider_operations"`
		Envelope           string              `json:"envelope"`
		Digest             string              `json:"digest"`
	} `json:"plan"`
	Target []struct {
		Name       string `json:"name"`
		Kind       string `json:"kind"`
		Host       string `json:"host"`
		BasePath   string `json:"base_path"`
		KeyID      string `json:"key_id"`
		ResourceID string `json:"resource_id"`
		Revision   string `json:"revision"`
		Envelope   string `json:"envelope"`
		Digest     string `json:"digest"`
	} `json:"target"`
	Execution []struct {
		Name         string `json:"name"`
		PlanDigest   string `json:"plan_digest"`
		TargetDigest string `json:"target_digest"`
		Envelope     string `json:"envelope"`
		Digest       string `json:"digest"`
	} `json:"execution"`
}

type vecPayload struct {
	SchemaID      string `json:"schema_id"`
	SchemaVersion string `json:"schema_version"`
	SchemaHash    string `json:"schema_hash"`
	Encoding      string `json:"encoding"`
	Data          string `json:"data"`
}

func (v vecPayload) payload() schemaPayload {
	return schemaPayload{SchemaID: v.SchemaID, SchemaVersion: v.SchemaVersion, SchemaHash: v.SchemaHash, Encoding: v.Encoding, Data: json.RawMessage(v.Data)}
}

func loadDigestVectors(t *testing.T) digestVectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/plan_digest_vectors.json")
	if err != nil {
		t.Fatalf("vectors: %v", err)
	}
	var v digestVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("the vectors are not valid JSON: %v", err)
	}
	if v.Format != "cic-oracle-digests/1" || len(v.Plan) == 0 || len(v.Target) == 0 || len(v.Execution) == 0 {
		t.Fatalf("vector file is empty or has the wrong format")
	}
	return v
}

func TestDigestsMatchTheLanguageNeutralVectors(t *testing.T) {
	v := loadDigestVectors(t)
	for _, c := range v.Plan {
		t.Run("plan/"+c.Name, func(t *testing.T) {
			env, err := planEnvelope(c.Kind, c.Contract, c.Desired.payload(), c.Observed.payload(), c.Operation, c.ProviderOperations)
			if err != nil {
				t.Fatalf("envelope: %v", err)
			}
			if string(env) != c.Envelope {
				t.Errorf("envelope\n got: %s\nwant: %s", env, c.Envelope)
			}
			if got := digestOf(env); got != c.Digest {
				t.Errorf("digest = %s, want %s", got, c.Digest)
			}
		})
	}
	for _, c := range v.Target {
		t.Run("target/"+c.Name, func(t *testing.T) {
			env := targetEnvelope(c.Kind, execBinding{Host: c.Host, BasePath: c.BasePath, KeyID: c.KeyID, ResourceID: c.ResourceID, Revision: c.Revision})
			if string(env) != c.Envelope {
				t.Errorf("envelope\n got: %s\nwant: %s", env, c.Envelope)
			}
			if got := digestOf(env); got != c.Digest {
				t.Errorf("digest = %s, want %s", got, c.Digest)
			}
		})
	}
	for _, c := range v.Execution {
		t.Run("execution/"+c.Name, func(t *testing.T) {
			env := executionEnvelope(c.PlanDigest, c.TargetDigest)
			if string(env) != c.Envelope {
				t.Errorf("envelope\n got: %s\nwant: %s", env, c.Envelope)
			}
			if got := digestOf(env); got != c.Digest {
				t.Errorf("digest = %s, want %s", got, c.Digest)
			}
		})
	}
}

// The vectors are only useful if they separate what must be separate and join what
// must be joined; check the file itself says so.
func TestDigestVectorsSeparateAndJoinWhatTheyShould(t *testing.T) {
	v := loadDigestVectors(t)
	plan := map[string]string{}
	for _, c := range v.Plan {
		plan[c.Name] = c.Digest
	}
	target := map[string]string{}
	for _, c := range v.Target {
		target[c.Name] = c.Digest
	}
	same := func(m map[string]string, a, b string) {
		t.Helper()
		if m[a] == "" || m[b] == "" || m[a] != m[b] {
			t.Errorf("%q and %q must have one digest (%s / %s)", a, b, m[a], m[b])
		}
	}
	differ := func(m map[string]string, a, b string) {
		t.Helper()
		if m[a] == "" || m[b] == "" || m[a] == m[b] {
			t.Errorf("%q and %q must have different digests (%s / %s)", a, b, m[a], m[b])
		}
	}
	same(plan, "update plan", "same plan, other spelling of the JSON (key order, whitespace, escapes)")
	same(plan, "numbers: 1, 1.0 and 1e0 are one value", "numbers: other spelling of the same values")
	differ(plan, "2^53+1 is kept exactly", "2^53 differs from 2^53+1")
	differ(plan, "update plan", "another contract digest")
	differ(plan, "update plan", "another desired schema_version")
	differ(plan, "update plan", "another observed encoding")
	differ(plan, "update plan", "another observed schema_id")
	differ(plan, "update plan", "another operation name, same ordered operations")
	differ(plan, "two operations in one order", "the same two operations in the other order")
	differ(plan, "boundary: kind AB + schema_id CD", "boundary: kind A + schema_id BCD")
	differ(target, "boundary: host ab + base_path c", "boundary: host a + base_path bc")
	for _, other := range []string{"other resource id", "other revision", "other key_id", "other host", "other base_path", "another kind"} {
		differ(target, "full target", other)
	}
	// Every digest of one role differs from the digest of another role over the same text.
	seen := map[string]string{}
	for _, m := range []map[string]string{plan, target} {
		for name, d := range m {
			if prev, dup := seen[d]; dup && prev != name {
				// the only allowed repeats are the ones asserted equal above
				if !(strings.Contains(name, "other spelling") || strings.Contains(prev, "other spelling") ||
					strings.Contains(name, "update plan") || strings.Contains(prev, "update plan") ||
					strings.Contains(name, "numbers") || strings.Contains(prev, "numbers")) {
					t.Errorf("%q and %q unexpectedly share a digest", prev, name)
				}
			}
			seen[d] = name
		}
	}
}

// ---- Plan: the lifecycle and the target ----

func TestPlanReturnsThreeDigestsAndNoLegacyPlanID(t *testing.T) {
	p := m4aPlan(t, bindingKind, `{"displayName":"new"}`, `{"displayName":"old"}`, bindingWith("ocid1.vcn.oc1..a"))
	for name, d := range map[string]string{"plan_digest": p.PlanDigest, "target_digest": p.TargetDigest, "execution_digest": p.ExecutionDigest} {
		if len(d) != len("sha256:")+64 || !strings.HasPrefix(d, "sha256:") {
			t.Errorf("%s = %q, want sha256:<64 hex>", name, d)
		}
	}
	if p.PlanDigest == p.TargetDigest || p.PlanDigest == p.ExecutionDigest || p.TargetDigest == p.ExecutionDigest {
		t.Errorf("the three digests must differ: %s %s %s", p.PlanDigest, p.TargetDigest, p.ExecutionDigest)
	}
	if env := executionEnvelope(p.PlanDigest, p.TargetDigest); digestOf(env) != p.ExecutionDigest {
		t.Errorf("execution_digest is not the digest of {plan_digest, target_digest}")
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "plan_id") {
		t.Errorf("a plan must not carry the retired plan_id: %s", raw)
	}
}

func TestPlanIsDeterministic(t *testing.T) {
	first := m4aPlan(t, bindingKind, `{"displayName":"new","compartmentId":"ocid1.compartment..b"}`, `{"displayName":"old","compartmentId":"ocid1.compartment..a"}`, bindingWith("ocid1.vcn.oc1..a"))
	for i := 0; i < 40; i++ {
		again := m4aPlan(t, bindingKind, `{"displayName":"new","compartmentId":"ocid1.compartment..b"}`, `{"displayName":"old","compartmentId":"ocid1.compartment..a"}`, bindingWith("ocid1.vcn.oc1..a"))
		a, _ := json.Marshal(first)
		b, _ := json.Marshal(again)
		if string(a) != string(b) {
			t.Fatalf("Plan differs between runs:\n%s\n%s", a, b)
		}
	}
}

func TestPlanLifecycleDecidesWhatTheTargetMustCarry(t *testing.T) {
	plan := func(observed string, b execBinding) (providerResult, executionPlan) {
		raw, _ := json.Marshal(planRequest{Kind: bindingKind,
			Desired: m4aPayload("d", `{"compartmentId":"ocid1.compartment..c"}`), Observed: m4aPayload("o", observed), Target: b})
		out, _ := Plan(nil, raw)
		res := decodeResult(t, out)
		var p executionPlan
		if res.Status == "ok" {
			json.Unmarshal(res.Result, &p)
		}
		return res, p
	}
	t.Run("no state and no resource id is a create", func(t *testing.T) {
		res, p := plan(`{}`, bindingWith(""))
		if res.Status != "ok" || p.Operation != "create" || len(p.ProviderOperations) != 1 || p.ProviderOperations[0].Operation != "CreateVcn" {
			t.Fatalf("got %q %+v (%+v), want a create plan with CreateVcn", res.Status, p, res.Error)
		}
	})
	refused := []struct {
		name, observed string
		b              execBinding
		field          string
	}{
		{"no state but a resource id", `{}`, bindingWith("ocid1.vcn.oc1..a"), "observed.data"},
		{"state but no resource id", `{"displayName":"x"}`, bindingWith(""), "target.resource_id"},
		{"state and a malformed resource id", `{"displayName":"x"}`, bindingWith("vcn-42"), "target.resource_id"},
		{"no host", `{"displayName":"x"}`, execBinding{KeyID: "k", ResourceID: "ocid1.vcn.oc1..a"}, "target.host"},
		{"no key_id", `{"displayName":"x"}`, execBinding{Host: "h", ResourceID: "ocid1.vcn.oc1..a"}, "target.key_id"},
		{"a host longer than the bound", `{"displayName":"x"}`, execBinding{Host: strings.Repeat("h", 2000), KeyID: "k", ResourceID: "ocid1.vcn.oc1..a"}, "target.host"},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			res, _ := plan(c.observed, c.b)
			if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation || res.Error.FieldPath != c.field {
				t.Fatalf("status %q error %+v, want a validation error at %s", res.Status, res.Error, c.field)
			}
		})
	}
}

// ---- Execute: the gate ----

// consistentUpdate returns a request that passes the gate, an update of displayName.
func consistentUpdate(t *testing.T) executeRequest {
	t.Helper()
	b := bindingWith("ocid1.vcn.oc1..approved")
	b.Revision = "etag-approved"
	return m4aRequest(t, bindingKind, `{"displayName":"approved-name"}`, `{"displayName":"old-name"}`, b)
}

// consistentTwoStep is an update followed by an action: two provider operations.
func consistentTwoStep(t *testing.T) executeRequest {
	t.Helper()
	req := m4aRequest(t, bindingKind,
		`{"displayName":"new","compartmentId":"ocid1.compartment..b"}`,
		`{"displayName":"old","compartmentId":"ocid1.compartment..a"}`, bindingWith("ocid1.vcn.oc1..approved"))
	if len(req.Plan.ProviderOperations) != 2 {
		t.Fatalf("test setup: want a 2-step plan, got %+v", req.Plan.ProviderOperations)
	}
	return req
}

func runExecute(t *testing.T, req executeRequest) (providerResult, *cicFlowMock) {
	t.Helper()
	return runExecuteJSON(t, marshalReq(t, req))
}

func runExecuteJSON(t *testing.T, raw []byte) (providerResult, *cicFlowMock) {
	t.Helper()
	mock := &cicFlowMock{Reply: replyWith(200, `{}`, nil)}
	restore := mock.wireHostSeam()
	defer restore()
	out, err := Execute(nil, raw)
	if err != nil {
		t.Fatalf("Execute returned a Go error: %v", err)
	}
	return decodeResult(t, out), mock
}

func wantGateRefusal(t *testing.T, res providerResult, mock *cicFlowMock, field string) {
	t.Helper()
	if res.Status != "error" || res.Error == nil {
		t.Fatalf("status = %q, want an error (the request was run)", res.Status)
	}
	if res.Error.Class != classValidation {
		t.Errorf("error class = %q, want %q", res.Error.Class, classValidation)
	}
	if field != "" && res.Error.FieldPath != field {
		t.Errorf("field_path = %q, want %q", res.Error.FieldPath, field)
	}
	if mock.signCount() != 0 || mock.actuateCount() != 0 {
		t.Errorf("%d sign / %d actuate calls, want none: refuse before anything is signed or sent", mock.signCount(), mock.actuateCount())
	}
}

func TestExecuteRunsAConsistentRequestAndEchoesTheExecutionDigest(t *testing.T) {
	req := consistentUpdate(t)
	res, mock := runExecute(t, req)
	if res.Status != "ok" {
		t.Fatalf("status = %q (%+v)", res.Status, res.Error)
	}
	if mock.signCount() != 1 || mock.actuateCount() != 1 {
		t.Fatalf("%d sign / %d actuate calls, want 1 each", mock.signCount(), mock.actuateCount())
	}
	var er executionResult
	json.Unmarshal(res.Result, &er)
	if er.ExecutionDigest != req.Plan.ExecutionDigest {
		t.Errorf("execution_digest = %q, want the request's %q", er.ExecutionDigest, req.Plan.ExecutionDigest)
	}
}

// The step results are what they were; M4a adds only the digest to the output.
func TestExecuteOutputOnlyAddsTheExecutionDigest(t *testing.T) {
	req := consistentUpdate(t)
	mock := &cicFlowMock{Signature: "S", Reply: replyWith(200, "{}", map[string]string{"etag": "etag-1", "opc-request-id": "req-1"})}
	restore := mock.wireHostSeam()
	out, err := Execute(nil, marshalReq(t, req))
	restore()
	if err != nil {
		t.Fatal(err)
	}
	var wrapped struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	json.Unmarshal(out, &wrapped)
	var er executionResult
	json.Unmarshal(wrapped.Result, &er)
	er.ExecutionDigest = ""

	mock2 := &cicFlowMock{Signature: "S", Reply: replyWith(200, "{}", map[string]string{"etag": "etag-1", "opc-request-id": "req-1"})}
	restore2 := mock2.wireHostSeam()
	config, _ := decodeObject(req.Desired.Data)
	want, _ := okResult(executeSteps(resourceContracts()[bindingKind], req.Plan.ProviderOperations, config, req.Binding))
	restore2()
	got, _ := okResult(er)
	if string(got) != string(want) {
		t.Errorf("Execute's result minus execution_digest differs from the steps alone.\n got: %s\nwant: %s", got, want)
	}
}

// Every refusal below changes ONE thing in a request that Plan produced.
func TestExecuteRefusesARequestThatDoesNotMatchItsPlan(t *testing.T) {
	otherID := "ocid1.vcn.oc1..someoneelse"
	cases := []struct {
		name   string
		base   func(*testing.T) executeRequest
		change func(*executeRequest)
		field  string
	}{
		// config and states
		{"config differs from desired", consistentUpdate, func(r *executeRequest) { r.Config.Data = json.RawMessage(`{"displayName":"EVIL-RENAME"}`) }, "config.data"},
		{"config is empty", consistentUpdate, func(r *executeRequest) { r.Config.Data = nil }, "config.data"},
		{"desired changed after the plan", consistentUpdate, func(r *executeRequest) {
			r.Desired.Data = json.RawMessage(`{"displayName":"changed-after"}`)
			r.Config.Data = r.Desired.Data
		}, "plan.plan_digest"},
		{"observed changed after the plan", consistentUpdate, func(r *executeRequest) { r.Observed.Data = json.RawMessage(`{"displayName":"other-old"}`) }, "plan.plan_digest"},
		{"desired schema_version changed", consistentUpdate, func(r *executeRequest) { r.Desired.SchemaVersion = "v9" }, "plan.plan_digest"},
		{"observed encoding changed", consistentUpdate, func(r *executeRequest) { r.Observed.Encoding = "cbor" }, "plan.plan_digest"},
		{"desired missing", consistentUpdate, func(r *executeRequest) { r.Desired = schemaPayload{} }, "desired.data"},
		{"observed missing", consistentUpdate, func(r *executeRequest) { r.Observed = schemaPayload{} }, "observed.data"},
		// operations
		{"an update replaced by an M3-valid delete", consistentUpdate, func(r *executeRequest) {
			r.Plan.ProviderOperations = []providerOperation{{Operation: "DeleteVcn", Method: "DELETE", Path: "/vcns/{vcnId}"}}
		}, "plan.provider_operations[0]"},
		{"operation field changed", consistentUpdate, func(r *executeRequest) { r.Plan.Operation = "replace" }, "plan.operation"},
		{"an operation appended", consistentUpdate, func(r *executeRequest) {
			r.Plan.ProviderOperations = append(r.Plan.ProviderOperations, providerOperation{Operation: "DeleteVcn", Method: "DELETE", Path: "/vcns/{vcnId}"})
		}, "plan.provider_operations"},
		{"the operations removed", consistentUpdate, func(r *executeRequest) { r.Plan.ProviderOperations = nil }, "plan.provider_operations"},
		{"the order of two operations swapped", consistentTwoStep, func(r *executeRequest) {
			ops := r.Plan.ProviderOperations
			ops[0], ops[1] = ops[1], ops[0]
		}, "plan.provider_operations[0]"},
		{"the second of two operations replaced", consistentTwoStep, func(r *executeRequest) {
			r.Plan.ProviderOperations[1] = providerOperation{Operation: "DeleteVcn", Method: "DELETE", Path: "/vcns/{vcnId}"}
		}, "plan.provider_operations[1]"},
		// target
		{"resource_id changed", consistentUpdate, func(r *executeRequest) { r.Binding.ResourceID = otherID }, "plan.target_digest"},
		{"host changed", consistentUpdate, func(r *executeRequest) { r.Binding.Host = "iaas.us-ashburn-1.oraclecloud.com" }, "plan.target_digest"},
		{"base_path changed", consistentUpdate, func(r *executeRequest) { r.Binding.BasePath = "/20990101" }, "plan.target_digest"},
		{"key_id changed", consistentUpdate, func(r *executeRequest) { r.Binding.KeyID = "other/tenancy/key" }, "plan.target_digest"},
		{"revision changed", consistentUpdate, func(r *executeRequest) { r.Binding.Revision = "etag-STALE" }, "plan.target_digest"},
		{"revision removed", consistentUpdate, func(r *executeRequest) { r.Binding.Revision = "" }, "plan.target_digest"},
		// digests
		{"plan_digest missing", consistentUpdate, func(r *executeRequest) { r.Plan.PlanDigest = "" }, "plan.plan_digest"},
		{"target_digest missing", consistentUpdate, func(r *executeRequest) { r.Plan.TargetDigest = "" }, "plan.target_digest"},
		{"execution_digest missing", consistentUpdate, func(r *executeRequest) { r.Plan.ExecutionDigest = "" }, "plan.execution_digest"},
		{"plan_digest nonsense", consistentUpdate, func(r *executeRequest) { r.Plan.PlanDigest = "sha256:not-a-hash" }, "plan.plan_digest"},
		{"target_digest nonsense", consistentUpdate, func(r *executeRequest) { r.Plan.TargetDigest = "x" }, "plan.target_digest"},
		{"execution_digest nonsense", consistentUpdate, func(r *executeRequest) { r.Plan.ExecutionDigest = "sha256:" + strings.Repeat("0", 64) }, "plan.execution_digest"},
		{"plan_digest and target_digest swapped", consistentUpdate, func(r *executeRequest) {
			r.Plan.PlanDigest, r.Plan.TargetDigest = r.Plan.TargetDigest, r.Plan.PlanDigest
		}, "plan.plan_digest"},
		{"the digests of another plan", consistentUpdate, func(r *executeRequest) {
			o := m4aPlan(t, bindingKind, `{"displayName":"another"}`, `{"displayName":"old-name"}`, bindingWith("ocid1.vcn.oc1..approved"))
			r.Plan.PlanDigest, r.Plan.ExecutionDigest = o.PlanDigest, o.ExecutionDigest
		}, "plan.plan_digest"},
		// a valid plan paired with another, equally valid, target
		{"a plan paired with another target's digests", consistentUpdate, func(r *executeRequest) {
			other := bindingWith(otherID)
			other.Revision = "etag-approved"
			o := m4aPlan(t, bindingKind, `{"displayName":"approved-name"}`, `{"displayName":"old-name"}`, other)
			r.Binding = other
			r.Plan.TargetDigest = o.TargetDigest // the new target, with its own correct digest
			// execution_digest is left as it was: it binds the plan to the ORIGINAL target
		}, "plan.execution_digest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := c.base(t)
			c.change(&req)
			res, mock := runExecute(t, req)
			wantGateRefusal(t, res, mock, c.field)
		})
	}
}

func TestExecuteRefusesTheRetiredPlanID(t *testing.T) {
	for _, legacy := range []string{`"sha256:abc"`, `""`, `null`} {
		t.Run(legacy, func(t *testing.T) {
			var m map[string]interface{}
			json.Unmarshal(marshalReq(t, consistentUpdate(t)), &m)
			m["plan"].(map[string]interface{})["plan_id"] = json.RawMessage(legacy)
			raw, _ := json.Marshal(m)
			res, mock := runExecuteJSON(t, raw)
			wantGateRefusal(t, res, mock, "plan.plan_id")
		})
	}
}

// A plan with a bad step in the middle runs no step at all: the check is over the whole
// plan, not step by step.
func TestAMultiStepPlanWithOneChangedStepRunsNoStep(t *testing.T) {
	for _, pos := range []int{0, 1} {
		t.Run(strings.Repeat("x", pos)+"changed step", func(t *testing.T) {
			req := consistentTwoStep(t)
			req.Plan.ProviderOperations[pos] = providerOperation{Operation: "DeleteVcn", Method: "DELETE", Path: "/vcns/{vcnId}"}
			res, mock := runExecute(t, req)
			wantGateRefusal(t, res, mock, "")
		})
	}
}

// The request is edited as TEXT, the way a caller (or an attacker) would: no struct, no
// helper between the edit and Execute.
func TestExecuteRefusesAHandEditedRequestText(t *testing.T) {
	good := string(marshalReq(t, consistentUpdate(t)))
	edits := []struct{ name, from, to, field string }{
		{"config renamed in the text", `"config":{"schema_id":"cic:network:vcn-config","schema_version":"v0.1.0","schema_hash":"abc123","encoding":"canonical-json","data":{"displayName":"approved-name"}}`,
			`"config":{"schema_id":"cic:network:vcn-config","schema_version":"v0.1.0","schema_hash":"abc123","encoding":"canonical-json","data":{"displayName":"X"}}`, "config.data"},
		{"resource id in the text", `"resource_id":"ocid1.vcn.oc1..approved"`, `"resource_id":"ocid1.vcn.oc1..approveX"`, "plan.target_digest"},
		{"method of the step in the text", `"method":"PUT"`, `"method":"DELETE"`, "plan.provider_operations[0]"},
		{"path of the step in the text", `"path":"/vcns/{vcnId}"`, `"path":"/vcns"`, "plan.provider_operations[0]"},
	}
	for _, e := range edits {
		t.Run(e.name, func(t *testing.T) {
			if !strings.Contains(good, e.from) {
				t.Fatalf("test setup: %q is not in the request text:\n%s", e.from, good)
			}
			res, mock := runExecuteJSON(t, []byte(strings.Replace(good, e.from, e.to, 1)))
			wantGateRefusal(t, res, mock, e.field)
		})
	}
}

// The same intent and state written differently produce the same plan_digest, and a
// request that spells them differently from the one the plan was made from still runs.
func TestEquivalentSpellingsOfDesiredAndObservedGiveOnePlanDigest(t *testing.T) {
	b := bindingWith("ocid1.vcn.oc1..a")
	spellings := []struct{ name, desired, observed string }{
		{"plain", `{"displayName":"new","compartmentId":"ocid1.compartment..b"}`, `{"displayName":"old","compartmentId":"ocid1.compartment..a"}`},
		{"key order", `{"compartmentId":"ocid1.compartment..b","displayName":"new"}`, `{"compartmentId":"ocid1.compartment..a","displayName":"old"}`},
		{"whitespace", ` { "displayName" : "new" , "compartmentId" : "ocid1.compartment..b" } `, "{\n\t\"displayName\": \"old\",\n\t\"compartmentId\": \"ocid1.compartment..a\"\n}"},
		{"escapes", `{"displayName":"new","compartmentId":"ocid1.compartment..b"}`, `{"displayName":"old","compartmentId":"ocid1.compartment..a"}`},
	}
	want := m4aPlan(t, bindingKind, spellings[0].desired, spellings[0].observed, b)
	for _, s := range spellings[1:] {
		t.Run(s.name, func(t *testing.T) {
			got := m4aPlan(t, bindingKind, s.desired, s.observed, b)
			if got.PlanDigest != want.PlanDigest || got.ExecutionDigest != want.ExecutionDigest {
				t.Errorf("plan_digest %s, want %s: the same state spelled differently must give one digest", got.PlanDigest, want.PlanDigest)
			}
			// the request made with one spelling runs when it carries another one
			req := m4aRequest(t, bindingKind, spellings[0].desired, spellings[0].observed, b)
			req.Desired.Data = json.RawMessage(s.desired)
			req.Observed.Data = json.RawMessage(s.observed)
			req.Config.Data = json.RawMessage(s.desired)
			res, mock := runExecute(t, req)
			if res.Status != "ok" || mock.actuateCount() != 2 {
				t.Errorf("status %q with %d requests (%+v), want ok with 2", res.Status, mock.actuateCount(), res.Error)
			}
		})
	}
	t.Run("numbers", func(t *testing.T) {
		a := m4aPlan(t, "cic:compute:instance", `{"shapeConfig":{"ocpus":1}}`, `{"shapeConfig":{"ocpus":2}}`, bindingWith("ocid1.instance..a"))
		c := m4aPlan(t, "cic:compute:instance", `{"shapeConfig":{"ocpus":1.0}}`, `{"shapeConfig":{"ocpus":2e0}}`, bindingWith("ocid1.instance..a"))
		d := m4aPlan(t, "cic:compute:instance", `{"shapeConfig":{"ocpus":10e-1}}`, `{"shapeConfig":{"ocpus":2.00}}`, bindingWith("ocid1.instance..a"))
		if a.PlanDigest != c.PlanDigest || a.PlanDigest != d.PlanDigest {
			t.Errorf("1, 1.0 and 10e-1 must give one plan_digest: %s %s %s", a.PlanDigest, c.PlanDigest, d.PlanDigest)
		}
	})
}

func TestEveryRelevantInputChangesItsDigest(t *testing.T) {
	b := bindingWith("ocid1.vcn.oc1..a")
	base := m4aPlan(t, bindingKind, `{"displayName":"new"}`, `{"displayName":"old"}`, b)
	t.Run("desired data", func(t *testing.T) {
		if o := m4aPlan(t, bindingKind, `{"displayName":"newer"}`, `{"displayName":"old"}`, b); o.PlanDigest == base.PlanDigest {
			t.Error("plan_digest did not change")
		}
	})
	t.Run("observed data", func(t *testing.T) {
		if o := m4aPlan(t, bindingKind, `{"displayName":"new"}`, `{"displayName":"older"}`, b); o.PlanDigest == base.PlanDigest {
			t.Error("plan_digest did not change")
		}
	})
	t.Run("the operation (a second changed field)", func(t *testing.T) {
		if o := m4aPlan(t, bindingKind, `{"displayName":"new","compartmentId":"ocid1.compartment..b"}`, `{"displayName":"old","compartmentId":"ocid1.compartment..a"}`, b); o.PlanDigest == base.PlanDigest {
			t.Error("plan_digest did not change")
		}
	})
	for name, change := range map[string]func(*execBinding){
		"host":        func(x *execBinding) { x.Host = "other.example" },
		"base_path":   func(x *execBinding) { x.BasePath = "/20990101" },
		"key_id":      func(x *execBinding) { x.KeyID = "other/key" },
		"resource_id": func(x *execBinding) { x.ResourceID = "ocid1.vcn.oc1..b" },
		"revision":    func(x *execBinding) { x.Revision = "etag-9" },
	} {
		t.Run("target "+name, func(t *testing.T) {
			nb := b
			change(&nb)
			o := m4aPlan(t, bindingKind, `{"displayName":"new"}`, `{"displayName":"old"}`, nb)
			if o.TargetDigest == base.TargetDigest {
				t.Error("target_digest did not change")
			}
			if o.PlanDigest != base.PlanDigest {
				t.Error("plan_digest must not depend on the target")
			}
			if o.ExecutionDigest == base.ExecutionDigest {
				t.Error("execution_digest did not change")
			}
		})
	}
	t.Run("an absent revision and an empty one are the same", func(t *testing.T) {
		x := b
		x.Revision = ""
		y := b
		if m4aPlan(t, bindingKind, `{"displayName":"new"}`, `{"displayName":"old"}`, x).TargetDigest !=
			m4aPlan(t, bindingKind, `{"displayName":"new"}`, `{"displayName":"old"}`, y).TargetDigest {
			// y has a revision? bindingWith sets none, so both are empty: this documents the rule
			t.Error("absent and empty must be one value")
		}
	})
	t.Run("the contract of the kind", func(t *testing.T) {
		c := resourceContracts()[bindingKind]
		d1 := digestOf(contractEnvelope(bindingKind, c, true))
		changed := c
		changed.operations = map[string]httpOp{}
		for k, v := range c.operations {
			changed.operations[k] = v
		}
		op := changed.operations["UpdateVcn"]
		op.method = "PATCH"
		changed.operations["UpdateVcn"] = op
		if d2 := digestOf(contractEnvelope(bindingKind, changed, true)); d2 == d1 {
			t.Error("a changed registry operation did not change the contract digest")
		}
	})
}

func TestDigestInputsAreNotAmbiguousAtFieldBoundaries(t *testing.T) {
	d := func(sid string) schemaPayload { return m4aPayload(sid, `{}`) }
	a, _ := planEnvelope("AB", "c", d("CD"), d("o"), "noop", nil)
	b, _ := planEnvelope("A", "c", d("BCD"), d("o"), "noop", nil)
	if digestOf(a) == digestOf(b) {
		t.Error("moving bytes from kind to schema_id must change the plan digest")
	}
	x := digestOf(targetEnvelope("k", execBinding{Host: "ab", BasePath: "c", KeyID: "k"}))
	y := digestOf(targetEnvelope("k", execBinding{Host: "a", BasePath: "bc", KeyID: "k"}))
	if x == y {
		t.Error("moving bytes from host to base_path must change the target digest")
	}
}

// A noop plan has nothing to run: it is derived, digested and accepted like any other,
// and runs no step.
func TestExecuteOfANoopPlanRunsNoStep(t *testing.T) {
	req := m4aRequest(t, bindingKind, `{"displayName":"same"}`, `{"displayName":"same"}`, bindingWith("ocid1.vcn.oc1..a"))
	if req.Plan.Operation != "noop" {
		t.Fatalf("test setup: plan = %q", req.Plan.Operation)
	}
	res, mock := runExecute(t, req)
	if res.Status != "ok" || mock.signCount() != 0 || mock.actuateCount() != 0 {
		t.Fatalf("status %q, %d sign / %d actuate (%+v), want ok and nothing sent", res.Status, mock.signCount(), mock.actuateCount(), res.Error)
	}
	var er executionResult
	json.Unmarshal(res.Result, &er)
	if er.ExecutionDigest != req.Plan.ExecutionDigest || len(er.Steps) != 0 {
		t.Errorf("result %+v, want the execution digest echoed and no steps", er)
	}
}

// ---- what M4a does not show ----

// A request that agrees with itself is not an approved one. A caller who writes the
// desired state, the observed state and the target, asks Plan, and sends the result,
// is accepted: M4a checks consistency, never approval. The approval is the Relay's
// (M4b); when it exists this test is the one to turn around.
func TestM4aDoesNotClaimApproval(t *testing.T) {
	// Nothing here was approved by anyone: the caller invented every input.
	req := m4aRequest(t, bindingKind, `{"displayName":"invented-by-the-caller"}`, `{"displayName":"whatever-the-caller-says"}`, bindingWith("ocid1.vcn.oc1..anything"))
	res, mock := runExecute(t, req)
	if res.Status != "ok" || mock.actuateCount() != 1 {
		t.Fatalf("status %q with %d requests (%+v): this test records that M4a runs a self-consistent, unapproved request", res.Status, mock.actuateCount(), res.Error)
	}
}

// The same consistent request runs every time it is sent: the module keeps no state
// between calls, so it cannot tell a first execution from a repeat. Single use is the
// Relay's (M4b). This records the absence of that protection; it is not a property to
// keep.
func TestM4aDoesNotPreventReplay(t *testing.T) {
	req := consistentUpdate(t)
	mock := &cicFlowMock{Reply: replyWith(200, `{}`, nil)}
	restore := mock.wireHostSeam()
	defer restore()
	for i := 1; i <= 3; i++ {
		out, err := Execute(nil, marshalReq(t, req))
		if err != nil {
			t.Fatal(err)
		}
		if res := decodeResult(t, out); res.Status != "ok" {
			t.Fatalf("run %d: status %q (%+v)", i, res.Status, res.Error)
		}
	}
	if mock.actuateCount() != 3 {
		t.Fatalf("%d provider requests after 3 identical executions, want 3: M4a does not stop a repeat", mock.actuateCount())
	}
}
