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
	"os"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// wasmPath is where `make wasm.build` (mk/wasm.mk) emits the TinyGo artifact.
const wasmPath = "module.wasm"

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

	wasmBytes, err := os.ReadFile(wasmPath)
	if os.IsNotExist(err) {
		t.Skipf("module.wasm not built — run `make wasm.build` first (path: %s)", wasmPath)
	}
	if err != nil {
		t.Fatalf("failed to read %s: %v", wasmPath, err)
	}

	ctx := context.Background()
	runtime := wazero.NewRuntime(ctx)
	t.Cleanup(func() { runtime.Close(ctx) })

	if _, err := wasi_snapshot_preview1.Instantiate(ctx, runtime); err != nil {
		t.Fatalf("failed to instantiate wasi: %v", err)
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

// TestHostLoadDomainError verifies the domain-error path through wasm: a
// sign+send op (execute) that cannot actuate returns a SUCCESSFUL transport call
// (error null) whose data is an "error" providerResult carrying the typed
// scaffold provider-error (provider.go: hostSignSendUnavailable). Domain errors
// live in data, not the transport error slot.
func TestHostLoadDomainError(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "execute", "{}", "{}")

	if string(env.Error) != "null" {
		t.Fatalf("Call(\"execute\"): transport error = %s, want null", env.Error)
	}
	var res struct {
		Status string `json:"status"`
		Error  struct {
			Class        string `json:"class"`
			ProviderCode string `json:"provider_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("Call(\"execute\"): data is not a providerResult: %v (raw: %s)", err, env.Data)
	}
	if res.Status != "error" {
		t.Errorf("Call(\"execute\"): data.status = %q, want %q", res.Status, "error")
	}
	if res.Error.ProviderCode != "HOST_SIGN_SEND_UNAVAILABLE" {
		t.Errorf("Call(\"execute\"): provider_code = %q, want HOST_SIGN_SEND_UNAVAILABLE", res.Error.ProviderCode)
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
