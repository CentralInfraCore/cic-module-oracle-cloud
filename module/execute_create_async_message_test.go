//go:build !wasip1

package main

// execute_create_async_message_test.go pins the wording of the failure of a
// create that the provider ACCEPTED (HTTP 202) with a Work Request but whose
// answer carries no usable id.
//
// Only the message is in scope here. The outcome stays what it was: the step is
// "failed", its class is "provider", no resource_id is reported, the work_request_id
// is kept so the caller can look at it, and the create is neither retried nor
// polled by Execute. What changed is that the message no longer says the resource
// "was created": the provider only accepted the operation, its final state is not
// known yet, and the caller must check the Work Request instead of retrying.

import (
	"strings"
	"testing"
)

// execCreateAnswering runs a create whose answer has the given status, headers and
// body, and returns the single step plus the mock (for the request counters).
func execCreateAnswering(t *testing.T, status int, headers map[string]string, body string) (executionStep, *cicFlowMock) {
	t.Helper()
	mock := &cicFlowMock{Reply: replyWith(status, body, headers)}
	_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)
	if er.Status != "failed" || len(er.Steps) != 1 {
		t.Fatalf("status %q with %d steps, want failed with 1 step: %+v", er.Status, len(er.Steps), er.Steps)
	}
	return er.Steps[0], mock
}

func TestExecuteAsyncCreateWithoutIDDoesNotClaimCreation(t *testing.T) {
	const wr = "ocid1.workrequest.oc1..wr42"
	cases := []struct{ name, body string }{
		{"empty body", ``},
		{"object without an id", `{"status":"ACCEPTED"}`},
		{"non-JSON body", `<html>accepted</html>`},
		{"an id that is not an OCID", `{"id":"not-an-ocid"}`},
		{"a repeated id", `{"id":"ocid1.vcn.oc1..a","id":"ocid1.vcn.oc1..b"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, mock := execCreateAnswering(t, 202, map[string]string{"opc-work-request-id": wr}, tc.body)

			// The outcome is unchanged.
			if s.ErrorClass != classProvider {
				t.Errorf("error_class = %q, want %q", s.ErrorClass, classProvider)
			}
			if s.ResourceID != "" {
				t.Errorf("resource_id = %q, want empty", s.ResourceID)
			}
			if s.WorkRequestID != wr || s.HTTPStatus != 202 {
				t.Errorf("step = %+v, want the work request %q and HTTP 202 kept", s, wr)
			}
			if mock.actuateCount() != 1 || mock.signCount() != 1 {
				t.Errorf("%d requests / %d signs, want exactly 1 / 1 (no retry)", mock.actuateCount(), mock.signCount())
			}

			// Only the wording is new: accepted, final state unknown, check the Work
			// Request, do not retry, and never "the resource was created".
			if strings.Contains(s.Error, "was created") {
				t.Errorf("error = %q: a 202 only means the operation was accepted; it must not say the resource was created", s.Error)
			}
			for _, want := range []string{"accepted", wr, "Work Request", "do NOT retry"} {
				if !strings.Contains(s.Error, want) {
					t.Errorf("error = %q, want it to mention %q", s.Error, want)
				}
			}
		})
	}
}

// TestExecuteCreateWithoutIDScopeOfTheAcceptedWording keeps the change from
// spreading: the "accepted" wording is only for a 202 that carries a Work Request.
func TestExecuteCreateWithoutIDScopeOfTheAcceptedWording(t *testing.T) {
	wrHeader := map[string]string{"opc-work-request-id": "ocid1.workrequest.oc1..wr1"}

	t.Run("a 200 with a work request header keeps the created wording", func(t *testing.T) {
		s, _ := execCreateAnswering(t, 200, wrHeader, `{}`)
		if !strings.Contains(s.Error, "was created") || strings.Contains(s.Error, "accepted the create") {
			t.Errorf("error = %q, want the unchanged 2xx-created wording", s.Error)
		}
	})
	t.Run("a 200 without a work request keeps the created wording", func(t *testing.T) {
		s, _ := execCreateAnswering(t, 200, nil, `{}`)
		if !strings.Contains(s.Error, "was created") || strings.Contains(s.Error, "accepted the create") {
			t.Errorf("error = %q, want the unchanged 2xx-created wording", s.Error)
		}
	})
	t.Run("a 202 without a work request never points at one", func(t *testing.T) {
		s, _ := execCreateAnswering(t, 202, nil, ``)
		if strings.Contains(s.Error, "Work Request") {
			t.Errorf("error = %q: there is no work request to check, so the message must not promise one", s.Error)
		}
		if !strings.Contains(s.Error, "do NOT retry") || s.ErrorClass != classProvider || s.ResourceID != "" {
			t.Errorf("step = %+v, want the retry warning, class provider and no id", s)
		}
	})
}
