//go:build !wasip1

package main

// execute_bytes_test.go freezes the EXACT bytes Execute emits for non-create
// operations.
//
// Why bytes and not fields: steps[i].resource_id was added to a shape that
// already has consumers (docs/design/manual-verification.md and
// module/manual_real_oci_test.go read steps[]). The guarantee this file carries
// is stronger than "the fields I checked are still right": for update, action
// and delete — and for their async and error answers — the serialized output is
// character-for-character what it was before the field existed.
//
// The golden strings below were not written by hand. They were MEASURED on the
// pristine tree at 04c2482, before steps[i].resource_id existed, with a
// throwaway probe; this file then asserts the same bytes afterwards. The test
// compiles and passes against both trees, which is what makes it a before/after
// proof rather than a snapshot of whatever the code happens to do now.
//
// The deliberate case to look at is update-200-body-with-id: a non-create answer
// that DOES carry an "id" still reports no resource_id. The field follows the
// operation's lifecycle ROLE, not the presence of an id in the response.

import (
	"encoding/json"
	"testing"
	"time"
)

func TestExecuteNonCreateOutputBytesUnchanged(t *testing.T) {
	// A fixed clock: Execute stamps the OCI date header from nowFunc, and a
	// moving clock could only make this test flaky, never more truthful.
	savedNow := nowFunc
	nowFunc = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	defer func() { nowFunc = savedNow }()

	cases := []struct {
		label   string
		po      providerOperation
		status  int
		body    string
		headers map[string]string
		config  string
		want    string
	}{
		{
			label: "update-200-empty-body", po: providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
			status: 200, body: "{}", headers: map[string]string{"etag": "etag-1", "opc-request-id": "req-1"}, config: `{"displayName":"prod"}`,
			want: `{"status":"ok","result":{"status":"succeeded","steps":[{"operation":"UpdateVcn","http_status":200,"etag":"etag-1","opc_request_id":"req-1"}]}}`,
		},
		{
			label: "update-200-no-body", po: providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
			status: 200, body: "", headers: map[string]string{"etag": "etag-1"}, config: `{"displayName":"prod"}`,
			want: `{"status":"ok","result":{"status":"succeeded","steps":[{"operation":"UpdateVcn","http_status":200,"etag":"etag-1"}]}}`,
		},
		{
			// The telling one: an id in a NON-create answer is not reported.
			label: "update-200-body-with-id", po: providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
			status: 200, body: `{"id":"ocid1.vcn.oc1..u"}`, headers: map[string]string{"etag": "etag-1"}, config: `{"displayName":"prod"}`,
			want: `{"status":"ok","result":{"status":"succeeded","steps":[{"operation":"UpdateVcn","http_status":200,"etag":"etag-1"}]}}`,
		},
		{
			label: "action-200-empty-body", po: providerOperation{Operation: "ChangeVcnCompartment", Method: "POST", Path: "/vcns/{vcnId}/actions/changeCompartment"},
			status: 200, body: "{}", headers: map[string]string{"opc-request-id": "req-2"}, config: `{"compartmentId":"ocid1.compartment..n"}`,
			want: `{"status":"ok","result":{"status":"succeeded","steps":[{"operation":"ChangeVcnCompartment","http_status":200,"opc_request_id":"req-2"}]}}`,
		},
		{
			// A delete answers 204 with no body at all — it must not be read as
			// "2xx without an id".
			label: "delete-204-no-body", po: providerOperation{Operation: "DeleteVcn", Method: "DELETE", Path: "/vcns/{vcnId}"},
			status: 204, body: "", headers: map[string]string{}, config: `{}`,
			want: `{"status":"ok","result":{"status":"succeeded","steps":[{"operation":"DeleteVcn","http_status":204}]}}`,
		},
		{
			label: "update-202-async", po: providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
			status: 202, body: "", headers: map[string]string{"opc-work-request-id": "ocid1.wr..a"}, config: `{"displayName":"prod"}`,
			want: `{"status":"ok","result":{"status":"accepted","steps":[{"operation":"UpdateVcn","http_status":202,"work_request_id":"ocid1.wr..a"}]}}`,
		},
		{
			label: "update-409-error", po: providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
			status: 409, body: `{"code":"IncorrectState","message":"nope"}`, headers: map[string]string{}, config: `{"displayName":"prod"}`,
			want: `{"status":"ok","result":{"status":"failed","steps":[{"operation":"UpdateVcn","http_status":409,"error":"nope","error_class":"conflict","provider_code":"IncorrectState"}]}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			mock := &cicFlowMock{
				Signature: "S",
				Reply: func(n int, call cicFlowCall) cicFlowReply {
					return cicFlowReply{Status: tc.status, Body: tc.body, Headers: tc.headers}
				},
			}
			restore := mock.wireHostSeam()
			defer restore()

			// The step mapping is checked on the steps themselves (executeSteps), so a
			// single DeleteVcn or ChangeVcnCompartment step can be exercised on its own;
			// whether a plan may run at all is the gate's business (m4a_gate_test.go),
			// and TestExecuteOutputOnlyAddsTheExecutionDigest ties the two together.
			config, err := decodeObject(json.RawMessage(tc.config))
			if err != nil {
				t.Fatalf("config: %v", err)
			}
			out, err := okResult(executeSteps(resourceContracts()["cic:network:vcn"], []providerOperation{tc.po}, config,
				execBinding{Host: "h", BasePath: "/20160918", KeyID: "k", ResourceID: "ocid1.vcn..z", Revision: "etag-42"}))
			if err != nil {
				t.Fatalf("okResult returned a Go error: %v", err)
			}
			if string(out) != tc.want {
				t.Errorf("Execute output changed for a non-create operation.\n got: %s\nwant: %s", out, tc.want)
			}
		})
	}
}
