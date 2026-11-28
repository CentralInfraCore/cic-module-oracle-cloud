//go:build wasip1

package main

// cicflow_guest.go binds the relay's `cic-flow` host module. Since O-1 WP-04 the
// guest's only host import is `cic-flow.send`: the host signs and sends, so the
// guest needs neither a raw sign nor a raw actuate import (D-11). The ABI is the
// `git` host-module convention: a JSON request at
// (reqPtr, reqLen), a JSON response written into a guest-allocated buffer at
// (outPtr, outLen), and a return of bytes written or -1 (did not fit / memory
// error). A host-side failure is an {"error": "..."} JSON body with a positive
// return. See docs/design/specs/relay-sign-send-interface.md.

import (
	"errors"
	"unsafe"
)

// wasmCicFlowSend is the relay's cic-flow.send host import, and the ONLY host
// import of the sign+send path: it takes a JSON sign+send request at
// (reqPtr, reqLen), writes the JSON response into (outPtr, outLen) and returns
// the bytes written, or -1 when the response did not fit or memory access failed.
// The host validates the target against the operator policy, builds the canonical
// string, signs it with a key this guest cannot name, and sends it; the guest
// receives the HTTP result only.
//
//go:wasmimport cic-flow send
func wasmCicFlowSend(reqPtr, reqLen, outPtr, outLen uint32) int32

// cicFlowOutCap is the response buffer the guest allocates per host call. OCI
// bodies are small (a VCN/subnet is a few KiB); 256 KiB is ample headroom.
const cicFlowOutCap = 1 << 18

// hostCall marshals the request into guest memory, allocates the response
// buffer, invokes the host function, and returns the bytes it wrote.
func hostCall(fn func(uint32, uint32, uint32, uint32) int32, req []byte) ([]byte, error) {
	reqPtr := allocate(uint32(len(req)))
	defer deallocate(reqPtr, uint32(len(req)))
	if len(req) > 0 {
		copy(unsafe.Slice((*byte)(unsafe.Pointer(reqPtr)), len(req)), req)
	}
	outPtr := allocate(cicFlowOutCap)
	defer deallocate(outPtr, cicFlowOutCap)

	n := fn(uint32(reqPtr), uint32(len(req)), uint32(outPtr), cicFlowOutCap)
	if n < 0 {
		return nil, errors.New("cic-flow: response did not fit the buffer or a memory error occurred")
	}
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(unsafe.Pointer(outPtr)), int(n)))
	return out, nil
}

// callHostSend sends req to the host's cic-flow.send and returns its JSON
// response; a host-side denial or failure arrives in that body, not as err. A
// //go:wasmimport function cannot be used as a value, so the import is wrapped in
// a plain closure before handing it to hostCall.
func callHostSend(req []byte) ([]byte, error) {
	return hostCall(func(rp, rl, op, ol uint32) int32 { return wasmCicFlowSend(rp, rl, op, ol) }, req)
}
