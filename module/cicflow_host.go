//go:build !wasip1

package main

// cicflow_host.go is the host-build (go test) seam for the cic-flow host calls.
// In the wasip1 guest these are backed by //go:wasmimport (cicflow_guest.go); on
// the host they are injectable so Execute can be unit-tested without wasm, and
// default to an error. The primary end-to-end test drives the real wasmimport
// path through a mock cic-flow wazero host module (module_loadtest_test.go).

import "errors"

// testCallHostSend is the injectable cic-flow.send host call for host-side tests.
var testCallHostSend func(req []byte) ([]byte, error)

func callHostSend(req []byte) ([]byte, error) {
	if testCallHostSend != nil {
		return testCallHostSend(req)
	}
	return nil, errors.New("cic-flow.send unavailable on host")
}
