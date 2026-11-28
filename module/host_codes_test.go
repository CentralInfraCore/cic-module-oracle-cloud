//go:build !wasip1

package main

// host_codes_test.go mirrors the named denial and failure codes of the cic-flow.send contract
// (CIC_Relay pkg/hostsign/types.go, contract 03 section 6, SS-01..05). They are duplicated here
// only so the module's tests can name the code they expect: the module does not import the relay,
// and a code the guest invents would prove nothing. Keep in sync with the relay's constants.
const (
	hostCodePolicyHost         = "POLICY_HOST"
	hostCodePolicyPath         = "POLICY_PATH"
	hostCodePolicyMethod       = "POLICY_METHOD"
	hostCodePolicyTarget       = "POLICY_TARGET"
	hostCodePolicyBinding      = "POLICY_BINDING"
	hostCodeHeaderForbidden    = "HEADER_FORBIDDEN"
	hostCodeProfileUnknown     = "PROFILE_UNKNOWN"
	hostCodeKeyRefUnauthorized = "KEY_REF_UNAUTHORIZED"
	hostCodeSignerError        = "SIGNER_ERROR"
	hostCodeTransportError     = "TRANSPORT_ERROR"
	hostCodeRequestInvalid     = "REQUEST_INVALID"
)
