// Package main — observed.go turns the outcome of a read (GetVcn and its
// siblings) into an observed state that says, per field, how much of it was
// actually observed: observed | absent | unobserved | unknown.
//
// It is a PURE function (no network, no signing, no host calls, no clock). It
// reuses effectiveConfig and applyDerivations; what it adds is the coverage
// classification a conformance check needs so that "nothing was seen" is never
// mistaken for "nothing is there":
//
//	observed    the field is in the answer and has the type the contract says
//	absent      OCI omitted the field and, for this field, omission means "not
//	            set" (declared per kind in unsetWhenOmitted — hand-authored, like
//	            the correspondences; the default for an omitted field is unknown)
//	unobserved  the value could not be obtained: the request got no answer, or
//	            the field is input-only and is not returned by the read, or its
//	            derivation source is missing (a partial read never fabricates a
//	            value: no ipv6CidrBlocks means isIpv6Enabled is unobserved, not false)
//	unknown     something was read but cannot be trusted: a wrong type, a
//	            non-object body, or an HTTP error — in particular 404
//	            NotAuthorizedOrNotFound, which OCI gives for a deleted resource and
//	            for one the caller may not see, so it is NOT authoritative absence
//
// Status (three-level): implemented and unit-tested against the common VCN
// fixture (testdata/vcn-v1); NOT wired into Observe — Observe's result shape and
// its error-result handling of HTTP errors stay exactly as they were
// (provider-abi.md). Wiring it is a separate, deliberate ABI step.
package main

import (
	"bytes"
	"encoding/json"
	"sort"
)

type coverageState string

const (
	covObserved   coverageState = "observed"
	covAbsent     coverageState = "absent"
	covUnobserved coverageState = "unobserved"
	covUnknown    coverageState = "unknown"
)

// unsetWhenOmitted lists, per kind, the config fields for which OCI leaves the
// property out of the answer when it is not set. Hand-authored and reviewed as
// code. Everything not listed is not claimed absent when omitted: it is unknown.
var unsetWhenOmitted = map[string]map[string]bool{
	"cic:network:vcn": {"dnsLabel": true},
}

// getOutcome is what a read produced: an HTTP answer, or no answer at all.
type getOutcome struct {
	Status    int    // HTTP status; ignored when Transport is set
	Body      []byte // the response body as received
	Transport string // non-empty: the request did not complete (timeout, reset, ...)
}

// normalizedObservation is the read-only observed state with its coverage.
// Coverage keys are config-surface paths ("/cidrBlocks"), one per contract
// config field.
type normalizedObservation struct {
	EffectiveConfig map[string]json.RawMessage `json:"effective_config"`
	State           map[string]json.RawMessage `json:"state"`
	Coverage        map[string]coverageState   `json:"coverage"`
}

// normalizeObserved classifies a read outcome for a kind. It returns false only
// when the module has no contract for the kind.
func normalizeObserved(kind string, o getOutcome) (normalizedObservation, bool) {
	c, ok := resourceContracts()[kind]
	if !ok {
		return normalizedObservation{}, false
	}
	out := normalizedObservation{
		EffectiveConfig: map[string]json.RawMessage{},
		State:           map[string]json.RawMessage{},
		Coverage:        map[string]coverageState{},
	}
	all := func(s coverageState) normalizedObservation {
		for name := range c.fields {
			out.Coverage["/"+name] = s
		}
		return out
	}
	if o.Transport != "" {
		return all(covUnobserved), true // no answer: nothing was observed
	}
	if o.Status < 200 || o.Status > 299 {
		return all(covUnknown), true // an error answer says nothing authoritative about the fields
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(o.Body, &state); err != nil || state == nil {
		return all(covUnknown), true // read, but not an object: untrustworthy
	}
	out.State = state
	eff := effectiveConfig(c, state)
	applyDerivations(kind, state, eff)
	out.EffectiveConfig = eff

	derived := map[string]string{} // config field -> the state field it is derived from
	for _, d := range correspondences()[kind] {
		derived[d.Field] = d.From
	}
	for name, f := range c.fields {
		path := "/" + name
		if from, isDerived := derived[name]; isDerived {
			// realized from state by applyDerivations, which only sets it when the source is there
			if _, has := state[from]; has {
				out.Coverage[path] = covObserved
			} else {
				out.Coverage[path] = covUnobserved
			}
			continue
		}
		if f.policy == "input-only" {
			out.Coverage[path] = covUnobserved // not returned by the read: it cannot be observed
			continue
		}
		raw, present := state[name]
		switch {
		case !present || isJSONNull(raw):
			if unsetWhenOmitted[kind][name] {
				out.Coverage[path] = covAbsent
			} else {
				out.Coverage[path] = covUnknown
			}
		case jsonTypeMatches(f.jsonType, raw):
			out.Coverage[path] = covObserved
		default:
			out.Coverage[path] = covUnknown
		}
	}
	// a value that is not observed does not stay in effective_config: what could not be trusted is not compared
	for name := range c.fields {
		if out.Coverage["/"+name] != covObserved {
			delete(out.EffectiveConfig, name)
		}
	}
	return out, true
}

func isJSONNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

// jsonTypeMatches checks the coarse JSON type of a value against the contract's.
// An unmapped type ("") matches anything.
func jsonTypeMatches(want string, raw json.RawMessage) bool {
	s := bytes.TrimSpace(raw)
	if len(s) == 0 {
		return false
	}
	switch want {
	case "":
		return true
	case "string":
		return s[0] == '"'
	case "array":
		return s[0] == '['
	case "object":
		return s[0] == '{'
	case "boolean":
		return string(s) == "true" || string(s) == "false"
	case "integer", "number":
		return s[0] == '-' || (s[0] >= '0' && s[0] <= '9')
	}
	return true
}

// coveragePaths lists the coverage paths in stable order (for tests and reports).
func (n normalizedObservation) coveragePaths() []string {
	ps := make([]string, 0, len(n.Coverage))
	for p := range n.Coverage {
		ps = append(ps, p)
	}
	sort.Strings(ps)
	return ps
}
