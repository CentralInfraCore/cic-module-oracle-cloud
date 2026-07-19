// Package main — correspondence.go carries the semantic correspondences that the
// SDK tags cannot express: how an input-only config field is realized as a
// different output-only state field (state-model.md, "effective_config"). These
// are hand-authored per kind (reviewed as code), separate from the generated
// schema so `make oci.generate` stays idempotent. observe applies them to
// complete effective_config so it is directly comparable to intent.
//
// Scope (honest): this covers derivations expressible from the read state alone
// (a boolean from a list's presence, a boolean inversion, a rename). It does NOT
// cover name↔ID resolution (intent name → observed OCID), which needs a further
// provider lookup — that stays out until a resolve step exists.
package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed correspondence/vcn.json
var vcnCorrespondence []byte

var embeddedCorrespondences = [][]byte{vcnCorrespondence}

// derivation fills a config field from a state field by a rule.
type derivation struct {
	Field string `json:"field"`
	From  string `json:"from"`
	Rule  string `json:"rule"` // bool-from-nonempty | bool-invert | rename
}

var (
	corrCache map[string][]derivation
	corrDone  bool
)

// correspondences lazily parses the embedded correspondence contracts (not init():
// the host runs no _start; //go:embed data is static).
func correspondences() map[string][]derivation {
	if corrDone {
		return corrCache
	}
	corrDone = true
	corrCache = map[string][]derivation{}
	for _, raw := range embeddedCorrespondences {
		var c struct {
			Kind        string       `json:"kind"`
			Derivations []derivation `json:"derivations"`
		}
		if err := json.Unmarshal(raw, &c); err != nil || c.Kind == "" {
			continue
		}
		corrCache[c.Kind] = c.Derivations
	}
	return corrCache
}

// applyDerivations completes effective_config with the input-only fields derived
// from state (in place). It only sets a field when the source state field is
// present, so a partial read never fabricates a value.
func applyDerivations(kind string, state, eff map[string]json.RawMessage) {
	for _, d := range correspondences()[kind] {
		sv, ok := state[d.From]
		if !ok {
			continue
		}
		switch d.Rule {
		case "bool-from-nonempty":
			eff[d.Field] = boolJSON(jsonNonEmpty(sv))
		case "bool-invert":
			eff[d.Field] = boolJSON(!jsonTrue(sv))
		case "rename":
			eff[d.Field] = sv
		}
	}
}

// jsonNonEmpty reports whether a JSON value is a non-empty array/string/object
// (i.e. not null, not [], not "", not {}).
func jsonNonEmpty(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	switch s {
	case "", "null", "[]", `""`, "{}", "0", "false":
		return false
	}
	return true
}

func jsonTrue(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "true"
}

func boolJSON(b bool) json.RawMessage {
	if b {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}
