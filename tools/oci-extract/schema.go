package ociextract

// schema.go emits a resource's CIC payload schemas (P2.3, part 2) from the field
// policy + model type graph:
//
//   - <ns>-config: the intent surface — fields a caller may set (mutable,
//     create-only, action-managed, input-only), each carrying its CIC policy so
//     the create-only/immutable/action semantics the SDK tags cannot express are
//     first-class. required = the create model's mandatory fields.
//   - <ns>-state: the observed surface — every field read back (output-only plus
//     the settable fields as observed), each carrying its policy.
//
// Output is a JSON-Schema object (additionalProperties:false) built as ordered
// maps so it marshals deterministically for hashing (the P2.4 gate input).

import (
	"sort"
	"strings"
)

// xCICPolicy maps a field policy to the schema annotation a consumer acts on.
var xCICPolicy = map[string]string{
	PolicyMutable:    "mutable",
	PolicyCreateOnly: "create-only",
	PolicyAction:     "action-managed",
	PolicyInputOnly:  "input-only",
	PolicyOutputOnly: "provider-computed",
}

// ResourceSchemas builds the config and state schemas for a resource. schemaNS
// is the CIC schema id stem, e.g. "cic:network:vcn" → "cic:network:vcn-config"
// and "…-state". models is the extracted registry's models.
func ResourceSchemas(models []Model, resource, schemaNS, version string) (config, state map[string]interface{}) {
	byName := map[string]Model{}
	for _, m := range models {
		byName[m.Name] = m
	}
	create := byName["Create"+resource+"Details"]

	// json name -> Field, preferring the read model's type, then create, update,
	// action — the read model is the most canonical view of a field.
	info := map[string]Field{}
	for _, src := range append([]Model{byName[resource], create, byName["Update"+resource+"Details"]}, actionModels(models, resource)...) {
		for _, f := range src.Fields {
			if f.JSON == "" {
				continue
			}
			if _, seen := info[f.JSON]; !seen {
				info[f.JSON] = f
			}
		}
	}
	mandatory := map[string]bool{}
	for _, f := range create.Fields {
		if f.JSON != "" && f.Mandatory {
			mandatory[f.JSON] = true
		}
	}

	policies := ResourcePolicy(models, resource)

	configProps := map[string]interface{}{}
	stateProps := map[string]interface{}{}
	var required []string
	for _, p := range policies {
		prop := typeToSchema(info[p.Field].Type)
		prop["x-cic-policy"] = xCICPolicy[p.Policy]
		if p.Policy == PolicyAction && p.Action != "" {
			prop["x-cic-action"] = p.Action
		}
		if doc := info[p.Field].Doc; doc != "" {
			prop["description"] = firstSentence(doc)
		}
		// Config = the settable surface (everything not purely provider-computed).
		if p.Policy != PolicyOutputOnly {
			configProps[p.Field] = clone(prop)
			if mandatory[p.Field] {
				required = append(required, p.Field)
			}
		}
		// State = everything read back.
		if p.InRead {
			stateProps[p.Field] = clone(prop)
		}
	}
	sort.Strings(required)

	config = schemaDoc(schemaNS+"-config", version,
		"Intent (config) surface for "+resource+" — the fields a caller may set.",
		configProps, required)
	// The SDK resource name, so a consumer can construct operation names by
	// convention (Create/Update/Delete<Resource>) — used by plan to emit
	// provider_operations without embedding the full operation registry.
	config["x-cic-resource"] = resource
	state = schemaDoc(schemaNS+"-state", version,
		"Observed (state) surface for "+resource+" — the fields read back from the provider.",
		stateProps, nil)
	state["x-cic-resource"] = resource
	return config, state
}

// schemaDoc wraps properties into a JSON-Schema object.
func schemaDoc(id, version, desc string, props map[string]interface{}, required []string) map[string]interface{} {
	doc := map[string]interface{}{
		"$schema":              "http://json-schema.org/draft-07/schema#",
		"$id":                  id,
		"x-cic-schema-version": version,
		"title":                id,
		"description":          desc,
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
	}
	if len(required) > 0 {
		doc["required"] = required
	}
	return doc
}

// typeToSchema maps a Go type (as ExtractFile renders it) to a JSON-Schema type.
// Unresolved named types (nested structs, enums without a known mapping) are not
// guessed: they carry x-cic-go-type so the gap is visible, not silently wrong.
func typeToSchema(goType string) map[string]interface{} {
	t := strings.TrimPrefix(goType, "*")
	switch {
	case t == "string":
		return map[string]interface{}{"type": "string"}
	case t == "bool":
		return map[string]interface{}{"type": "boolean"}
	case t == "int", t == "int8", t == "int16", t == "int32", t == "int64",
		t == "uint", t == "uint8", t == "uint16", t == "uint32", t == "uint64":
		return map[string]interface{}{"type": "integer"}
	case t == "float32", t == "float64":
		return map[string]interface{}{"type": "number"}
	case strings.HasPrefix(t, "[]"):
		return map[string]interface{}{"type": "array", "items": typeToSchema(t[2:])}
	case strings.HasPrefix(t, "map["):
		if i := strings.Index(t, "]"); i >= 0 {
			return map[string]interface{}{"type": "object", "additionalProperties": typeToSchema(t[i+1:])}
		}
		return map[string]interface{}{"type": "object"}
	case t == "":
		return map[string]interface{}{"x-cic-go-type": "unknown"}
	case strings.HasSuffix(t, "Enum"):
		// OCI enum types are string-valued; the value set could be enumerated
		// later from the enum's declared constants.
		return map[string]interface{}{"type": "string", "x-cic-go-type": t}
	default:
		// A nested struct or otherwise unmapped named type: object, flagged.
		return map[string]interface{}{"type": "object", "x-cic-go-type": t}
	}
}

// ResourceOperationMap returns the HTTP method+path for the operations a plan
// references for this resource: Create/Update/Delete<Resource> plus each
// action-managed field's operation (x-cic-action minus "Details"). Keyed by
// operation name, so the module can attach the concrete HTTP call to each
// provider_operation without embedding the whole registry.
func ResourceOperationMap(operations []Operation, resource string, policies []FieldPolicy) map[string]map[string]string {
	need := map[string]bool{
		"Create" + resource: true,
		"Update" + resource: true,
		"Delete" + resource: true,
	}
	for _, p := range policies {
		if p.Policy == PolicyAction && p.Action != "" {
			need[strings.TrimSuffix(p.Action, "Details")] = true
		}
	}
	out := map[string]map[string]string{}
	for _, op := range operations {
		if need[op.Name] {
			out[op.Name] = map[string]string{"method": op.HTTPMethod, "path": op.HTTPPath}
		}
	}
	return out
}

// actionModels returns the Change*/Add*/Remove*…Details models that mention the
// resource (the action-managed mutation surface).
func actionModels(models []Model, resource string) []Model {
	var out []Model
	for _, m := range models {
		if actionModelRe.MatchString(m.Name) && strings.Contains(m.Name, resource) {
			out = append(out, m)
		}
	}
	return out
}

// clone shallow-copies a property map so config and state don't share mutable
// sub-maps.
func clone(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
