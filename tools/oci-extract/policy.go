package ociextract

// policy.go derives a resource's field policy (P2.3) — the core semantic step of
// turning OCI's Create/Update/Read model split into a CIC provider contract. It
// classifies each field as mutable, create-only, action-managed, or output-only.
//
// The crucial rule (oci-schema-pipeline.md, P2.3): a field absent from the
// Update model is NOT automatically immutable. Many OCI fields are mutable only
// through a dedicated action (Change*/Add*/Remove*Details), e.g. a VCN's
// compartmentId via ChangeVcnCompartmentDetails. So classification consults the
// action models before ever calling a field create-only.

import (
	"regexp"
	"sort"
	"strings"
)

// Field policy classes.
const (
	PolicyMutable    = "mutable"     // in Update — freely changeable
	PolicyAction     = "action"      // changeable only via a named action model
	PolicyCreateOnly = "create-only" // set at create, then immutable
	PolicyInputOnly  = "input-only"  // accepted at create but never read back
	PolicyOutputOnly = "output-only" // provider-computed, read-only
	PolicyUnknown    = "unknown"
)

// FieldPolicy is one field's classification across the Create/Update/Read models.
type FieldPolicy struct {
	Field    string `json:"field"` // json name
	InCreate bool   `json:"in_create"`
	InUpdate bool   `json:"in_update"`
	InRead   bool   `json:"in_read"`
	Policy   string `json:"policy"`
	Action   string `json:"action,omitempty"` // action model that can change it, if Policy==action
}

// DeriveFieldPolicy classifies each field of a resource by comparing its Create,
// Update, and Read models, consulting actionModels before calling a field
// immutable. Any of the three models may be a zero Model (a resource with no
// Update model, say); action models are matched by json field name. Output is
// sorted by field name for stable, hashable results.
func DeriveFieldPolicy(create, update, read Model, actionModels []Model) []FieldPolicy {
	inC := jsonNames(create)
	inU := jsonNames(update)
	inR := jsonNames(read)

	// field json name -> the first action model that carries it. Only
	// Change*Details models are currently auto-bound as field setters.
	// Add*/Remove*Details models are conservatively treated as standalone
	// operations instead: a matching json field name does not prove
	// convergence (that the resource field ends up equal to what the action
	// sent), and getting this wrong is worse than being conservative — a
	// falsely convergent binding makes plan/execute re-fire the action
	// forever, since desired never catches up with observed. Confirmed
	// non-convergent case: compartmentId via ChangeVcnCompartmentDetails
	// does converge (observed.compartmentId becomes what was sent), but
	// AddVcnCidrDetails.cidrBlock does not — it's "the CIDR to append to
	// cidrBlocks" per the OCI SDK's own doc comment, while the resource's
	// own (deprecated) cidrBlock scalar stays pinned to cidrBlocks[0]
	// regardless of what gets added (cic-module-oracle-cloud PR #27 review,
	// 2026-09-28). Add*/Remove*Details models are still registered as
	// operations (schema.go / resolve.go) — they are simply not auto-bound
	// to a config field's policy here.
	actionOf := map[string]string{}
	for _, am := range actionModels {
		if !strings.HasPrefix(am.Name, "Change") {
			continue
		}
		for name := range jsonNames(am) {
			if _, seen := actionOf[name]; !seen {
				actionOf[name] = am.Name
			}
		}
	}

	all := map[string]bool{}
	for n := range inC {
		all[n] = true
	}
	for n := range inU {
		all[n] = true
	}
	for n := range inR {
		all[n] = true
	}

	var out []FieldPolicy
	for name := range all {
		fp := FieldPolicy{
			Field:    name,
			InCreate: inC[name],
			InUpdate: inU[name],
			InRead:   inR[name],
		}
		action := actionOf[name]
		switch {
		case fp.InUpdate:
			fp.Policy = PolicyMutable
		case action != "":
			fp.Policy = PolicyAction
			fp.Action = action
		case fp.InCreate && fp.InRead:
			fp.Policy = PolicyCreateOnly
		case fp.InCreate:
			fp.Policy = PolicyInputOnly
		case fp.InRead:
			fp.Policy = PolicyOutputOnly
		default:
			fp.Policy = PolicyUnknown
		}
		out = append(out, fp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}

// jsonNames returns the set of a model's field json names, skipping fields
// without a json tag (embedded request/response wrappers). For OCI *Details
// models every field has a json tag, so this reads their full surface.
func jsonNames(m Model) map[string]bool {
	set := map[string]bool{}
	for _, f := range m.Fields {
		if f.JSON != "" {
			set[f.JSON] = true
		}
	}
	return set
}

// actionModelRe matches an action's *Details model name: Change*/Add*/Remove*
// (the OCI verbs that mutate a resource outside plain Update).
var actionModelRe = regexp.MustCompile(`^(Change|Add|Remove)\w*Details$`)

// ResourcePolicy derives a resource's field policy from models alone, using the
// SDK's name conventions. Prefer PolicyOf with a Resolution built from the
// operation registry — see resolve.go for why the name conventions do not hold
// across services.
//
// resource is the read-model name, e.g. "Vcn". Missing models are treated as
// empty (a resource may legitimately have no Update model).
func ResourcePolicy(models []Model, resource string) []FieldPolicy {
	return PolicyOf(Resolve(models, nil, resource))
}

// PolicyOf derives the field policy of an already-resolved resource.
func PolicyOf(res Resolution) []FieldPolicy {
	return DeriveFieldPolicy(res.Create, res.Update, res.Read, res.Actions)
}
