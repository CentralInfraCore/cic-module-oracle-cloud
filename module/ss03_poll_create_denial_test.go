//go:build !wasip1

package main

// ss03_poll_create_denial_test.go records a deliberate, measured deviation, not a feature: the
// frozen SS-03 target contract cannot express the module's `poll` and `create` requests, and it is
// NOT being extended for them (the observation PoC needs neither -- see the WP-04b job's
// ref/ss03-poll-create-deviation.md).
//
//   - poll asks for GET /<api version>/workRequests/<work request OCID>. SS-03 matches the request
//     path against the policy's path_template with {resource_id} substituted, and a Work Request is
//     a different resource type with no binding in the policy, so the host refuses it.
//   - create asks for POST /<api version>/<collection>. It has no resource_id yet (the provider
//     mints it in the response), and the observation profile forbids every method but GET/HEAD, so
//     the host refuses it.
//
// WHICH code comes back was measured against the relay's own hostsign.Policy.ValidateTarget, not
// assumed (the WP-04b job's output/evidence/50-ss03-relay-measurement.log):
//
//   intent.operation as the module sends it ("poll", "execute" with no policy entry) -> POLICY_TARGET
//   the same paths under an operation the policy DOES define ("observe")             -> POLICY_PATH
//                                                                                       (Work Request path)
//                                                                                    -> POLICY_METHOD
//                                                                                       (POST under
//                                                                                        ObservationOnly)
//
// ValidateTarget reports the FIRST failing step in a fixed order (operation, host, method, resource,
// binding, path), which is why an unknown operation name wins over the path and method reasons. Both
// codes are pinned below: the module must surface whichever the host chose, unchanged.
//
// These tests pin that the module (a) still sends exactly the request described above, so the
// deviation is visible rather than hidden, and (b) surfaces the host's refusal with the host's own
// named code, never as a success and never as a generic error. The guest does not pre-empt the
// refusal: deciding the target is the host's job, and faking the refusal in the guest would hide a
// future policy change.

import (
	"strings"
	"testing"
)

// denyWith makes the simulated host refuse every send with the given contract code, before it signs
// or sends anything.
func denyWith(code string) func(int, cicFlowSend) (string, string, string) {
	return func(int, cicFlowSend) (string, string, string) {
		return "denied", code, "outside the operator policy (SS-03)"
	}
}

// Poll's target is a Work Request path, which the frozen SS-03 policy cannot express. The host
// answers POLICY_TARGET (measured) or POLICY_PATH (when the operation name is in the policy), and
// the module reports it as a transport-class error carrying that very code.
func TestPollWorkRequestTargetIsDeniedWithPolicyPath(t *testing.T) {
	for _, code := range []string{hostCodePolicyTarget, hostCodePolicyPath} {
		t.Run(code, func(t *testing.T) { pollDeniedWith(t, code) })
	}
}

// pollDeniedWith runs one poll against a host that refuses with the given contract code.
func pollDeniedWith(t *testing.T, code string) {
	t.Helper()
	mock := newPollMock()
	mock.Refuse = denyWith(code)
	res := callPoll(t, mock, pollRequest{Binding: pollBinding(pollVersion),
		WorkRequestID: "ocid1.workrequest.oc1..aaaaaaaapoll"})

	if res.Status != "error" || res.Error == nil {
		t.Fatalf("status = %q, want an error: a denied poll must not look like a successful one", res.Status)
	}
	if res.Error.Class != classTransport {
		t.Errorf("error class = %q, want %q", res.Error.Class, classTransport)
	}
	if !strings.Contains(res.Error.Message, "send denied: "+code) {
		t.Errorf("message = %q, want it to carry %q", res.Error.Message, "send denied: "+code)
	}
	// The module did ask -- once -- and the host refused before signing or sending anything.
	if mock.sendCount() != 1 {
		t.Fatalf("%d cic-flow.send calls, want exactly 1", mock.sendCount())
	}
	if mock.signCount() != 0 || mock.actuateCount() != 0 {
		t.Errorf("%d sign / %d actuate, want none: a denial happens before anything is signed or sent",
			mock.signCount(), mock.actuateCount())
	}
	// This is the request SS-03 cannot express, recorded verbatim.
	sent := mock.lastSend(t)
	if sent.Method != "GET" || sent.Path != pollVersion+"/workRequests/ocid1.workrequest.oc1..aaaaaaaapoll" {
		t.Errorf("poll sent %s %s, want GET %s/workRequests/<ocid>", sent.Method, sent.Path, pollVersion)
	}
	if sent.Operation != "poll" {
		t.Errorf("intent.operation = %q, want poll", sent.Operation)
	}
	// The Work Request is not the bound resource, which is exactly why the policy cannot match it.
	if sent.ResourceID != "" {
		t.Errorf("intent.resource_id = %q, want empty: a Work Request is not the policy-bound resource", sent.ResourceID)
	}
}

// A create is a POST with no resource id yet; the observation profile forbids it. The host answers
// POLICY_TARGET (measured) or POLICY_METHOD (when the operation name is in the policy), and the
// step fails with that code and no resource_id -- and is not retried.
func TestCreateIsDeniedWithPolicyMethod(t *testing.T) {
	for _, code := range []string{hostCodePolicyTarget, hostCodePolicyMethod} {
		t.Run(code, func(t *testing.T) { createDeniedWith(t, code) })
	}
}

// createDeniedWith runs one create plan against a host that refuses with the given contract code.
func createDeniedWith(t *testing.T, code string) {
	t.Helper()
	mock := &cicFlowMock{Reply: replyWith(200, `{"id":"ocid1.vcn.oc1..created"}`, nil)}
	mock.Refuse = denyWith(code)
	_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

	if er.Status != "failed" {
		t.Fatalf("execute status = %q, want failed: a denied create must not look like a successful one", er.Status)
	}
	if len(er.Steps) != 1 {
		t.Fatalf("%d steps, want 1", len(er.Steps))
	}
	step := er.Steps[0]
	if !strings.Contains(step.Error, "send denied: "+code) {
		t.Errorf("step error = %q, want it to carry %q", step.Error, "send denied: "+code)
	}
	if step.ResourceID != "" {
		t.Errorf("steps[0].resource_id = %q, want empty: nothing was created", step.ResourceID)
	}
	// Exactly one attempt: a retry could create a duplicate resource.
	if mock.sendCount() != 1 {
		t.Errorf("%d cic-flow.send calls, want exactly 1 (no retry behind the caller's back)", mock.sendCount())
	}
	if mock.signCount() != 0 || mock.actuateCount() != 0 {
		t.Errorf("%d sign / %d actuate, want none: a denial happens before anything is signed or sent",
			mock.signCount(), mock.actuateCount())
	}
	// The request SS-03 cannot express: a POST to the collection, with no resource id to bind.
	sent := mock.lastSend(t)
	if sent.Method != "POST" || sent.Path != "/20160918/vcns" {
		t.Errorf("create sent %s %s, want POST /20160918/vcns", sent.Method, sent.Path)
	}
	if sent.ResourceID != "" {
		t.Errorf("intent.resource_id = %q, want empty: a create has no resource id yet", sent.ResourceID)
	}
}

// A denial is distinguishable from a failure: the host's code travels either way, and neither is
// reported as a completed call.
func TestDenialAndFailureKeepTheirOwnWording(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, code, want string
	}{
		{"denied", "denied", hostCodePolicyTarget, "send denied: " + hostCodePolicyTarget},
		{"failed", "failed", hostCodeSignerError, "send failed: " + hostCodeSignerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newPollMock()
			mock.Refuse = func(int, cicFlowSend) (string, string, string) { return tc.outcome, tc.code, "" }
			res := callPoll(t, mock, pollRequest{Binding: pollBinding(pollVersion),
				WorkRequestID: "ocid1.workrequest.oc1..aaaaaaaapoll"})
			if res.Status != "error" || res.Error == nil {
				t.Fatalf("status = %q, want an error", res.Status)
			}
			if res.Error.Message != tc.want {
				t.Errorf("message = %q, want %q", res.Error.Message, tc.want)
			}
		})
	}
}
