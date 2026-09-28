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

// ResourceSchemas builds the config and state schemas for a resource from models
// alone, using the SDK's name conventions. Prefer ResourceSchemasFrom with a
// Resolution built from the operation registry: that is the service-agnostic
// path, and this one cannot see a resource whose create verb is not "Create".
func ResourceSchemas(models []Model, resource, schemaNS, version string) (config, state map[string]interface{}) {
	return ResourceSchemasFrom(Resolve(models, nil, resource), schemaNS, version)
}

// ResourceSchemasFrom builds the config and state schemas for a resolved
// resource. schemaNS is the CIC schema id stem, e.g. "cic:network:vcn" →
// "cic:network:vcn-config" and "…-state".
func ResourceSchemasFrom(res Resolution, schemaNS, version string) (config, state map[string]interface{}) {
	resource, create := res.Resource, res.Create

	// json name -> Field, preferring the read model's type, then create, update,
	// action — the read model is the most canonical view of a field.
	info := map[string]Field{}
	for _, src := range append([]Model{res.Read, create, res.Update}, res.Actions...) {
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

	policies := PolicyOf(res)

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
	// The SDK resource name — descriptive metadata for a human reading the
	// schema. provider_operations plan against the role-tagged entries in
	// "operations" (see OperationMap), never against this name: OCI's naming is
	// not a lifecycle convention (LaunchInstance creates, TerminateInstance
	// deletes).
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

// OperationMap returns the HTTP method+path for the operations a plan references
// for this resource — its read/create/update/delete lifecycle plus every
// resolved action operation, whether or not it happens to govern an existing
// config field's policy (AddVcnCidr is a standalone action: see
// cic-module-oracle-cloud#27/#29 — it governs no field, only carries its own
// input contract below). Keyed by operation name, so the module can attach the
// concrete HTTP call to each provider_operation without embedding the whole
// registry.
//
// Each entry carries role, the lifecycle role Resolve derived structurally
// (RoleRead/RoleCreate/RoleUpdate/RoleDelete/RoleAction — see resolve.go). This
// is the only place a consumer needs to look to tell what an operation does:
// the operation's Go name is an SDK naming accident (LaunchInstance creates,
// TerminateInstance deletes) and must not be re-derived from it downstream.
//
// Each entry also carries path_params, the {name} placeholders of its path in
// order. A consumer must bind every one of them: /vcns/{vcnId} needs only the
// resource id, but /n/{namespaceName}/b/{bucketName} needs two values and
// neither is an id. Leaving the list implicit is what made "substitute the
// resource id into every placeholder" look correct — it is correct only for
// single-parameter paths, which is 75% of the SDK's Get operations, not all of
// them.
func OperationMap(res Resolution) map[string]map[string]interface{} {
	out := map[string]map[string]interface{}{}
	add := func(op *Operation, role string, body Model) {
		if op == nil {
			return
		}
		e := map[string]interface{}{"method": op.HTTPMethod, "path": op.HTTPPath, "role": role}
		if len(op.PathParams) > 0 {
			e["path_params"] = op.PathParams
		}
		if input := actionInputSchema(body); input != nil {
			e["input"] = input
		}
		out[op.Name] = e
	}
	add(res.ReadOp, RoleRead, Model{})
	add(res.CreateOp, RoleCreate, Model{})
	add(res.UpdateOp, RoleUpdate, Model{})
	add(res.DeleteOp, RoleDelete, Model{})
	// Actions are paired with ActionOps by index (see Resolution.Actions' doc).
	for i := range res.ActionOps {
		var body Model
		if i < len(res.Actions) {
			body = res.Actions[i]
		}
		add(&res.ActionOps[i], RoleAction, body)
	}
	return out
}

// actionInputSchema builds a JSON-Schema properties map for an action's request
// body model, so a caller driving Invoke() directly has a formal contract for
// what the action expects — not just the method/path/role OperationMap already
// carries. Only action operations get this (create/update/delete already have
// their full body via the config schema's mutable/create-only fields; an
// action's body is otherwise undocumented anywhere in the generated schema —
// cic-module-oracle-cloud#29). Unlike config fields, an action's own fields
// never carry x-cic-policy: their role is "argument to this action", not a
// resource lifecycle classification, and is not auto-bound to any config field
// (policy.go: only Change*Details models are, and even then only for the
// convergent field they set).
func actionInputSchema(m Model) map[string]interface{} {
	props := map[string]interface{}{}
	var required []string
	for _, f := range m.Fields {
		if f.JSON == "" {
			continue
		}
		prop := typeToSchema(f.Type)
		if f.Doc != "" {
			prop["description"] = firstSentence(f.Doc)
		}
		props[f.JSON] = prop
		if f.Mandatory {
			required = append(required, f.JSON)
		}
	}
	if len(props) == 0 {
		return nil
	}
	sort.Strings(required)
	doc := map[string]interface{}{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
	}
	if len(required) > 0 {
		doc["required"] = required
	}
	return doc
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
