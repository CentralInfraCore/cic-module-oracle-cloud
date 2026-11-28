//go:build !wasip1

package main

// cicflow_host.go is the host-build (go test) seam for the cic-flow.send host
// call. In the wasip1 guest it is backed by //go:wasmimport (cicflow_guest.go); on
// the host it is injectable so Execute can be unit-tested without wasm, and
// defaults to an error. The primary end-to-end test drives the real wasmimport
// path through a mock cic-flow wazero host module (module_loadtest_test.go).

import "errors"

// testCallHostSend, when set by a test, stands in for the cic-flow.send host call
// in the host build.
var testCallHostSend func(req []byte) ([]byte, error)

// callHostSend is the host-build cic-flow.send: it delegates to testCallHostSend
// when a test injected one, and otherwise fails.
func callHostSend(req []byte) ([]byte, error) {
	if testCallHostSend != nil {
		return testCallHostSend(req)
	}
	return nil, errors.New("cic-flow.send unavailable on host")
}
