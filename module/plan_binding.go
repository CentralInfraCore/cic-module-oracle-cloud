package main

// plan_binding.go is the M4a plan-binding model: three digests over canonical JSON
// (cic-canonical-json/1) and the derivation Execute repeats before it signs anything.
// The shape of every digest input is in docs/design/specs/plan-digest.md and its
// expected values are in testdata/plan_digest_vectors.json, so another implementation
// can compute the same digests: Go is one implementation of that specification.
//
// What this proves, and what it does not: a request whose plan, desired state,
// observed state and target agree with each other, and with the plan the provider
// derives from them, is internally consistent. It does not prove that anyone approved
// it, that the caller may run it, or that it was not run before; a caller who can
// change every input and every digest builds another consistent request. Approval,
// authority and single use are the Relay's (M4b).

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Domain tags. Each digest has its own, so objects of different roles never share a
// hash namespace, and a new definition changes the tag.
const (
	planDigestTag      = "cic-oracle-execution-plan/1"
	targetDigestTag    = "cic-oracle-execution-target/1"
	executionDigestTag = "cic-oracle-execution/1"
)

// canonical JSON builders: the digest input is itself canonical JSON text, so every
// field is delimited by the JSON structure (quotes, brackets, commas), never by plain
// concatenation of strings.
type jmember struct {
	key string
	val []byte
}

func jstr(s string) []byte { return canonString(nil, []byte(s)) }

// jnullable writes an empty value as null: a field that is absent and a field that is
// the empty string are one thing in the digest, by rule.
func jnullable(s string) []byte {
	if s == "" {
		return []byte("null")
	}
	return jstr(s)
}

func jobj(ms ...jmember) []byte {
	sort.Slice(ms, func(i, j int) bool { return ms[i].key < ms[j].key })
	out := []byte{'{'}
	for i, m := range ms {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, jstr(m.key)...)
		out = append(out, ':')
		out = append(out, m.val...)
	}
	return append(out, '}')
}

func jarr(vs ...[]byte) []byte {
	out := []byte{'['}
	for i, v := range vs {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, v...)
	}
	return append(out, ']')
}

func digestOf(envelope []byte) string {
	sum := sha256.Sum256(envelope)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// contractEnvelope is the canonical form of everything in a kind's registry that
// shapes a plan or an execution: its operations (name, method, path template, role)
// and its fields (policy, type, action). A module version that maps a change to other
// operations has another contract digest, so an older plan no longer matches.
func contractEnvelope(kind string, c resourceContract, known bool) []byte {
	if !known {
		return jobj(jmember{"kind", jstr(kind)}, jmember{"known", []byte("false")})
	}
	var ops []jmember
	for name, o := range c.operations {
		ops = append(ops, jmember{name, jobj(
			jmember{"method", jstr(o.method)}, jmember{"path", jstr(o.path)}, jmember{"role", jstr(o.role)})})
	}
	var fields []jmember
	for name, f := range c.fields {
		fields = append(fields, jmember{name, jobj(
			jmember{"action", jnullable(f.action)}, jmember{"json_type", jnullable(f.jsonType)}, jmember{"policy", jstr(f.policy)})})
	}
	req := append([]string(nil), c.required...)
	sort.Strings(req)
	var reqs [][]byte
	for _, r := range req {
		reqs = append(reqs, jstr(r))
	}
	return jobj(
		jmember{"fields", jobj(fields...)},
		jmember{"kind", jstr(kind)},
		jmember{"known", []byte("true")},
		jmember{"operations", jobj(ops...)},
		jmember{"required", jarr(reqs...)},
	)
}

func contractDigest(kind string) string {
	c, ok := resourceContracts()[kind]
	return digestOf(contractEnvelope(kind, c, ok))
}

func payloadEnvelope(p schemaPayload) ([]byte, error) {
	data, err := canonicalJSON(p.Data)
	if err != nil {
		return nil, err
	}
	return jobj(
		jmember{"data", data},
		jmember{"encoding", jstr(p.Encoding)},
		jmember{"schema_hash", jstr(p.SchemaHash)},
		jmember{"schema_id", jstr(p.SchemaID)},
		jmember{"schema_version", jstr(p.SchemaVersion)},
	), nil
}

// planEnvelope is the input of plan_digest. provider_operations keep their order;
// their reason text and the plan's notes are explanations nothing executes, so they
// are not part of it (and can change without changing the digest, on purpose).
func planEnvelope(kind, contract string, desired, observed schemaPayload, operation string, ops []providerOperation) ([]byte, error) {
	d, err := payloadEnvelope(desired)
	if err != nil {
		return nil, fmt.Errorf("desired: %w", err)
	}
	o, err := payloadEnvelope(observed)
	if err != nil {
		return nil, fmt.Errorf("observed: %w", err)
	}
	var po [][]byte
	for _, op := range ops {
		po = append(po, jobj(jmember{"method", jstr(op.Method)}, jmember{"operation", jstr(op.Operation)}, jmember{"path", jstr(op.Path)}))
	}
	return jobj(
		jmember{"contract", jstr(contract)},
		jmember{"desired", d},
		jmember{"kind", jstr(kind)},
		jmember{"observed", o},
		jmember{"operation", jstr(operation)},
		jmember{"provider_operations", jarr(po...)},
		jmember{"v", jstr(planDigestTag)},
	), nil
}

// targetEnvelope is the input of target_digest: where the plan runs. host and key_id
// are always strings; base_path, resource_id and revision are null when empty.
func targetEnvelope(kind string, b execBinding) []byte {
	return jobj(
		jmember{"base_path", jnullable(b.BasePath)},
		jmember{"host", jstr(b.Host)},
		jmember{"key_id", jstr(b.KeyID)},
		jmember{"kind", jstr(kind)},
		jmember{"resource_id", jnullable(b.ResourceID)},
		jmember{"revision", jnullable(b.Revision)},
		jmember{"v", jstr(targetDigestTag)},
	)
}

// executionEnvelope binds the plan and the target in one value: the one an approval
// (M4b) names, so a plan cannot be paired with another, equally valid, target.
func executionEnvelope(planDigest, targetDigest string) []byte {
	return jobj(
		jmember{"plan_digest", jstr(planDigest)},
		jmember{"target_digest", jstr(targetDigest)},
		jmember{"v", jstr(executionDigestTag)},
	)
}

// Bounds on the free-text target fields: they end up in a signed request.
const maxTargetField = 1024

// checkTarget is the STRUCTURE of a target: host and key_id present, every field
// bounded, resource_id (when there is one) a well-formed OCID of the kind. Whether a
// resource id is REQUIRED is the lifecycle's business (see planFromRequest), not this
// function's. field names the object in a refusal ("target" for plan, "binding" for
// execute).
func checkTarget(kind string, b execBinding, field string) *providerError {
	bad := func(name, why string) *providerError {
		return &providerError{Class: classValidation, FieldPath: field + "." + name, Message: field + "." + name + " " + why}
	}
	if b.Host == "" {
		return bad("host", "is required")
	}
	if b.KeyID == "" {
		return bad("key_id", "is required")
	}
	for name, v := range map[string]string{"host": b.Host, "base_path": b.BasePath, "key_id": b.KeyID, "resource_id": b.ResourceID, "revision": b.Revision} {
		if len(v) > maxTargetField {
			return bad(name, fmt.Sprintf("is longer than %d bytes", maxTargetField))
		}
	}
	if b.ResourceID != "" && !wellFormedOCID(kind, b.ResourceID) {
		return bad("resource_id", "is not a well-formed OCID of the type for "+kind+" (got \""+shortASCII(b.ResourceID)+"\")")
	}
	return nil
}
