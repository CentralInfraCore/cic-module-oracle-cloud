//go:build !wasip1

package main

// ocid_boundary_test.go guards the trust boundary
//
//	OCI response -> steps[].resource_id -> (the caller hands it back as
//	binding.resource_id) -> templatePath -> a later SIGNED request path
//
// from both ends:
//
//   - createdResourceID accepts only a well-formed OCID of the expected type, and
//     reads the response as ONE unambiguous JSON object: a repeated "id" (even
//     with two valid string values), an "id" next to an "ID", or a missing, null,
//     empty or non-string id all fail the step;
//   - templatePath terminates on every input and behaves exactly as before for
//     ids that contain no "{".
//
// The Execute-level assertions are on the OUTCOME only (status, the absence of a
// resource_id, the CIC class, one sign call and one provider request). They do
// not match message text.

import (
	"strings"
	"testing"
	"time"
)

// assertCreateRefused runs a create whose 2xx answer is body and checks that the
// step failed without ever reporting a resource_id or retrying.
func assertCreateRefused(t *testing.T, body string) {
	t.Helper()
	mock := &cicFlowMock{Reply: replyWith(200, body, map[string]string{"opc-request-id": "req-b"})}
	_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)

	if er.Status != "failed" {
		t.Fatalf("status = %q, want failed for body %q: %+v", er.Status, body, er.Steps)
	}
	if len(er.Steps) != 1 {
		t.Fatalf("got %d steps, want 1", len(er.Steps))
	}
	s := er.Steps[0]
	if s.ResourceID != "" {
		t.Errorf("steps[0].resource_id = %q for body %q, want empty", s.ResourceID, body)
	}
	if s.ErrorClass != classProvider {
		t.Errorf("steps[0].error_class = %q, want %q", s.ErrorClass, classProvider)
	}
	if s.HTTPStatus != 200 || s.OpcRequestID != "req-b" {
		t.Errorf("steps[0] = %+v, want the 2xx evidence preserved", s)
	}
	if n := mock.actuateCount(); n != 1 {
		t.Errorf("%d provider requests for body %q, want exactly 1 (no retry)", n, body)
	}
	if n := mock.signCount(); n != 1 {
		t.Errorf("%d sign calls for body %q, want exactly 1", n, body)
	}
}

// TestCreatedResourceIDAcceptsOnlyWellFormedOCIDs is the format table for the
// validator itself.
func TestCreatedResourceIDAcceptsOnlyWellFormedOCIDs(t *testing.T) {
	const kind = "cic:network:vcn"
	accept := []string{
		"ocid1.vcn..z",       // short fixture form used elsewhere in this repo
		"ocid1.vcn.oc1..xyz", // realm fixture form
		"ocid1.vcn.oc1.eu-frankfurt-1.amaaaaaaexamplevcn",
		"ocid1.vcn.oc1.eu-frankfurt-1.future.amaaaaaaexamplevcn", // optional "future use" segment
		"ocid1.vcn.oc2.iad.AbC_123-xyz",
	}
	for _, id := range accept {
		got, pe := createdResourceID(kind, []byte(`{"id":"`+id+`"}`), 200, "")
		if pe != nil || got != id {
			t.Errorf("createdResourceID(%q) = %q, %v; want it accepted", id, got, pe)
		}
	}

	reject := []struct{ name, id string }{
		{"path traversal", "ocid1.vcn.oc1..a/../../subnets/x"},
		{"query and fragment", "ocid1.vcn.oc1..a?x=1#f"},
		{"space inside", "ocid1.vcn.oc1..a b"},
		{"only a space after the prefix", "ocid1.vcn.oc1.. "},
		{"newline inside", "ocid1.vcn.oc1..a\\nb"},
		{"brace placeholder", "ocid1.vcn.oc1..{a}"},
		{"opening brace only", "ocid1.vcn.oc1..a{"},
		{"percent escape", "ocid1.vcn.oc1..a%2fb"},
		{"non-ASCII (U+00E9)", "ocid1.vcn.oc1.." + string(rune(0xe9))},
		{"leading space", " ocid1.vcn.oc1..a"},
		{"trailing space", "ocid1.vcn.oc1..a "},
		{"another type", "ocid1.subnet.oc1..a"},
		{"type in the wrong case", "ocid1.VCN.oc1..a"},
		{"wrong first segment", "ocid2.vcn.oc1..a"},
		{"too few segments", "ocid1.vcn.z"},
		{"too many segments", "ocid1.vcn.oc1.iad.f1.f2.zzz"},
		{"empty unique id", "ocid1.vcn.oc1.iad."},
		{"bare prefix", "ocid1.vcn."},
		{"no prefix at all", "vcn-42"},
		{"too long", "ocid1.vcn.oc1.." + strings.Repeat("a", 300)},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			// The id is embedded through a JSON string so control characters stay legal JSON.
			body := `{"id":"` + tc.id + `"}`
			if got, pe := createdResourceID(kind, []byte(body), 200, ""); pe == nil {
				t.Errorf("createdResourceID accepted %q as %q; want it refused", tc.id, got)
			}
		})
	}
}

// TestExecuteCreateRefusesUnsafeIDs: the same refusals, as seen by a caller of
// Execute. The "{a}" row is the one that used to hang the later templatePath.
func TestExecuteCreateRefusesUnsafeIDs(t *testing.T) {
	for _, id := range []string{
		"ocid1.vcn.oc1..a/../../subnets/x",
		"ocid1.vcn.oc1..a?x=1#f",
		"ocid1.vcn.oc1..a b",
		"ocid1.vcn.oc1..{a}",
		"ocid1.vcn.oc1..a%2fb",
	} {
		t.Run(id, func(t *testing.T) { assertCreateRefused(t, `{"id":"`+id+`"}`) })
	}
}

// TestExecuteCreateRefusesDuplicateAndAmbiguousIDs: an id must never be decided
// by "last key wins". Every body here is a 2xx whose id cannot be read
// unambiguously.
func TestExecuteCreateRefusesDuplicateAndAmbiguousIDs(t *testing.T) {
	cases := []struct{ name, body string }{
		{"two different valid ids", `{"id":"ocid1.vcn.oc1..a","id":"ocid1.vcn.oc1..b"}`},
		{"the same valid id twice", `{"id":"ocid1.vcn.oc1..a","id":"ocid1.vcn.oc1..a"}`},
		{"id and ID together", `{"id":"ocid1.vcn.oc1..a","ID":"ocid1.vcn.oc1..b"}`},
		{"ID before id", `{"ID":"ocid1.vcn.oc1..a","id":"ocid1.vcn.oc1..a"}`},
		{"id and Id together", `{"id":"ocid1.vcn.oc1..a","Id":"ocid1.vcn.oc1..a"}`},
		{"only an upper-case ID", `{"ID":"ocid1.vcn.oc1..a"}`},
		{"valid id then null duplicate", `{"id":"ocid1.vcn.oc1..a","id":null}`},
		{"null id", `{"id":null}`},
		{"empty id", `{"id":""}`},
		{"numeric id", `{"id":12345}`},
		{"boolean id", `{"id":true}`},
		{"array id", `{"id":["ocid1.vcn.oc1..a"]}`},
		{"object id", `{"id":{"v":"ocid1.vcn.oc1..a"}}`},
		{"no id member", `{"displayName":"prod"}`},
		{"two concatenated objects", `{"id":"ocid1.vcn.oc1..a"}{"id":"ocid1.vcn.oc1..b"}`},
		{"data after the object", `{"id":"ocid1.vcn.oc1..a"} trailing`},
		{"not an object", `["ocid1.vcn.oc1..a"]`},
		{"not JSON", `<html>502</html>`},
		{"empty body", ``},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { assertCreateRefused(t, tc.body) })
	}
}

// TestExecuteCreateStillAcceptsAWellFormedAnswer holds the line from the other
// side: strictness must not reject what OCI really sends.
func TestExecuteCreateStillAcceptsAWellFormedAnswer(t *testing.T) {
	const want = "ocid1.vcn.oc1.eu-frankfurt-1.amaaaaaaexamplevcn"
	cases := []struct{ name, body string }{
		{"typical answer", `{"id":"` + want + `","displayName":"prod","lifecycleState":"PROVISIONING"}`},
		{"surrounding whitespace", "  \n{\"id\":\"" + want + "\"}\n  "},
		{"an id member nested elsewhere is not the id", `{"id":"` + want + `","freeformTags":{"id":5,"id":6}}`},
		{"an unrelated key repeated", `{"displayName":"a","id":"` + want + `","displayName":"b"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(200, tc.body, nil)}
			_, er := execVCN(t, mock, createVcnOp, createBinding, `{"compartmentId":"ocid1.compartment..c"}`)
			if er.Status != "succeeded" || len(er.Steps) != 1 || er.Steps[0].ResourceID != want {
				t.Fatalf("got status %q steps %+v, want succeeded with resource_id %q", er.Status, er.Steps, want)
			}
		})
	}
}

// templatePathWithin fails the test if templatePath does not return in time.
func templatePathWithin(t *testing.T, path, id string) string {
	t.Helper()
	ch := make(chan string, 1)
	go func() { ch <- templatePath(path, id) }()
	select {
	case s := <-ch:
		return s
	case <-time.After(2 * time.Second):
		t.Fatalf("templatePath(%q, %q) did not terminate within 2s", path, id)
		return ""
	}
}

// TestTemplatePathTerminatesOnAnyID: an id that itself contains "{...}" used to
// send templatePath into an endless re-scan of its own output. Whatever the id
// holds, the substitution is a single finite pass and the id is inserted
// literally.
func TestTemplatePathTerminatesOnAnyID(t *testing.T) {
	ids := []string{
		"ocid1.vcn.oc1..{a}", "{vcnId}", "{", "}", "{}", "{{}}", "a{b}c{d}e",
		strings.Repeat("{x}", 1000),
	}
	for _, id := range ids {
		if got, want := templatePathWithin(t, "/vcns/{vcnId}", id), "/vcns/"+id; got != want {
			t.Errorf("templatePath(%q) = %q, want %q", id, got, want)
		}
		// Two placeholders: each gets the id once, the inserted text is never re-read.
		if got, want := templatePathWithin(t, "/a/{x}/b/{y}", id), "/a/"+id+"/b/"+id; got != want {
			t.Errorf("two-placeholder templatePath(%q) = %q, want %q", id, got, want)
		}
	}
}

// referenceTemplatePath is the pre-fix algorithm, kept ONLY as a differential
// oracle, with an iteration cap so it cannot hang the test. ok is false if the
// cap was hit (the old endless-loop case).
func referenceTemplatePath(path, resourceID string) (string, bool) {
	for n := 0; n < 10000; n++ {
		i := strings.IndexByte(path, '{')
		if i < 0 {
			return path, true
		}
		j := strings.IndexByte(path[i:], '}')
		if j < 0 {
			return path, true
		}
		path = path[:i] + resourceID + path[i+j+1:]
	}
	return "", false
}

// TestTemplatePathUnchangedForIDsWithoutAnOpeningBrace is the no-silent-refactor
// guard: for every id without "{" the fixed templatePath returns exactly what the
// old algorithm returned, over every path shape and odd id below.
func TestTemplatePathUnchangedForIDsWithoutAnOpeningBrace(t *testing.T) {
	paths := []string{
		"/vcns/{vcnId}", "/subnets/{subnetId}/actions/x", "/instances/{instanceId}",
		"/{a}/{b}", "/a{b}c{d}e", "/plain", "", "/unmatched{x", "/closed}only", "{}", "/{}{}",
	}
	ids := []string{
		"ocid1.vcn.oc1..x", "", "a}b", "}", "/../..", "?q=1#f", "a b", "%7Bx%7D", "ocid1.subnet.oc1.iad.zzz",
	}
	n := 0
	for _, p := range paths {
		for _, id := range ids {
			want, ok := referenceTemplatePath(p, id)
			if !ok {
				t.Fatalf("reference algorithm did not finish for (%q, %q): corpus must stay brace-free", p, id)
			}
			if got := templatePathWithin(t, p, id); got != want {
				t.Errorf("templatePath(%q, %q) = %q, the old algorithm gave %q", p, id, got, want)
			}
			n++
		}
	}
	if n < 90 {
		t.Fatalf("corpus shrank to %d combinations", n)
	}
}

// TestTemplatePathOldAlgorithmReallyHangs documents WHY the guard exists: the
// reference (old) algorithm hits its cap on a brace-containing id.
func TestTemplatePathOldAlgorithmReallyHangs(t *testing.T) {
	if _, ok := referenceTemplatePath("/vcns/{vcnId}", "ocid1.vcn.oc1..{a}"); ok {
		t.Fatalf("the reference algorithm finished on a brace-containing id; the oracle no longer reproduces the defect")
	}
}
