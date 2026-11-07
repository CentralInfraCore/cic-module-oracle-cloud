//go:build !wasip1

package main

// execute_create_id_test.go covers the one piece of an Execute outcome that
// cannot be re-derived from its input: the OCID of a resource the run just
// created. The create request's path is /vcns — it carries no resource id — so
// the new id exists ONLY in the provider's answer. Before steps[i].resource_id
// the module read nothing out of that answer, and the id was lost.
//
// What these tests pin down:
//   - the id lands on the create step and comes from the response's own "id",
//     never from binding.resource_id;
//   - every answer that does not carry a usable id FAILS the step, with a
//     message that says the resource was created, because a blind retry would
//     create a second one;
//   - there is no retry: a failed create makes exactly one provider request;
//   - non-create steps are byte-for-byte what they were before this field
//     existed.
//
// These drive Execute directly on the host seam. The same path over the real
// wasm Call export is TestHostLoadExecuteCreateCarriesOCID
// (execute_create_id_wasm_test.go).

import (
	"encoding/json"
	"strings"
	"testing"
)

const vcnKindForExec = "cic:network:vcn"

// createVcnOp is the real CreateVcn operation from the embedded registry
// (schemas/core/vcn.json: POST /vcns, role=create) — the role, not the name, is
// what makes the id mandatory.
var createVcnOp = providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns"}

// execVCN runs one provider operation through Execute against mock and returns
// the raw Execute output plus the decoded execution result.
func execVCN(t *testing.T, mock *cicFlowMock, po providerOperation, binding execBinding, configJSON string) ([]byte, executionResult) {
	t.Helper()
	restore := mock.wireHostSeam()
	defer restore()

	req, _ := json.Marshal(executeRequest{
		Kind:    vcnKindForExec,
		Plan:    executionPlan{ProviderOperations: []providerOperation{po}},
		Config:  schemaPayload{Data: json.RawMessage(configJSON)},
		Binding: binding,
	})
	out, err := Execute(nil, req)
	if err != nil {
		t.Fatalf("Execute returned a Go error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "ok" {
		t.Fatalf("Execute wrapper status = %q, want ok — a domain outcome travels in data (raw: %s)", res.Status, out)
	}
	var er executionResult
	if err := json.Unmarshal(res.Result, &er); err != nil {
		t.Fatalf("executionResult decode: %v (raw: %s)", err, res.Result)
	}
	return out, er
}

// replyWith answers every actuate call with one fixed status/body.
func replyWith(status int, body string, headers map[string]string) func(int, cicFlowCall) cicFlowReply {
	return func(n int, call cicFlowCall) cicFlowReply {
		return cicFlowReply{Status: status, Body: body, Headers: headers}
	}
}

// createBinding is a create's binding: no resource_id, because the resource does
// not exist yet (resolveOp's resource_id requirement is an observe/update/delete
// concern — Execute only requires host and key_id).
var createBinding = execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", BasePath: "/20160918", KeyID: "t/u/f"}

// TestExecuteCreateCarriesTheCreatedOCID is the positive table: a create that
// answered with a well-formed id reports it on its step and succeeds.
func TestExecuteCreateCarriesTheCreatedOCID(t *testing.T) {
	cases := []struct {
		name   string
		status int
		id     string
	}{
		// The two id forms the repo's existing fixtures already use — they must
		// keep passing, or this check would break the suite it ships with.
		{"short fixture form, 200", 200, "ocid1.vcn..z"},
		{"realm fixture form, 200", 200, "ocid1.vcn.oc1..xyz"},
		{"full region form, 200", 200, "ocid1.vcn.oc1.eu-frankfurt-1.amaaaaaaexamplevcn"},
		// OCI answers CreateVcn with 200, but a 2xx that is not 200 is still a
		// completed create and must be treated the same way.
		{"201 Created", 201, "ocid1.vcn.oc1..created"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(tc.status, `{"id":"`+tc.id+`","displayName":"prod","lifecycleState":"PROVISIONING"}`,
				map[string]string{"etag": "etag-c", "opc-request-id": "req-c"})}
			_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c","displayName":"prod"}`)

			if er.Status != "succeeded" {
				t.Fatalf("status = %q, want succeeded: %+v", er.Status, er.Steps)
			}
			if len(er.Steps) != 1 {
				t.Fatalf("got %d steps, want 1", len(er.Steps))
			}
			s := er.Steps[0]
			if s.ResourceID != tc.id {
				t.Errorf("steps[0].resource_id = %q, want %q (the create response's own id)", s.ResourceID, tc.id)
			}
			if s.Error != "" || s.ErrorClass != "" {
				t.Errorf("steps[0] reported an error on a good create: %q / %q", s.Error, s.ErrorClass)
			}
			// The id must not displace the step's existing evidence.
			if s.Operation != "CreateVcn" || s.HTTPStatus != tc.status || s.Etag != "etag-c" || s.OpcRequestID != "req-c" {
				t.Errorf("steps[0] = %+v, want CreateVcn/%d/etag-c/req-c", s, tc.status)
			}
		})
	}
}

// TestExecuteCreateRejectsAnUnusableID is the negative table. Every one of these
// is a create the provider accepted (2xx) whose id we cannot use, so each must
// FAIL with a class and must never produce an empty or invented resource_id.
func TestExecuteCreateRejectsAnUnusableID(t *testing.T) {
	cases := []struct {
		name string
		body string
		why  string
	}{
		{"2xx with no id field", `{"displayName":"prod"}`, "nothing to report"},
		{"2xx with an empty id", `{"id":""}`, "an empty string is not an id"},
		{"2xx with a null id", `{"id":null}`, "null is not an id"},
		{"2xx with an empty object", `{}`, "the create answered nothing at all"},
		{"2xx with an empty body", ``, "no body to read an id from"},
		{"2xx with a non-JSON body", `<html>502 from a proxy</html>`, "the answer is not JSON"},
		{"2xx with a JSON array body", `[{"id":"ocid1.vcn..z"}]`, "the answer is not a JSON object"},
		{"2xx with a non-OCID id", `{"id":"vcn-42"}`, "not an OCID at all"},
		{"2xx with another kind's OCID", `{"id":"ocid1.subnet.oc1..s"}`, "a subnet id cannot be this VCN"},
		{"2xx with a bare prefix", `{"id":"ocid1.vcn."}`, "the prefix alone identifies nothing"},
		{"2xx with a numeric id", `{"id":12345}`, "an id must be a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(200, tc.body, map[string]string{"opc-request-id": "req-bad"})}
			_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

			if er.Status != "failed" {
				t.Fatalf("status = %q, want failed (%s): %+v", er.Status, tc.why, er.Steps)
			}
			if len(er.Steps) != 1 {
				t.Fatalf("got %d steps, want 1", len(er.Steps))
			}
			s := er.Steps[0]
			if s.ResourceID != "" {
				t.Errorf("steps[0].resource_id = %q, want empty — an unusable answer must never yield an id", s.ResourceID)
			}
			if s.ErrorClass == "" {
				t.Errorf("steps[0].error_class is empty; a failed step must carry a CIC class")
			}
			if s.ErrorClass != classProvider {
				t.Errorf("steps[0].error_class = %q, want %q: the provider's answer is what is unusable", s.ErrorClass, classProvider)
			}
			// The message has one job: stop a blind retry from duplicating the VCN.
			for _, want := range []string{"2xx", "created", "id is unknown", "do NOT retry"} {
				if !strings.Contains(s.Error, want) {
					t.Errorf("steps[0].error = %q, want it to mention %q", s.Error, want)
				}
			}
			// Evidence that survives the failure: the request id stays readable.
			if s.OpcRequestID != "req-bad" || s.HTTPStatus != 200 {
				t.Errorf("steps[0] = %+v, want the 200 and opc-request-id preserved", s)
			}
		})
	}
}

// TestExecuteCreateRejectsAnAmbiguousBody pins down that a 2xx answer alone is
// not enough to identify the created resource: if the body cannot be read
// unambiguously, the step FAILS, even when a well-formed OCID is present in it.
//
// The bodies below repeat the "id" key with values of different JSON types. A
// permissive decoder may keep one well-formed string and only complain about the
// other; the response is refused outright because a repeated "id" is ambiguous,
// whatever the types (the same-type cases are in
// TestExecuteCreateRefusesDuplicateAndAmbiguousIDs). An id reported for the
// wrong resource is worse than none.
//
// The assertions are on the OUTCOME only: the status, the absence of a
// resource_id, the CIC class, and that exactly one provider request was made.
// They deliberately do not match the message text: that text is diagnostic, not
// a contract, and is pinned elsewhere only for the one job it has (the
// do-not-retry warning).
func TestExecuteCreateRejectsAnAmbiguousBody(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"valid id first, numeric duplicate last", `{"id":"ocid1.vcn.oc1..ok","id":5}`},
		{"numeric id first, valid duplicate last", `{"id":5,"id":"ocid1.vcn.oc1..ok"}`},
		{"valid id first, array duplicate last", `{"id":"ocid1.vcn.oc1..ok","id":["x"]}`},
		{"object id first, valid duplicate last", `{"id":{"a":1},"id":"ocid1.vcn.oc1..ok"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(200, tc.body, map[string]string{"opc-request-id": "req-amb"})}
			_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

			if er.Status != "failed" {
				t.Fatalf("status = %q, want failed: a 2xx with an ambiguous body must not identify the resource: %+v", er.Status, er.Steps)
			}
			if len(er.Steps) != 1 {
				t.Fatalf("got %d steps, want 1", len(er.Steps))
			}
			s := er.Steps[0]
			if s.ResourceID != "" {
				t.Errorf("steps[0].resource_id = %q, want empty: an ambiguous answer must never yield an id", s.ResourceID)
			}
			if s.ErrorClass != classProvider {
				t.Errorf("steps[0].error_class = %q, want %q", s.ErrorClass, classProvider)
			}
			if s.Error == "" {
				t.Errorf("steps[0].error is empty; a failed step must say something")
			}
			// The 2xx is evidence the create happened; it must survive the failure.
			if s.HTTPStatus != 200 || s.OpcRequestID != "req-amb" {
				t.Errorf("steps[0] = %+v, want the 200 and opc-request-id preserved", s)
			}
			// No automatic retry: one sign, one request.
			if n := mock.actuateCount(); n != 1 {
				t.Errorf("the module made %d provider requests, want exactly 1 — Execute must not retry a create", n)
			}
			if n := mock.signCount(); n != 1 {
				t.Errorf("the module made %d sign calls, want exactly 1", n)
			}
		})
	}
}

// TestExecuteCreateIDComesFromTheResponseNotTheBinding is the anti-echo check. A
// create's path has no resource id, so a resource_id in the binding is at best
// stale and at worst someone else's resource: the step must report the answer's
// id, never the binding's.
func TestExecuteCreateIDComesFromTheResponseNotTheBinding(t *testing.T) {
	const decoy = "ocid1.vcn.oc1..DECOY-FROM-THE-BINDING"
	const actual = "ocid1.vcn.oc1..theonejustcreated"

	mock := &cicFlowMock{Reply: replyWith(200, `{"id":"`+actual+`"}`, nil)}
	binding := createBinding
	binding.ResourceID = decoy
	_, er := execVCN(t, mock, createVcnOp, binding, `{"compartmentId":"ocid1.compartment..c"}`)

	if er.Status != "succeeded" || len(er.Steps) != 1 {
		t.Fatalf("execute = %+v, want succeeded with 1 step", er)
	}
	if got := er.Steps[0].ResourceID; got != actual {
		t.Errorf("steps[0].resource_id = %q, want the response id %q (not the binding's %q)", got, actual, decoy)
	}
	// ...and the create really did go to the collection path, with no id templated in.
	if url := mock.lastActuate(t).URL; !strings.HasSuffix(url, "/20160918/vcns") {
		t.Errorf("create URL = %q, want the collection path /20160918/vcns — a create has no resource id to template", url)
	}
}

// TestExecuteFailedCreateIsNotRetried proves the absence of a retry. A create
// that fails must leave exactly ONE provider request behind: a second attempt
// would be a second VCN.
func TestExecuteFailedCreateIsNotRetried(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"2xx without an id", 200, `{}`},
		{"500 from the provider (retryable class, still not retried here)", 500, `{"code":"InternalError","message":"boom"}`},
		{"429 throttle (retryable class, still not retried here)", 429, `{"code":"TooManyRequests","message":"slow down"}`},
		{"409 conflict", 409, `{"code":"IncorrectState","message":"nope"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(tc.status, tc.body, nil)}
			_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

			if er.Status != "failed" {
				t.Fatalf("status = %q, want failed", er.Status)
			}
			if n := mock.actuateCount(); n != 1 {
				t.Errorf("the module made %d provider requests, want exactly 1 — Execute must not retry a create", n)
			}
			if n := mock.signCount(); n != 1 {
				t.Errorf("the module made %d sign calls, want exactly 1", n)
			}
			if er.Steps[0].ResourceID != "" {
				t.Errorf("steps[0].resource_id = %q, want empty on a failed create", er.Steps[0].ResourceID)
			}
		})
	}
}

// TestExecuteCreateProviderErrorsUnchanged checks that the id requirement did
// not disturb the existing 4xx/5xx mapping on the create path: the CIC class and
// the OCI-native code still come from ociError, and no id is reported.
func TestExecuteCreateProviderErrorsUnchanged(t *testing.T) {
	cases := []struct {
		status    int
		wantClass string
		wantCode  string
	}{
		{400, classValidation, "InvalidParameter"},
		{401, classPermission, "NotAuthenticated"},
		{404, classNotFound, "NotAuthorizedOrNotFound"},
		{409, classConflict, "IncorrectState"},
		{429, classTransport, "TooManyRequests"},
		{503, classProvider, "ServiceUnavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.wantClass, func(t *testing.T) {
			body := `{"code":"` + tc.wantCode + `","message":"from OCI"}`
			mock := &cicFlowMock{Reply: replyWith(tc.status, body, nil)}
			_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

			if er.Status != "failed" || len(er.Steps) != 1 {
				t.Fatalf("execute = %+v, want failed with 1 step", er)
			}
			s := er.Steps[0]
			if s.ErrorClass != tc.wantClass || s.ProviderCode != tc.wantCode {
				t.Errorf("steps[0] = %s/%s, want %s/%s", s.ErrorClass, s.ProviderCode, tc.wantClass, tc.wantCode)
			}
			if s.Error != "from OCI" {
				t.Errorf("steps[0].error = %q, want the OCI message verbatim — not the id-unknown message", s.Error)
			}
			if s.ResourceID != "" {
				t.Errorf("steps[0].resource_id = %q, want empty: an error answer created nothing", s.ResourceID)
			}
		})
	}
}

// TestExecuteAsyncCreateMockOnly records what a create that answers 202 with a
// Work Request does.
//
// MOCK-ONLY: real OCI never does this for CreateVcn. The create is synchronous
// and returns the VCN inline — measured against real OCI and written down in
// docs/design/manual-verification.md ("CreateVcn/UpdateVcn are synchronous and
// never produce one"). So this is a hypothetical answer shape, kept because the
// module must behave defensibly if some future operation does it: the work
// request id is still surfaced (unchanged), and a 202 WITHOUT an id is still a
// create whose id we do not have — reported as failed, not as a success that
// quietly dropped the OCID.
func TestExecuteAsyncCreateMockOnly(t *testing.T) {
	t.Run("202 with a work request and an id: accepted, id reported", func(t *testing.T) {
		mock := &cicFlowMock{Reply: replyWith(202, `{"id":"ocid1.vcn.oc1..async"}`,
			map[string]string{"opc-work-request-id": "ocid1.wr..a"})}
		_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

		if er.Status != "accepted" {
			t.Errorf("status = %q, want accepted — an outstanding work request is not done", er.Status)
		}
		if er.Steps[0].WorkRequestID != "ocid1.wr..a" {
			t.Errorf("steps[0].work_request_id = %q, want it surfaced unchanged", er.Steps[0].WorkRequestID)
		}
		if er.Steps[0].ResourceID != "ocid1.vcn.oc1..async" {
			t.Errorf("steps[0].resource_id = %q, want the response id", er.Steps[0].ResourceID)
		}
	})

	t.Run("202 with a work request and no id: failed, work request still reported", func(t *testing.T) {
		mock := &cicFlowMock{Reply: replyWith(202, `{}`,
			map[string]string{"opc-work-request-id": "ocid1.wr..b"})}
		_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

		if er.Status != "failed" {
			t.Errorf("status = %q, want failed: a create whose id we never learned is not a success", er.Status)
		}
		if er.Steps[0].WorkRequestID != "ocid1.wr..b" {
			t.Errorf("steps[0].work_request_id = %q, want it surfaced unchanged so the caller can poll", er.Steps[0].WorkRequestID)
		}
		if er.Steps[0].ResourceID != "" {
			t.Errorf("steps[0].resource_id = %q, want empty", er.Steps[0].ResourceID)
		}
	})
}

// TestOCIDPrefixForKind pins the derivation of the expected OCID prefix from the
// CIC kind. It is derived, not hard-coded per resource, so the create check is
// correct for the other kinds the module already carries.
func TestOCIDPrefixForKind(t *testing.T) {
	cases := map[string]string{
		"cic:network:vcn":      "ocid1.vcn.",
		"cic:network:subnet":   "ocid1.subnet.",
		"cic:compute:instance": "ocid1.instance.",
		"":                     "ocid1.",   // degenerate: no kind segment to use
		"vcn":                  "ocid1.",   // no ':' separator
		"a:":                   "ocid1.",   // empty trailing segment
		"x:y:z":                "ocid1.z.", // always the LAST segment
	}
	for kind, want := range cases {
		if got := ocidPrefixForKind(kind); got != want {
			t.Errorf("ocidPrefixForKind(%q) = %q, want %q", kind, got, want)
		}
	}
}

// TestCreatedResourceIDIsNeverGuessed is the unit-level restatement of the rule
// the Execute tables assert end-to-end: the helper returns an id or an error,
// never a fabricated or partial value.
func TestCreatedResourceIDIsNeverGuessed(t *testing.T) {
	good := []string{"ocid1.vcn..z", "ocid1.vcn.oc1..xyz", "ocid1.vcn.oc1.eu-frankfurt-1.aaaa"}
	for _, id := range good {
		got, pe := createdResourceID(vcnKindForExec, []byte(`{"id":"`+id+`"}`), 200, "")
		if pe != nil {
			t.Errorf("createdResourceID(%q) errored: %s", id, pe.Message)
		}
		if got != id {
			t.Errorf("createdResourceID(%q) = %q, want it verbatim", id, got)
		}
	}
	bad := []string{`{}`, ``, `null`, `{"id":""}`, `{"id":"ocid1.vcn."}`, `{"id":"ocid1.subnet..s"}`, `not json`, `[]`}
	for _, body := range bad {
		got, pe := createdResourceID(vcnKindForExec, []byte(body), 200, "")
		if pe == nil {
			t.Errorf("createdResourceID(%q) returned %q with no error, want an error", body, got)
			continue
		}
		if got != "" {
			t.Errorf("createdResourceID(%q) returned %q alongside an error, want the empty string", body, got)
		}
		if pe.Retryable {
			t.Errorf("createdResourceID(%q): retryable=true, want false — retrying a create duplicates the resource", body)
		}
	}
}
