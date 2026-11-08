//go:build !wasip1

package main

// A create is planned from the desired config alone, so Plan must not produce a plan,
// and Execute must not run one, for a config the kind's contract does not accept: a
// missing required field, a field the caller cannot set, a gross type error. These
// tests pin that (review of PR #43, F1). An update diffs against an existing resource
// and is not held to the "required" rule; Validate is where a partial intent is
// reviewed.

import (
	"encoding/json"
	"strings"
	"testing"
)

type createCase struct {
	name, kind, desired, field, class string
}

func createCases() []createCase {
	return []createCase{
		{"vcn: required compartmentId missing", "cic:network:vcn", `{"displayName":"x"}`, "desired.data.compartmentId", classValidation},
		{"vcn: empty config", "cic:network:vcn", `{}`, "desired.data.compartmentId", classValidation},
		{"subnet: required vcnId missing", "cic:network:subnet", `{"compartmentId":"ocid1.compartment..c"}`, "desired.data.vcnId", classValidation},
		{"instance: required availabilityDomain missing", "cic:compute:instance", `{"compartmentId":"ocid1.compartment..c"}`, "desired.data.availabilityDomain", classValidation},
		{"vcn: a field the caller cannot set", "cic:network:vcn", `{"compartmentId":"ocid1.compartment..c","lifecycleState":"AVAILABLE"}`, "desired.data.lifecycleState", classSchema},
		{"vcn: an unknown field", "cic:network:vcn", `{"compartmentId":"ocid1.compartment..c","bogus":1}`, "desired.data.bogus", classSchema},
		{"vcn: a string where an array is wanted", "cic:network:vcn", `{"compartmentId":"ocid1.compartment..c","cidrBlocks":"10.0.0.0/16"}`, "desired.data.cidrBlocks", classSchema},
		{"vcn: a number where a string is wanted", "cic:network:vcn", `{"compartmentId":"ocid1.compartment..c","displayName":5}`, "desired.data.displayName", classSchema},
	}
}

func TestPlanRefusesACreateThatTheContractDoesNotAccept(t *testing.T) {
	for _, c := range createCases() {
		t.Run(c.name, func(t *testing.T) {
			raw, _ := json.Marshal(planRequest{Kind: c.kind,
				Desired:  m4aPayload(c.kind+"-config", c.desired),
				Observed: m4aPayload(c.kind+"-state", `{}`),
				Target:   bindingWith("")})
			out, err := Plan(nil, raw)
			if err != nil {
				t.Fatalf("Plan returned a Go error: %v", err)
			}
			res := decodeResult(t, out)
			if res.Status != "error" || res.Error == nil {
				t.Fatalf("status %q, want an error: a plan was produced for a config the contract refuses", res.Status)
			}
			if res.Error.Class != c.class || res.Error.FieldPath != c.field {
				t.Errorf("class %q field_path %q, want %q %q", res.Error.Class, res.Error.FieldPath, c.class, c.field)
			}
		})
	}
}

// A create request is changed by hand: the desired config (and the config, so the two
// still agree) loses a required field, or gains a field no caller may set, while the
// plan and digests stay as Plan made them. The gate refuses it before anything is
// signed or sent.
func TestExecuteRefusesAHandEditedCreateConfig(t *testing.T) {
	good := `{"compartmentId":"ocid1.compartment..c","displayName":"x"}`
	edits := []struct{ name, desired, field, class string }{
		{"required field removed", `{"displayName":"x"}`, "desired.data.compartmentId", classValidation},
		{"a field the caller cannot set added", `{"compartmentId":"ocid1.compartment..c","displayName":"x","lifecycleState":"AVAILABLE"}`, "desired.data.lifecycleState", classSchema},
		{"a wrong type", `{"compartmentId":"ocid1.compartment..c","displayName":7}`, "desired.data.displayName", classSchema},
	}
	refused := func(t *testing.T, res providerResult, mock *cicFlowMock, field, class string) {
		t.Helper()
		if res.Status != "error" || res.Error == nil {
			t.Fatalf("status %q, want an error: the request was run", res.Status)
		}
		if res.Error.Class != class || res.Error.FieldPath != field {
			t.Errorf("class %q field_path %q, want %q %q", res.Error.Class, res.Error.FieldPath, class, field)
		}
		if mock.signCount() != 0 || mock.actuateCount() != 0 {
			t.Errorf("%d sign / %d actuate calls, want none", mock.signCount(), mock.actuateCount())
		}
	}
	for _, e := range edits {
		t.Run(e.name, func(t *testing.T) {
			req := m4aForOp(t, bindingKind, providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns"}, good, bindingWith(""))
			req.Desired.Data = json.RawMessage(e.desired)
			req.Config.Data = json.RawMessage(e.desired)
			res, mock := runExecute(t, req)
			refused(t, res, mock, e.field, e.class)
		})
	}
	t.Run("as request text", func(t *testing.T) {
		req := m4aForOp(t, bindingKind, providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns"}, good, bindingWith(""))
		text := string(marshalReq(t, req))
		from := `"data":{"compartmentId":"ocid1.compartment..c","displayName":"x"}`
		if strings.Count(text, from) != 2 {
			t.Fatalf("test setup: want desired and config as %q in the text:\n%s", from, text)
		}
		res, mock := runExecuteJSON(t, []byte(strings.ReplaceAll(text, from, `"data":{"displayName":"x"}`)))
		refused(t, res, mock, "desired.data.compartmentId", classValidation)
	})
}

// The refusal does not touch the other lifecycles: an update is not held to the
// "required" rule, and a valid create still plans and runs.
func TestAnUpdateIsNotHeldToTheRequiredRuleAndAValidCreateStillRuns(t *testing.T) {
	up := m4aPlan(t, bindingKind, `{"displayName":"new"}`, `{"displayName":"old"}`, bindingWith("ocid1.vcn.oc1..a"))
	if up.Operation != "update" {
		t.Fatalf("an update with only displayName must still plan, got %q", up.Operation)
	}
	req := m4aForOp(t, bindingKind, providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns"},
		`{"compartmentId":"ocid1.compartment..c","displayName":"x"}`, bindingWith(""))
	res, mock := runExecute(t, req)
	if res.Status != "ok" || mock.actuateCount() != 1 {
		t.Fatalf("status %q with %d requests (%+v), want ok with 1", res.Status, mock.actuateCount(), res.Error)
	}
}

// Several problems at once: the refusal is the same on every run (the first by field
// path), never whichever the map walk happens to meet first.
func TestTheRefusalOfACreateWithSeveralProblemsIsDeterministic(t *testing.T) {
	desired := `{"zzz":1,"aaa":1,"mmm":1,"displayName":3}` // compartmentId missing as well
	for i := 0; i < 40; i++ {
		raw, _ := json.Marshal(planRequest{Kind: bindingKind,
			Desired:  m4aPayload("d", desired),
			Observed: m4aPayload("o", `{}`),
			Target:   bindingWith("")})
		out, _ := Plan(nil, raw)
		res := decodeResult(t, out)
		if res.Error == nil || res.Error.FieldPath != "desired.data.aaa" {
			t.Fatalf("run %d: refusal %+v, want field_path desired.data.aaa (the first by path)", i, res.Error)
		}
	}
}
