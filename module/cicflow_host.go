//go:build !wasip1

package main

// cicflow_host.go is the host-build (go test) seam for the cic-flow host calls.
// In the wasip1 guest these are backed by //go:wasmimport (cicflow_guest.go); on
// the host they are injectable so Execute can be unit-tested without wasm, and
// default to an error. The primary end-to-end test drives the real wasmimport
// path through a mock cic-flow wazero host module (module_loadtest_test.go).

import "errors"

var (
	// testCallHostSign, when set by a test, stands in for the cic-flow.sign host
	// call in the host build.
	testCallHostSign func(req []byte) ([]byte, error)
	// testCallHostActuate, when set by a test, stands in for the cic-flow.actuate
	// host call in the host build.
	testCallHostActuate func(req []byte) ([]byte, error)
)

// callHostSign is the host-build cic-flow.sign: it delegates to testCallHostSign
// when a test injected one, and otherwise fails.
func callHostSign(req []byte) ([]byte, error) {
	if testCallHostSign != nil {
		return testCallHostSign(req)
	}
	return nil, errors.New("cic-flow.sign unavailable on host")
}

// callHostActuate is the host-build cic-flow.actuate: it delegates to
// testCallHostActuate when a test injected one, and otherwise fails.
func callHostActuate(req []byte) ([]byte, error) {
	if testCallHostActuate != nil {
		return testCallHostActuate(req)
	}
	return nil, errors.New("cic-flow.actuate unavailable on host")
}
