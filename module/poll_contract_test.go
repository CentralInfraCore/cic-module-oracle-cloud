//go:build !wasip1

package main

// poll_contract_test.go pins the Poll boundary (M5). Poll sends a SIGNED GET, so the
// caller must not choose where it goes: the target is built by the module from a
// Work Request id (the id the Execute result already returns), and the legacy
// `path` field is only an alias that must reduce to the very same id and the very
// same canonical request path. Anything else is refused before a signature or a
// request exists.
//
// Assertions are on the outcome: status, CIC error class, field path, the count of
// sign/actuate calls at the host seam, and the exact request that was sent. They do
// not match message text. A well-formed id says nothing about whether the caller may
// poll that Work Request, or whether it belongs to a plan: those are other layers.

import (
	"encoding/json"
	"strings"
	"testing"
)

const pollVersion = "/20160918"

func pollBinding(base string) execBinding {
	return execBinding{Host: bindingHost, BasePath: base, KeyID: "t/u/f"}
}

func callPoll(t *testing.T, mock *cicFlowMock, req pollRequest) providerResult {
	t.Helper()
	restore := mock.wireHostSeam()
	defer restore()
	out, err := Poll(nil, marshalReq(t, req))
	if err != nil {
		t.Fatalf("Poll returned a Go error: %v", err)
	}
	return decodeResult(t, out)
}

func wantPollRefusal(t *testing.T, res providerResult, mock *cicFlowMock, field string) {
	t.Helper()
	if res.Status != "error" || res.Error == nil {
		t.Fatalf("status = %q, want an error", res.Status)
	}
	if res.Error.Class != classValidation {
		t.Errorf("error class = %q, want %q", res.Error.Class, classValidation)
	}
	if field != "" && res.Error.FieldPath != field {
		t.Errorf("field_path = %q, want %q", res.Error.FieldPath, field)
	}
	if mock.signCount() != 0 || mock.actuateCount() != 0 {
		t.Errorf("%d sign / %d actuate calls, want none: refuse before anything is signed or sent",
			mock.signCount(), mock.actuateCount())
	}
}

func newPollMock() *cicFlowMock {
	return &cicFlowMock{Reply: replyWith(200, `{"status":"SUCCEEDED","percentComplete":100.0}`, nil)}
}

const goodWR = "ocid1.wr..a"

// badAliasPaths are values of the legacy `path` field that are not exactly
// "/<8 digits>/workRequests/<well-formed id>".
func badAliasPaths() []struct{ name, path string } {
	id := goodWR
	return []struct{ name, path string }{
		{"no leading slash", "20160918/workRequests/" + id},
		{"double slash", "//20160918/workRequests/" + id},
		{"scheme-relative host", "//evil.example/x"},
		{"absolute URL", "https://evil.example" + pollVersion + "/workRequests/" + id},
		{"traversal", pollVersion + "/workRequests/" + id + "/../../instances/x"},
		{"encoded traversal", pollVersion + "/workRequests/%2e%2e/instances/x"},
		{"encoded slash lower", pollVersion + "/workRequests/ocid1.wr..a%2fx"},
		{"encoded slash upper", pollVersion + "/workRequests/ocid1.wr..a%2Fx"},
		{"query", pollVersion + "/workRequests/" + id + "?x=1"},
		{"fragment", pollVersion + "/workRequests/" + id + "#f"},
		{"semicolon", pollVersion + "/workRequests/" + id + ";x"},
		{"backslash", pollVersion + "/workRequests\\" + id},
		{"space", pollVersion + "/workRequests/" + id + " x"},
		{"newline", pollVersion + "/workRequests/" + id + "\nx"},
		{"tab", pollVersion + "/workRequests/" + id + "\tx"},
		{"NUL", pollVersion + "/workRequests/" + id + "\x00"},
		{"non-ASCII", pollVersion + "/workRequests/ocid1.wr.." + string(rune(0xe9))},
		{"another collection", pollVersion + "/instances/" + id},
		{"singular collection", pollVersion + "/workRequest/" + id},
		{"collection in upper case", pollVersion + "/WorkRequests/" + id},
		{"bare collection", pollVersion + "/workRequests"},
		{"bare collection with slash", pollVersion + "/workRequests/"},
		{"sub-resource errors", pollVersion + "/workRequests/" + id + "/errors"},
		{"sub-resource logs", pollVersion + "/workRequests/" + id + "/logs"},
		{"extra segment", pollVersion + "/workRequests/" + id + "/x"},
		{"trailing slash", pollVersion + "/workRequests/" + id + "/"},
		{"version too short", "/2016091/workRequests/" + id},
		{"version too long", "/201609188/workRequests/" + id},
		{"version with a letter", "/20160918x/workRequests/" + id},
		{"version word", "/v1/workRequests/" + id},
		{"no version", "/workRequests/" + id},
		{"id is a bare prefix", pollVersion + "/workRequests/ocid1.wr"},
		{"id without unique part", pollVersion + "/workRequests/ocid1.wr.."},
		{"id with braces", pollVersion + "/workRequests/ocid1.wr..{a}"},
		{"id too long", pollVersion + "/workRequests/ocid1.wr.." + strings.Repeat("a", 300)},
		{"id is not an OCID", pollVersion + "/workRequests/wr-42"},
	}
}

func badWorkRequestIDs() []struct{ name, id string } {
	return []struct{ name, id string }{
		{"bare prefix", "ocid1.wr"},
		{"without unique part", "ocid1.wr.."},
		{"braces", "ocid1.wr..{a}"},
		{"too long", "ocid1.wr.." + strings.Repeat("a", 300)},
		{"not an OCID", "wr-42"},
		{"slash", "ocid1.wr..a/b"},
		{"query", "ocid1.wr..a?b"},
		{"fragment", "ocid1.wr..a#b"},
		{"encoded slash", "ocid1.wr..a%2fb"},
		{"dot dot", ".."},
		{"traversal", "ocid1.wr..a/../../instances/x"},
		{"space", "ocid1.wr..a b"},
		{"newline", "ocid1.wr..a\nb"},
		{"non-ASCII", "ocid1.wr.." + string(rune(0xe9))},
		{"a whole path", pollVersion + "/workRequests/ocid1.wr..a"},
	}
}

func TestPollRefusesALegacyPathThatIsNotExactlyTheWorkRequestShape(t *testing.T) {
	for _, base := range []string{pollVersion, ""} {
		for _, bad := range badAliasPaths() {
			t.Run("base="+base+"/"+bad.name, func(t *testing.T) {
				mock := newPollMock()
				res := callPoll(t, mock, pollRequest{Binding: pollBinding(base), Path: bad.path})
				wantPollRefusal(t, res, mock, "path")
			})
		}
	}
}

func TestPollRefusesALegacyPathWhoseVersionIsNotTheBindingBasePath(t *testing.T) {
	mock := newPollMock()
	res := callPoll(t, mock, pollRequest{Binding: pollBinding(pollVersion), Path: "/20200101/workRequests/" + goodWR})
	wantPollRefusal(t, res, mock, "path")
}

func TestPollRefusesABadWorkRequestID(t *testing.T) {
	for _, bad := range badWorkRequestIDs() {
		t.Run(bad.name, func(t *testing.T) {
			mock := newPollMock()
			res := callPoll(t, mock, pollRequest{Binding: pollBinding(pollVersion), WorkRequestID: bad.id})
			wantPollRefusal(t, res, mock, "work_request_id")
		})
	}
}

func TestPollRequiresExactlyOneOfWorkRequestIDAndPath(t *testing.T) {
	both := "/20160918/workRequests/" + goodWR
	cases := []struct {
		name string
		req  pollRequest
	}{
		{"neither", pollRequest{Binding: pollBinding(pollVersion)}},
		{"both, equal", pollRequest{Binding: pollBinding(pollVersion), WorkRequestID: goodWR, Path: both}},
		{"both, different", pollRequest{Binding: pollBinding(pollVersion), WorkRequestID: "ocid1.wr..b", Path: both}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := newPollMock()
			wantPollRefusal(t, callPoll(t, mock, c.req), mock, "work_request_id")
		})
	}
}

// With a work_request_id the module builds the path from binding.base_path, so that
// must be a plain API version ("/" and 8 digits), never caller-shaped text.
func TestPollRefusesAWorkRequestIDWithoutAUsableBasePath(t *testing.T) {
	for _, base := range []string{"", "20160918", "/20160918/", "/20160918/../x", "/v1", "/20160918?x=1", "/2016091", "//20160918", "/20160918/x"} {
		t.Run("base_path="+base, func(t *testing.T) {
			mock := newPollMock()
			res := callPoll(t, mock, pollRequest{Binding: pollBinding(base), WorkRequestID: goodWR})
			wantPollRefusal(t, res, mock, "binding.base_path")
		})
	}
}

func TestPollKeepsTheExistingHostAndKeyRequirement(t *testing.T) {
	for _, b := range []execBinding{{KeyID: "k", BasePath: pollVersion}, {Host: bindingHost, BasePath: pollVersion}} {
		mock := newPollMock()
		res := callPoll(t, mock, pollRequest{Binding: b, WorkRequestID: goodWR})
		wantPollRefusal(t, res, mock, "")
	}
}

// Both inputs end at the same canonical request: a signed GET to
// https://<host><version>/workRequests/<id>, with no body, whatever type segment the
// id carries (no allow-list: OCI documents several Work Request types).
func TestPollSendsTheCanonicalGETForEveryAcceptedInput(t *testing.T) {
	ids := []string{
		goodWR,
		"ocid1.workrequest.oc1..xyz",
		"ocid1.coreservicesworkrequest.oc1.eu-frankfurt-1.amaaaaaaexample",
		"ocid1.clustersworkrequest.oc1..a",
	}
	for _, id := range ids {
		want := "https://" + bindingHost + pollVersion + "/workRequests/" + id
		inputs := []struct {
			name string
			req  pollRequest
		}{
			{"work_request_id", pollRequest{Binding: pollBinding(pollVersion), WorkRequestID: id}},
			{"path alias with base_path", pollRequest{Binding: pollBinding(pollVersion), Path: pollVersion + "/workRequests/" + id}},
			{"path alias without base_path", pollRequest{Binding: pollBinding(""), Path: pollVersion + "/workRequests/" + id}},
		}
		for _, in := range inputs {
			t.Run(id+"/"+in.name, func(t *testing.T) {
				mock := newPollMock()
				res := callPoll(t, mock, in.req)
				if res.Status != "ok" {
					t.Fatalf("status = %q, want ok (error: %+v)", res.Status, res.Error)
				}
				if mock.signCount() != 1 || mock.actuateCount() != 1 {
					t.Fatalf("%d sign / %d actuate calls, want exactly 1 each", mock.signCount(), mock.actuateCount())
				}
				call := mock.lastActuate(t)
				if call.Method != "GET" {
					t.Errorf("method = %q, want GET", call.Method)
				}
				if call.URL != want {
					t.Errorf("URL = %q, want %q", call.URL, want)
				}
				if len(call.Body) != 0 {
					t.Errorf("a poll sends no body, got %d bytes", len(call.Body))
				}
				var pr pollResult
				if err := json.Unmarshal(res.Result, &pr); err != nil {
					t.Fatalf("result: %v", err)
				}
				if pr.WorkStatus != "SUCCEEDED" || pr.PercentComplete != 100 || !pr.Terminal || pr.HTTPStatus != 200 {
					t.Errorf("poll result = %+v, want SUCCEEDED/100/terminal/200 unchanged", pr)
				}
			})
		}
	}
}

func TestPollRefusalDoesNotCarryTheRawInput(t *testing.T) {
	long := pollVersion + "/workRequests/ocid1.wr..a\n" + strings.Repeat("x", 500)
	for _, req := range []pollRequest{
		{Binding: pollBinding(pollVersion), Path: long},
		{Binding: pollBinding(pollVersion), WorkRequestID: "ocid1.wr..a\n" + strings.Repeat("x", 500)},
	} {
		mock := newPollMock()
		res := callPoll(t, mock, req)
		if res.Error == nil {
			t.Fatal("want a refusal")
		}
		if strings.Contains(res.Error.Message, "\n") || len(res.Error.Message) > 400 {
			t.Errorf("the refusal must stay one short line, got %d bytes: %q", len(res.Error.Message), res.Error.Message)
		}
	}
}

// An empty kind means "any type segment": the same shape rule, no type pinned.
func TestAnyTypeOCIDRuleKeepsTheShapeRule(t *testing.T) {
	for _, id := range []string{"ocid1.wr..a", "ocid1.vcn.oc1..z", "ocid1.coreservicesworkrequest.oc1.eu-frankfurt-1.amaaa"} {
		if !wellFormedOCID("", id) {
			t.Errorf("%q must be accepted for any type", id)
		}
	}
	for _, id := range []string{"", "ocid1", "ocid1..oc1..a", "ocid1.wr.", "ocid1.wr..", "ocid2.wr..a", "ocid1.w r..a", "ocid1.wr..a/b"} {
		if wellFormedOCID("", id) {
			t.Errorf("%q must be refused whatever the type", id)
		}
	}
}
