//go:build !wasip1

package main

// abi_negative_test.go extends the host-load smoke test
// (module_loadtest_test.go) with negative-path and memory-boundary
// coverage for the guest <-> host ABI (KB c689,
// docs/contracts/host-expectations.md):
//   - empty op string
//   - empty payload (data="")
//   - oversized payload
//   - invalid pointer/length at the host-wrapper level (Memory().Read/Write
//     out-of-bounds — abi.go's readBytes trusts the host to only ever pass
//     ptr/len pairs that Memory().Write succeeded for; cicwasm.go enforces
//     this before calling Call, so this test exercises that host-side guard
//     directly rather than the guest, which has no bounds-check of its own).

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHostLoadEmptyOp verifies that an empty op string is treated like any
// other unknown op: a CodeInput error envelope (abi.go: Call default case).
func TestHostLoadEmptyOp(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "", "{}", "{}")

	if string(env.Data) != "null" {
		t.Errorf("Call(\"\"): data = %s, want null", env.Data)
	}
	var gerr envelopeError
	if err := json.Unmarshal(env.Error, &gerr); err != nil {
		t.Fatalf("Call(\"\"): error is not a valid error envelope: %v (raw: %s)", err, env.Error)
	}
	if gerr.Code != "INPUT" {
		t.Errorf("Call(\"\"): error.code = %q, want %q", gerr.Code, "INPUT")
	}
}

// TestHostLoadEmptyPayload verifies the empty-payload (data="") path: describe
// ignores its data argument (provider.go: Describe), so an empty payload must
// still return an "ok" providerResult with the module manifest.
func TestHostLoadEmptyPayload(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "describe", "{}", "")

	if string(env.Error) != "null" {
		t.Fatalf("Call(\"describe\", data=\"\"): error = %s, want null", env.Error)
	}
	var res struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("Call(\"describe\", data=\"\"): data is not a providerResult: %v (raw: %s)", err, env.Data)
	}
	if res.Status != "ok" {
		t.Errorf("Call(\"describe\", data=\"\"): data.status = %q, want %q", res.Status, "ok")
	}
}

// TestHostLoadEmptyPayloadProviderOps covers the empty-payload (data="") variant
// across the provider op set: every op returns a null transport error and a
// data payload that decodes as a providerResult (provider.go).
func TestHostLoadEmptyPayloadProviderOps(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	for _, op := range []string{"describe", "validate", "plan", "observe", "execute"} {
		t.Run(op, func(t *testing.T) {
			env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, op, "", "")
			if string(env.Error) != "null" {
				t.Errorf("Call(%q, data=\"\"): transport error = %s, want null", op, env.Error)
			}
			var res struct {
				Status string `json:"status"`
			}
			if err := json.Unmarshal(env.Data, &res); err != nil {
				t.Fatalf("Call(%q, data=\"\"): data is not a providerResult: %v (raw: %s)", op, err, env.Data)
			}
			if res.Status != "ok" && res.Status != "error" {
				t.Errorf("Call(%q, data=\"\"): data.status = %q, want ok|error", op, res.Status)
			}
		})
	}
}

// TestHostLoadOversizedPayload verifies a large (~256KiB) invalid-JSON payload
// round-trips through allocate/Call/deallocate without crashing the instance.
// validate rejects the non-JSON request as a DOMAIN error (provider.go:
// Validate), so the transport call still succeeds (error null) and data is an
// "error" providerResult.
func TestHostLoadOversizedPayload(t *testing.T) {
	ctx, instance, callFn, allocateFn, deallocateFn := loadModule(t)

	big := strings.Repeat("x", 256*1024) // not valid JSON

	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "validate", "{}", big)

	if string(env.Error) != "null" {
		t.Fatalf("Call(\"validate\", data=<256KiB>): transport error = %s, want null", env.Error)
	}
	var res struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("Call(\"validate\", data=<256KiB>): data is not a providerResult: %v (raw: %s)", err, env.Data)
	}
	if res.Status != "error" {
		t.Errorf("Call(\"validate\", data=<256KiB>): data.status = %q, want %q", res.Status, "error")
	}
}

// TestHostMemoryOutOfBoundsAccess documents and verifies the memory-boundary
// guard the relay host (cicwasm.go: writeStringToWasm/callGuest) relies on:
// wazero's Memory().Write/Read return ok=false for ptr/len pairs that fall
// outside the instance's linear memory, instead of panicking or reading
// out-of-bounds. abi.go's readBytes (module/abi.go) has no equivalent
// bounds-check on the guest side — it trusts the host to only ever supply
// ptr/len pairs it successfully wrote. cicwasm.go's callGuest enforces this
// invariant by checking writeStringToWasm's error before calling Call, so
// an invalid ptr/len combination never reaches the guest in practice.
func TestHostLoadMemoryOutOfBoundsAccess(t *testing.T) {
	_, instance, _, _, _ := loadModule(t)

	memSize := instance.Memory().Size()

	// A pointer at the very end of memory with a length that overruns it.
	if _, ok := instance.Memory().Read(memSize-1, 16); ok {
		t.Errorf("Memory().Read(memSize-1, 16) = ok, want out-of-bounds failure (memSize=%d)", memSize)
	}

	// A pointer beyond the end of memory entirely.
	if _, ok := instance.Memory().Read(memSize+1024, 16); ok {
		t.Errorf("Memory().Read(memSize+1024, 16) = ok, want out-of-bounds failure (memSize=%d)", memSize)
	}

	// Writing past the end of memory must fail the same way.
	if ok := instance.Memory().Write(memSize-1, []byte{0, 1, 2, 3}); ok {
		t.Errorf("Memory().Write(memSize-1, ...) = ok, want out-of-bounds failure (memSize=%d)", memSize)
	}

	// A maximal uint32 pointer/length must fail without panicking.
	if _, ok := instance.Memory().Read(0xFFFFFFFF, 0xFFFFFFFF); ok {
		t.Errorf("Memory().Read(0xFFFFFFFF, 0xFFFFFFFF) = ok, want out-of-bounds failure")
	}
}
