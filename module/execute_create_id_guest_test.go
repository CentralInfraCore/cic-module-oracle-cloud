//go:build !wasip1

package main

// execute_create_id_guest_test.go drives the created-OCID path over the REAL
// guest ABI.
//
// NOTE ON THE FILE NAME — do not rename this to *_wasm_test.go. Go reads a
// trailing _<GOARCH> in a file name as an implicit build constraint, and `wasm`
// IS a GOARCH: such a file is silently dropped from the host test binary
// (`go list -f '{{.IgnoredGoFiles}}'` lists it) and every test in it quietly
// stops existing. These tests are host-side tests that LOAD a wasm guest; they
// are not compiled FOR wasm.
//
// What it does:
// A wazero-instantiated binary, the exported Call function, the
// //go:wasmimport cic-flow surface. The host-seam tests in
// execute_create_id_test.go call Execute as a Go function; this one proves the
// same behaviour survives compilation to wasip1 and the JSON envelope crossing
// linear memory — which is how the relay will actually see it.
//
// IMPORTANT, and the reason for CIC_MODULE_WASM_PATH: module/module.wasm is a
// committed, signed artifact pinned by project.yaml's metadata.buildHash. It
// does NOT contain the change this test checks, and rebuilding it is the owner's
// step, not a code change's. So with the variable unset this test SKIPS rather
// than passing vacuously against a stale binary — a pass here means a binary
// built from these sources was really exercised. Build one with:
//
//	tinygo build -o <scratch>/module.wasm -target wasip1 -scheduler=none .
//	CIC_MODULE_WASM_PATH=<scratch>/module.wasm go test -run TestHostLoadExecuteCreate ./...

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// requireFreshGuest skips unless the test was pointed at an explicitly built
// guest binary. Loading the committed module.wasm here would assert nothing
// about the current sources, so it is refused rather than reported as a pass.
func requireFreshGuest(t *testing.T) {
	t.Helper()
	if os.Getenv(wasmPathEnv) == "" {
		t.Skipf("set %s to a binary built from the current sources — the committed module.wasm predates this change and cannot evidence it", wasmPathEnv)
	}
}

// execStepThroughWasm runs one provider operation through the guest's Call
// export and returns the decoded execution result.
func execStepThroughWasm(t *testing.T, mock *cicFlowMock, po providerOperation, binding execBinding, configJSON string) (string, executionResult) {
	t.Helper()
	ctx, instance, callFn, allocateFn, deallocateFn := loadGuestWithCicFlow(t, mock)

	reqJSON, _ := json.Marshal(executeRequest{
		Kind:    "cic:network:vcn",
		Plan:    executionPlan{Operation: "create", ProviderOperations: []providerOperation{po}},
		Config:  schemaPayload{SchemaID: "cic:network:vcn-config", Encoding: encCanonicalJSON, Data: json.RawMessage(configJSON)},
		Binding: binding,
	})
	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "execute", "{}", string(reqJSON))
	if string(env.Error) != "null" {
		t.Fatalf("execute transport error = %s, want null", env.Error)
	}
	var res struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("data is not a providerResult: %v (raw: %s)", err, env.Data)
	}
	if res.Status != "ok" {
		t.Fatalf("providerResult status = %q, want ok (raw: %s)", res.Status, env.Data)
	}
	var er executionResult
	if err := json.Unmarshal(res.Result, &er); err != nil {
		t.Fatalf("executionResult decode: %v (raw: %s)", err, res.Result)
	}
	return string(res.Result), er
}

// TestHostLoadExecuteCreateCarriesOCID: a create run through the real Call
// export reports the OCID from the provider's answer, and the raw JSON that
// crossed the ABI really contains the resource_id key — not just a Go field that
// happened to decode.
func TestHostLoadExecuteCreateCarriesOCID(t *testing.T) {
	requireFreshGuest(t)

	const want = "ocid1.vcn.oc1.eu-frankfurt-1.amaaaaaacreatedbywasm"
	mock := &cicFlowMock{
		Reply: func(n int, call cicFlowCall) cicFlowReply {
			return cicFlowReply{
				Status:  200,
				Headers: map[string]string{"etag": "etag-w", "opc-request-id": "req-w"},
				Body:    `{"id":"` + want + `","displayName":"prod-vcn","lifecycleState":"PROVISIONING"}`,
			}
		},
	}
	raw, er := execStepThroughWasm(t, mock,
		providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns"},
		execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", BasePath: "/20160918", KeyID: "tenancy/user/fp"},
		`{"compartmentId":"ocid1.compartment.oc1..c","displayName":"prod-vcn"}`)

	if er.Status != "succeeded" || len(er.Steps) != 1 {
		t.Fatalf("execute = %+v, want succeeded with 1 step (raw: %s)", er, raw)
	}
	if er.Steps[0].ResourceID != want {
		t.Errorf("steps[0].resource_id = %q, want %q", er.Steps[0].ResourceID, want)
	}
	// The key is really on the wire, under its contract name.
	if !strings.Contains(raw, `"resource_id":"`+want+`"`) {
		t.Errorf("the JSON that crossed the ABI has no resource_id field:\n%s", raw)
	}
	// The guest signed and sent the create to the collection path — no id to template.
	// The signed request-target is the FULL path actually sent, base_path
	// included (opPath prepends binding.base_path) — not the registry path.
	if len(mock.Signed) != 1 || !strings.Contains(mock.Signed[0], "(request-target): post /20160918/vcns") {
		t.Errorf("signed string is not a POST to /20160918/vcns:\n%v", mock.Signed)
	}
	if url := mock.lastActuate(t).URL; url != "https://iaas.eu-frankfurt-1.oraclecloud.com/20160918/vcns" {
		t.Errorf("actuate URL = %q, want the base_path-prefixed collection path", url)
	}
}

// TestHostLoadExecuteCreateWithoutIDFails is the negative half over the real
// ABI: a 2xx create with nothing to read fails, says the resource was created,
// carries no resource_id key at all, and makes exactly one provider request.
func TestHostLoadExecuteCreateWithoutIDFails(t *testing.T) {
	requireFreshGuest(t)

	mock := &cicFlowMock{
		Reply: func(n int, call cicFlowCall) cicFlowReply {
			return cicFlowReply{Status: 200, Headers: map[string]string{"opc-request-id": "req-w2"}, Body: `{}`}
		},
	}
	raw, er := execStepThroughWasm(t, mock,
		providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns"},
		execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", BasePath: "/20160918", KeyID: "tenancy/user/fp"},
		`{"compartmentId":"ocid1.compartment.oc1..c"}`)

	if er.Status != "failed" || len(er.Steps) != 1 {
		t.Fatalf("execute = %+v, want failed with 1 step (raw: %s)", er, raw)
	}
	s := er.Steps[0]
	if s.ErrorClass != classProvider {
		t.Errorf("steps[0].error_class = %q, want %q", s.ErrorClass, classProvider)
	}
	if !strings.Contains(s.Error, "do NOT retry") || !strings.Contains(s.Error, "created") {
		t.Errorf("steps[0].error = %q, want the duplicate-create warning", s.Error)
	}
	if strings.Contains(raw, "resource_id") {
		t.Errorf("a failed create put a resource_id on the wire:\n%s", raw)
	}
	if n := mock.actuateCount(); n != 1 {
		t.Errorf("the guest made %d provider requests, want exactly 1 — no retry", n)
	}
}

// TestHostLoadExecuteUpdateHasNoResourceID is the role check over the real ABI:
// an update whose answer carries an "id" still emits no resource_id, so the
// field cannot leak into the shape every existing consumer already reads.
func TestHostLoadExecuteUpdateHasNoResourceID(t *testing.T) {
	requireFreshGuest(t)

	mock := &cicFlowMock{
		Reply: func(n int, call cicFlowCall) cicFlowReply {
			return cicFlowReply{
				Status:  200,
				Headers: map[string]string{"etag": "etag-u"},
				Body:    `{"id":"ocid1.vcn.oc1..updated","displayName":"prod-vcn"}`,
			}
		},
	}
	raw, er := execStepThroughWasm(t, mock,
		providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
		execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", BasePath: "/20160918", KeyID: "tenancy/user/fp", ResourceID: "ocid1.vcn.oc1..xyz"},
		`{"displayName":"prod-vcn"}`)

	if er.Status != "succeeded" || len(er.Steps) != 1 {
		t.Fatalf("execute = %+v, want succeeded with 1 step (raw: %s)", er, raw)
	}
	if er.Steps[0].ResourceID != "" {
		t.Errorf("steps[0].resource_id = %q, want empty for an update", er.Steps[0].ResourceID)
	}
	if strings.Contains(raw, "resource_id") {
		t.Errorf("an update emitted a resource_id key:\n%s", raw)
	}
}
