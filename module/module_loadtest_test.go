//go:build !wasip1

package main

// module_loadtest_test.go is a host-load smoke test, mirroring the relay
// cabinet ABI checks (CIC-Relay/core/cabinet/cicwasm.go):
//   - wazero + wasi_snapshot_preview1 instantiation, WithStartFunctions()
//     (cicwasm.go:66, :178) — these are libraries, not applications.
//   - the host requires three exported functions: Call, allocate, deallocate
//     (cicwasm.go:243-247).
//   - result packing is (size << 32) | pointer, payload is {data,error}
//     (cicwasm.go:325, :346).
//
// It does not import the relay's internal cabinet package — a template
// repository should not depend on CIC-Relay as a Go module. Instead it
// re-implements the minimal host-load contract directly against wazero,
// the same runtime cicwasm.go uses.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// wasmPath is where `make wasm.build` (mk/wasm.mk) emits the TinyGo artifact.
const wasmPath = "module.wasm"

// wasmPathEnv names an alternative guest binary to load.
//
// Why it exists: module/module.wasm is a COMMITTED, signed artifact bound to
// project.yaml's metadata.buildHash. A change to the guest sources is therefore
// invisible to these tests until the owner rebuilds and re-signs it — which is
// not something a code change may do on its own. With this variable a reviewer
// can point the host-load tests at a freshly built, throwaway binary
// (`tinygo build -o <scratch>/module.wasm -target wasip1 -scheduler=none .`)
// and see the new code actually execute over the real Call export.
//
// Unset — the CI and developer default — means module.wasm, so every existing
// test keeps loading exactly the artifact it always did.
const wasmPathEnv = "CIC_MODULE_WASM_PATH"

// wasmBinaryPath is the guest binary the host-load tests load.
func wasmBinaryPath() string {
	if p := os.Getenv(wasmPathEnv); p != "" {
		return p
	}
	return wasmPath
}

// guestBinaryBytes reads the guest binary the host-load tests execute and says
// what to do when it cannot be read.
//
//   - CIC_MODULE_WASM_PATH set: the caller asked for THAT binary, so any failure
//     to read it is an error and the test must FAIL. Skipping would turn a
//     mistyped or missing path into a green run that exercised nothing.
//   - unset: the committed module.wasm. "Not built yet" (the file does not
//     exist) is the one case that skips; any other read error is still an error.
//
// skip is non-empty only for that one case; skip and err are never both set.
func guestBinaryBytes() (wasm []byte, skip string, err error) {
	path := wasmBinaryPath()
	b, rerr := os.ReadFile(path)
	if rerr == nil {
		return b, "", nil
	}
	if os.Getenv(wasmPathEnv) == "" && os.IsNotExist(rerr) {
		return nil, "guest binary not built — run `make wasm.build` first (path: " + path + ")", nil
	}
	return nil, "", fmt.Errorf("cannot read the guest binary %s (%s is %q): %w", path, wasmPathEnv, os.Getenv(wasmPathEnv), rerr)
}

// envelope mirrors the host's GuestResult (cicwasm.go:346): {data, error}.
type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error json.RawMessage `json:"error"`
}

// envelopeError mirrors the error-codes contract (KB c689).
type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// loadModule compiles and instantiates module.wasm, returning a handle with
// the three host-required ABI exports (cicwasm.go:243-247). Skips the test
// if module.wasm has not been built yet.
func loadModule(t *testing.T) (context.Context, api.Module, api.Function, api.Function, api.Function) {
	t.Helper()

	wasmBytes, skip, err := guestBinaryBytes()
	if skip != "" {
		t.Skip(skip)
	}
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { runtime.Close(ctx) })

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatalf("failed to instantiate wasi: %v", err)
	}

	// The guest imports the relay's cic-flow host module (sign/actuate). Register
	// a stub so instantiation succeeds; tests that do not exercise execute never
	// call it. TestHostLoadExecute builds its own runtime with a capturing mock.
	stub := func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		resp := []byte(`{"error":"cic-flow stub: not wired in this test"}`)
		if uint32(len(resp)) > outLen || !mod.Memory().Write(outPtr, resp) {
			return -1
		}
		return int32(len(resp))
	}
	if _, err := runtime.NewHostModuleBuilder("cic-flow").
		NewFunctionBuilder().WithFunc(stub).WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").Export("sign").
		NewFunctionBuilder().WithFunc(stub).WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").Export("actuate").
		Instantiate(ctx); err != nil {
		t.Fatalf("failed to instantiate cic-flow stub: %v", err)
	}

	compiled, err := runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("failed to compile module: %v", err)
	}

	// Don't call _start — guest modules are libraries, not applications
	// (cicwasm.go:177-178).
	moduleConfig := wazero.NewModuleConfig().WithName("module_loadtest").WithStartFunctions()
	instance, err := runtime.InstantiateModule(ctx, compiled, moduleConfig)
	if err != nil {
		t.Fatalf("failed to instantiate module: %v", err)
	}
	t.Cleanup(func() { instance.Close(ctx) })

	callFn := instance.ExportedFunction("Call")
	allocateFn := instance.ExportedFunction("allocate")
	deallocateFn := instance.ExportedFunction("deallocate")
	if callFn == nil || allocateFn == nil || deallocateFn == nil {
		t.Fatalf("module does not export required ABI functions (Call/allocate/deallocate) — cicwasm.go:243-247")
	}

	return ctx, instance, callFn, allocateFn, deallocateFn
}

// callOp performs one Call(op, auth, data) round trip and returns the
// decoded {data, error} envelope.
func callOp(t *testing.T, ctx context.Context, instance api.Module, callFn, allocateFn, deallocateFn api.Function, op, auth, data string) envelope {
	t.Helper()

	opPtr, opLen := writeString(t, ctx, instance, allocateFn, op)
	defer deallocateFn.Call(ctx, uint64(opPtr), uint64(opLen))
	authPtr, authLen := writeString(t, ctx, instance, allocateFn, auth)
	defer deallocateFn.Call(ctx, uint64(authPtr), uint64(authLen))
	dataPtr, dataLen := writeString(t, ctx, instance, allocateFn, data)
	defer deallocateFn.Call(ctx, uint64(dataPtr), uint64(dataLen))

	results, err := callFn.Call(ctx, uint64(opPtr), uint64(opLen), uint64(authPtr), uint64(authLen), uint64(dataPtr), uint64(dataLen))
	if err != nil {
		t.Fatalf("Call(%q) failed: %v", op, err)
	}

	packed := results[0]
	if packed == 0 {
		// host treats packed 0 as null/empty (cicwasm.go:337-339) — not
		// expected from marshalData/marshalErr, which always emit a
		// non-empty {"data":...,"error":...} envelope.
		t.Fatalf("Call(%q) returned packed 0 (null/empty)", op)
	}

	resultLen := uint32(packed >> 32)
	resultPtr := uint32(packed)
	defer deallocateFn.Call(ctx, uint64(resultPtr), uint64(resultLen))

	resultBytes, ok := instance.Memory().Read(resultPtr, resultLen)
	if !ok {
		t.Fatalf("Call(%q): failed to read result from guest memory at ptr=%d, len=%d", op, resultPtr, resultLen)
	}

	var env envelope
	if err := json.Unmarshal(resultBytes, &env); err != nil {
		t.Fatalf("Call(%q): failed to unmarshal {data,error} envelope: %v (raw: %s)", op, err, resultBytes)
	}
	return env
}

// TestHostLoad verifies the ABI exports and the "describe" round trip: the
// transport error must be null and data must be an "ok" providerResult carrying
// the module manifest (provider.go: Describe).
func TestHostLoad(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "describe", "{}", "{}")

	if string(env.Error) != "null" {
		t.Fatalf("Call(\"describe\"): error = %s, want null", env.Error)
	}

	var res struct {
		Status string `json:"status"`
		Result struct {
			ABIVersion string   `json:"abi_version"`
			Operations []string `json:"operations"`
		} `json:"result"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("Call(\"describe\"): data is not the expected shape: %v (raw: %s)", err, env.Data)
	}
	if res.Status != "ok" {
		t.Errorf("Call(\"describe\"): data.status = %q, want %q", res.Status, "ok")
	}
	if res.Result.ABIVersion != "cic:provider@0.1.0" {
		t.Errorf("Call(\"describe\"): abi_version = %q, want cic:provider@0.1.0", res.Result.ABIVersion)
	}
	if len(res.Result.Operations) != 8 {
		t.Errorf("Call(\"describe\"): got %d operations, want 8", len(res.Result.Operations))
	}
}

// TestHostLoadUnknownOp verifies that an unknown op produces a CodeInput
// error envelope (abi.go: Call default case).
func TestHostLoadUnknownOp(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "bogus-op", "{}", "{}")

	if string(env.Data) != "null" {
		t.Errorf("Call(\"bogus-op\"): data = %s, want null", env.Data)
	}
	var gerr envelopeError
	if err := json.Unmarshal(env.Error, &gerr); err != nil {
		t.Fatalf("Call(\"bogus-op\"): error is not a valid error envelope: %v (raw: %s)", err, env.Error)
	}
	if gerr.Code != "INPUT" {
		t.Errorf("Call(\"bogus-op\"): error.code = %q, want %q", gerr.Code, "INPUT")
	}
}

// TestHostLoadDomainError verifies the domain-error path through wasm: a sign+send
// op called without a binding (poll, "{}") returns a SUCCESSFUL transport call
// (error null) whose data is an "error" providerResult of class validation.
// Domain errors live in data, not the transport error slot.
func TestHostLoadDomainError(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "poll", "{}", "{}")

	if string(env.Error) != "null" {
		t.Fatalf("Call(\"poll\"): transport error = %s, want null", env.Error)
	}
	var res struct {
		Status string `json:"status"`
		Error  struct {
			Class string `json:"class"`
		} `json:"error"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("Call(\"poll\"): data is not a providerResult: %v (raw: %s)", err, env.Data)
	}
	if res.Status != "error" || res.Error.Class != "validation" {
		t.Errorf("Call(\"poll\", no binding): got status=%q class=%q, want error/validation", res.Status, res.Error.Class)
	}
}

// TestHostLoadProviderOps smoke-tests the whole cic:provider op set through
// wasm: every op returns a null transport error and a data payload that decodes
// as a providerResult with status ok|error (provider.go).
func TestHostLoadProviderOps(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	for _, op := range []string{
		"describe", "validate", "observe", "plan",
		"execute", "poll", "invoke", "destroy",
	} {
		t.Run(op, func(t *testing.T) {
			env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, op, "{}", "{}")
			if string(env.Error) != "null" {
				t.Errorf("Call(%q): transport error = %s, want null", op, env.Error)
			}
			var res struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(env.Data, &res); err != nil {
				t.Fatalf("Call(%q): data is not a providerResult: %v (raw: %s)", op, err, env.Data)
			}
			if res.Status != "ok" && res.Status != "error" {
				t.Errorf("Call(%q): data.status = %q, want ok|error", op, res.Status)
			}
		})
	}
}

// TestHostLoadValidateConformance proves the embedded generated schema
// (schemas/vcn.json) is parsed and enforced inside the real wasm runtime — the
// critical check, since the host instantiates the guest without running _start,
// so contract parsing must be lazy (contracts.go), not init().
func TestHostLoadValidateConformance(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	admissible := func(intentData string) (bool, string) {
		req := `{"kind":"cic:network:vcn","intent":{"schema_id":"cic:network:vcn-config","schema_version":"v0.1.0","schema_hash":"abc123","encoding":"canonical-json","data":` + intentData + `}}`
		env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "validate", "{}", req)
		if string(env.Error) != "null" {
			t.Fatalf("validate transport error = %s, want null", env.Error)
		}
		var res struct {
			Result struct {
				Admissible bool     `json:"admissible"`
				Checked    []string `json:"checked"`
			} `json:"result"`
		}
		if err := json.Unmarshal(env.Data, &res); err != nil {
			t.Fatalf("decode validationResult: %v (raw: %s)", err, env.Data)
		}
		checked := ""
		if len(res.Result.Checked) > 0 {
			checked = res.Result.Checked[len(res.Result.Checked)-1]
		}
		return res.Result.Admissible, checked
	}

	// A conformant intent is admissible AND the module reports it ran
	// schema-conformance (proving the embedded contract was found in wasm).
	if ok, checked := admissible(`{"compartmentId":"ocid1..","displayName":"prod"}`); !ok || checked != "schema-conformance" {
		t.Errorf("conformant intent: admissible=%v checked=%q, want true/schema-conformance", ok, checked)
	}
	// A provider-computed field in the intent is rejected by the embedded contract.
	if ok, _ := admissible(`{"compartmentId":"ocid1..","lifecycleState":"AVAILABLE"}`); ok {
		t.Errorf("intent with output-only field: admissible=true, want false")
	}
}

// TestHostLoadExecute drives `execute` end-to-end through wasm against a mock
// `cic-flow` host module that matches the relay's real ABI (R1/R2, CIC_Relay#91):
// (req_ptr,req_len,out_ptr,out_len)->i32, JSON in linear memory. It proves the
// guest builds the OCI signing material, calls sign, assembles the Authorization
// header, calls actuate, and returns the execution-result — the whole sign+send
// path, exercised over the real wasmimport surface.
func TestHostLoadExecute(t *testing.T) {
	// The generalized cic-flow mock (cicflow_mock_test.go), configured exactly as
	// the mock inlined here used to be: 200, etag-1/req-1, an empty JSON body.
	mock := &cicFlowMock{
		Reply: func(n int, call cicFlowCall) cicFlowReply {
			return cicFlowReply{
				Status:  200,
				Headers: map[string]string{"etag": "etag-1", "opc-request-id": "req-1"},
				Body:    "{}",
			}
		},
	}
	ctx, instance, callFn, allocateFn, deallocateFn := loadGuestWithCicFlow(t, mock)

	// An approved plan (as `plan` would emit) + desired config + binding.
	exReq := executeRequest{
		Kind: "cic:network:vcn",
		Plan: executionPlan{
			Operation:          "update",
			ProviderOperations: []providerOperation{{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"}},
		},
		Config:  schemaPayload{SchemaID: "cic:network:vcn-config", Encoding: encCanonicalJSON, Data: json.RawMessage(`{"displayName":"prod-vcn"}`)},
		Binding: execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", KeyID: "tenancy/user/fp", ResourceID: "ocid1.vcn.oc1..xyz"},
	}
	reqJSON, _ := json.Marshal(exReq)
	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "execute", "{}", string(reqJSON))

	if string(env.Error) != "null" {
		t.Fatalf("execute transport error = %s, want null", env.Error)
	}
	var res struct {
		Status string `json:"status"`
		Result struct {
			Status string `json:"status"`
			Steps  []struct {
				Operation  string `json:"operation"`
				HTTPStatus int    `json:"http_status"`
				Etag       string `json:"etag"`
				Error      string `json:"error"`
			} `json:"steps"`
		} `json:"result"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("decode: %v (raw: %s)", err, env.Data)
	}
	if res.Status != "ok" || res.Result.Status != "succeeded" {
		t.Fatalf("execute status = %s/%s, want ok/succeeded (raw: %s)", res.Status, res.Result.Status, env.Data)
	}
	if len(res.Result.Steps) != 1 {
		t.Fatalf("got %d steps, want 1: %s", len(res.Result.Steps), env.Data)
	}
	s := res.Result.Steps[0]
	if s.Operation != "UpdateVcn" || s.HTTPStatus != 200 || s.Etag != "etag-1" || s.Error != "" {
		t.Errorf("step = %+v, want UpdateVcn/200/etag-1/no-error", s)
	}

	// The guest signed the OCI canonical string with the templated path...
	signedString := mock.Signed[0]
	actuateReq := mock.lastActuate(t).Raw
	if !strings.Contains(signedString, "(request-target): put /vcns/ocid1.vcn.oc1..xyz") {
		t.Errorf("signed string missing templated request-target:\n%s", signedString)
	}
	if !strings.Contains(signedString, "host: iaas.eu-frankfurt-1.oraclecloud.com") {
		t.Errorf("signed string missing host header:\n%s", signedString)
	}
	// ...and actuated to the concrete URL carrying its own Signature Authorization.
	if !strings.Contains(actuateReq, `"url":"https://iaas.eu-frankfurt-1.oraclecloud.com/vcns/ocid1.vcn.oc1..xyz"`) {
		t.Errorf("actuate URL wrong:\n%s", actuateReq)
	}
	if !strings.Contains(actuateReq, `keyId=\"tenancy/user/fp\"`) || !strings.Contains(actuateReq, "FAKESIG==") {
		t.Errorf("actuate Authorization missing keyId/signature:\n%s", actuateReq)
	}
}

func writeString(t *testing.T, ctx context.Context, instance api.Module, allocateFn api.Function, s string) (uint32, uint32) {
	data := []byte(s)
	results, err := allocateFn.Call(ctx, uint64(len(data)))
	if err != nil {
		t.Fatalf("allocate failed: %v", err)
	}
	ptr := uint32(results[0])
	if !instance.Memory().Write(ptr, data) {
		t.Fatalf("Memory.Write failed for %q", s)
	}
	return ptr, uint32(len(data))
}
