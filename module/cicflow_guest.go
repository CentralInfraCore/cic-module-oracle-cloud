//go:build wasip1

package main

// cicflow_guest.go binds the relay's `cic-flow` host module (R1/R2, landed in
// CIC_Relay#91). The ABI is the `git` host-module convention: a JSON request at
// (reqPtr, reqLen), a JSON response written into a guest-allocated buffer at
// (outPtr, outLen), and a return of bytes written or -1 (did not fit / memory
// error). A host-side failure is an {"error": "..."} JSON body with a positive
// return. See docs/design/specs/relay-sign-send-interface.md.

import (
	"errors"
	"unsafe"
)

//go:wasmimport cic-flow sign
func wasmCicFlowSign(reqPtr, reqLen, outPtr, outLen uint32) int32

//go:wasmimport cic-flow actuate
func wasmCicFlowActuate(reqPtr, reqLen, outPtr, outLen uint32) int32

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

// A //go:wasmimport function cannot be used as a value, so wrap each in a plain
// closure before handing it to hostCall.
func callHostSign(req []byte) ([]byte, error) {
	return hostCall(func(rp, rl, op, ol uint32) int32 { return wasmCicFlowSign(rp, rl, op, ol) }, req)
}

func callHostActuate(req []byte) ([]byte, error) {
	return hostCall(func(rp, rl, op, ol uint32) int32 { return wasmCicFlowActuate(rp, rl, op, ol) }, req)
}
