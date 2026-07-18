package ociextract

// diff.go compares two generated payload-schema bundles and classifies the
// changes as breaking or compatible (P2.4 breaking-change gate). On an SDK bump,
// re-generating the schema and diffing it against the pinned one turns OCI's
// minor-version breakage from a silent runtime failure into a caught,
// reviewable build signal.
//
// Breaking (an existing valid intent could stop validating or change meaning):
//   - a config field removed
//   - a field that became required
//   - a new field that is required
//   - a field's type changed
//
// Compatible:
//   - a new optional field
//   - a field's CIC policy changed (notable, but does not invalidate intents)

import (
	"encoding/json"
	"sort"
)

// Change is one classified schema difference.
type Change struct {
	Field  string `json:"field"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// SchemaDiff is the classified difference between two config schemas.
type SchemaDiff struct {
	Breaking   []Change `json:"breaking"`
	Compatible []Change `json:"compatible"`
}

// Breaking change kinds.
const (
	ChangeRemoved     = "removed"
	ChangeNowRequired = "now-required"
	ChangeAddedReq    = "added-required"
	ChangeTypeChanged = "type-changed"
	ChangeAdded       = "added"
	ChangePolicy      = "policy-changed"
)

type configView struct {
	required map[string]bool
	fields   map[string]struct {
		Type   string `json:"type"`
		Policy string `json:"x-cic-policy"`
	}
}

func parseConfigView(bundle []byte) (configView, error) {
	var b struct {
		Config struct {
			Required   []string `json:"required"`
			Properties map[string]struct {
				Type   string `json:"type"`
				Policy string `json:"x-cic-policy"`
			} `json:"properties"`
		} `json:"config"`
	}
	if err := json.Unmarshal(bundle, &b); err != nil {
		return configView{}, err
	}
	v := configView{required: map[string]bool{}, fields: b.Config.Properties}
	for _, r := range b.Config.Required {
		v.required[r] = true
	}
	return v, nil
}

// DiffConfigSchemas classifies the changes from oldBundle to newBundle (each a
// {config, state} JSON bundle as emitted by ResourceSchemas). Output slices are
// sorted by field for stable, reviewable results.
func DiffConfigSchemas(oldBundle, newBundle []byte) (SchemaDiff, error) {
	oldV, err := parseConfigView(oldBundle)
	if err != nil {
		return SchemaDiff{}, err
	}
	newV, err := parseConfigView(newBundle)
	if err != nil {
		return SchemaDiff{}, err
	}

	var d SchemaDiff
	// Removed and changed fields.
	for name, of := range oldV.fields {
		nf, ok := newV.fields[name]
		if !ok {
			d.Breaking = append(d.Breaking, Change{Field: name, Kind: ChangeRemoved})
			continue
		}
		if of.Type != nf.Type {
			d.Breaking = append(d.Breaking, Change{Field: name, Kind: ChangeTypeChanged, Detail: of.Type + " -> " + nf.Type})
		}
		if newV.required[name] && !oldV.required[name] {
			d.Breaking = append(d.Breaking, Change{Field: name, Kind: ChangeNowRequired})
		}
		if of.Policy != nf.Policy {
			d.Compatible = append(d.Compatible, Change{Field: name, Kind: ChangePolicy, Detail: of.Policy + " -> " + nf.Policy})
		}
	}
	// Added fields.
	for name := range newV.fields {
		if _, ok := oldV.fields[name]; ok {
			continue
		}
		if newV.required[name] {
			d.Breaking = append(d.Breaking, Change{Field: name, Kind: ChangeAddedReq})
		} else {
			d.Compatible = append(d.Compatible, Change{Field: name, Kind: ChangeAdded})
		}
	}

	sortChanges(d.Breaking)
	sortChanges(d.Compatible)
	return d, nil
}

func sortChanges(cs []Change) {
	sort.Slice(cs, func(i, j int) bool {
		if cs[i].Field != cs[j].Field {
			return cs[i].Field < cs[j].Field
		}
		return cs[i].Kind < cs[j].Kind
	})
}
