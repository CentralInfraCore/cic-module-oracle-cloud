// Package main — provider.go is the cic:provider ABI domain layer
// (docs/design/specs/provider-abi.md). It has no build tag so the same code is
// compiled into the TinyGo/wasip1 guest (dispatched by abi.go) AND exercised by
// host-side `go test` (provider_test.go) without going through wasm.
//
// Status discipline (three-level, see CIC/CLAUDE.md):
//   - describe          — implemented: deterministic module manifest.
//   - validate          — implemented at the ENVELOPE level; schema-conformance
//     of the payload is scaffold (needs the generated OCI
//     payload schemas, roadmap P2.3). validate reports what
//     it actually checked in validation-result.checked.
//   - plan              — implemented for the trivial noop (desired == observed);
//     real diff→provider_operations is scaffold (P2.3).
//   - observe/execute/poll/invoke/destroy — scaffold: these are sign+send ops
//     that require the relay trust-flow host capability
//     (relay-requirements.md R1/R2), not yet available. They
//     return a typed provider-error naming the missing
//     precondition — never a faked success.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// abiVersion is the provider interface this module implements.
const abiVersion = "cic:provider@0.1.0"

// providerName is the CIC provider identity this module actuates.
const providerName = "oracle-cloud"

// providerOps is the fixed op set of the cic:provider ABI, in spec order. The
// dispatcher (abi.go) routes exactly these; project.yaml's abi.operations must
// match. describe/validate/plan are host-boundary "none"; the rest are
// "sign+send" (provider-abi.md, "Operation meanings").
var providerOps = []string{
	"describe", "validate", "observe", "plan",
	"execute", "poll", "invoke", "destroy",
}

// ---- schema-payload envelope (provider-abi.md, "Envelope") ----

// schemaPayload crosses the boundary as a schema-tagged, hashed payload. Resource
// data never changes the ABI; it travels here. schema_hash is the hex sha256 of
// the schema the payload validates against; data is the instance, encoded per
// encoding. For canonical-json, data is the raw JSON instance.
type schemaPayload struct {
	SchemaID      string          `json:"schema_id"`
	SchemaVersion string          `json:"schema_version"`
	SchemaHash    string          `json:"schema_hash"`
	Encoding      string          `json:"encoding"`
	Data          json.RawMessage `json:"data"`
}

const (
	encCanonicalJSON = "canonical-json"
	encCBOR          = "cbor"
)

// ---- provider-error (provider-abi.md, "Error model") ----

// providerError is a CIC-canonical error. class drives host behaviour;
// provider_code preserves the provider-native code verbatim for evidence.
type providerError struct {
	Class        string `json:"class"`
	ProviderCode string `json:"provider_code,omitempty"`
	Retryable    bool   `json:"retryable"`
	RequestID    string `json:"request_id,omitempty"`
	FieldPath    string `json:"field_path,omitempty"`
	Message      string `json:"message"`
}

// Error classes (provider-abi.md). class is CIC-canonical, not provider-native.
const (
	classSchema     = "schema"
	classValidation = "validation"
	classConflict   = "conflict"
	classNotFound   = "not-found"
	classPermission = "permission"
	classTransport  = "transport"
	classProvider   = "provider"
	classInternal   = "internal"
)

// ---- result discriminator ----
//
// Each op returns result<T, provider-error>. On the {data,error} transport wire
// (envelope.go), a DOMAIN error is an expected outcome of a SUCCESSFUL call, so
// it travels in data as the error arm of this discriminator — not in the
// transport error slot, which is reserved for malformed input / internal panics.

type providerResult struct {
	Status string          `json:"status"` // "ok" | "error"
	Result json.RawMessage `json:"result,omitempty"`
	Error  *providerError  `json:"error,omitempty"`
}

func okResult(v interface{}) ([]byte, error) {
	inner, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(providerResult{Status: "ok", Result: json.RawMessage(inner)})
}

func errResult(pe *providerError) ([]byte, error) {
	return json.Marshal(providerResult{Status: "error", Error: pe})
}

// hostSignSendUnavailable is the scaffold error for every sign+send op: the
// relay trust-flow host capability (relay-requirements.md R1/R2) is not wired,
// so the op cannot actuate. Typed transport-class, non-retryable, with a
// provider_code a reviewer can grep for.
func hostSignSendUnavailable(op string) *providerError {
	return &providerError{
		Class:        classTransport,
		ProviderCode: "HOST_SIGN_SEND_UNAVAILABLE",
		Retryable:    false,
		Message: op + ": requires the relay trust-flow sign+send host capability " +
			"(relay-requirements.md R1/R2), not yet available. This module declares " +
			"the reach in project.yaml capability_manifest; the host does not expose it yet.",
	}
}

// ---- describe (implemented) ----

type moduleManifest struct {
	Name                 string      `json:"name"`
	Version              string      `json:"version"`
	ABIVersion           string      `json:"abi_version"`
	Provider             string      `json:"provider"`
	ResourceKinds        []string    `json:"resource_kinds"`
	Operations           []string    `json:"operations"`
	Imports              []importReq `json:"imports"`
	RequiredCapabilities capReq      `json:"required_capabilities"`
}

// importReq is a host-function requirement the module declares for its sign+send
// ops (P0.3). Declaration only — names are provisional until settled with the
// relay (relay-requirements.md R1/R2). Mirrors project.yaml abi.imports.
type importReq struct {
	Module    string   `json:"module"`
	Functions []string `json:"functions"`
}

// capReq is a summary of project.yaml's capability_manifest reach, so a host can
// read the module's declared boundary from describe() without parsing the repo.
type capReq struct {
	EgressHosts []string `json:"egress_hosts"`
	SecretOps   []string `json:"secret_ops"`
}

// Describe returns the module manifest: identity, supported resource kinds,
// the fixed op set, and the declared capability reach. Pure, deterministic.
func Describe(auth, data []byte) ([]byte, error) {
	m := moduleManifest{
		Name:       "cic-module-oracle-cloud",
		Version:    "1.0.0",
		ABIVersion: abiVersion,
		Provider:   providerName,
		// Derived from the embedded generated contracts, so describe() always
		// reports exactly the kinds the module can validate/plan.
		ResourceKinds: supportedKinds(),
		Operations:    providerOps,
		// The sign+send host surface this module requires (project.yaml
		// abi.imports). Provisional until settled with the relay (R1/R2).
		Imports: []importReq{
			{Module: "cic-flow", Functions: []string{"sign", "actuate"}},
		},
		RequiredCapabilities: capReq{
			EgressHosts: []string{"*.oraclecloud.com"},
			SecretOps:   []string{"oci/*:sign"},
		},
	}
	return okResult(m)
}

// supportedKinds is the sorted list of resource kinds the module has an embedded
// contract for (contracts.go).
func supportedKinds() []string {
	c := resourceContracts()
	kinds := make([]string, 0, len(c))
	for k := range c {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// ---- validate (envelope-level implemented; schema-conformance scaffold) ----

type validationRequest struct {
	Kind   string        `json:"kind"`
	Intent schemaPayload `json:"intent"`
}

type validationResult struct {
	Admissible bool            `json:"admissible"`
	Checked    []string        `json:"checked"` // what was actually verified
	Errors     []providerError `json:"errors,omitempty"`
}

// Validate checks the intent envelope for well-formedness. It does NOT yet check
// the payload against its schema — that needs the generated OCI payload schemas
// (roadmap P2.3) — and it says so via validation-result.checked, so a caller is
// never misled into thinking schema-conformance was verified. Pure.
func Validate(auth, data []byte) ([]byte, error) {
	var req validationRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return errResult(&providerError{
			Class: classValidation, Message: "validation-request is not valid JSON: " + err.Error(),
		})
	}

	var errs []providerError
	if req.Kind == "" {
		errs = append(errs, providerError{
			Class: classValidation, FieldPath: "kind", Message: "kind is required",
		})
	}
	errs = append(errs, checkEnvelope("intent", req.Intent)...)

	checked := []string{"envelope.well-formed"}
	// Schema-conformance against the generated contract (P2.3), when one exists
	// for the kind and the envelope decoded as canonical-json. Envelope errors
	// above already cover a malformed payload; only conform when it is usable.
	if c, ok := resourceContracts()[req.Kind]; ok && len(errs) == 0 {
		checked = append(checked, "schema-conformance")
		errs = append(errs, conformIntent(c, req.Intent)...)
	}

	res := validationResult{
		Admissible: len(errs) == 0,
		Checked:    checked,
		Errors:     errs,
	}
	return okResult(res)
}

// conformIntent checks an intent payload against a resource contract: required
// fields present, no unknown/provider-computed fields set (the config contract
// omits output-only fields, so an unknown key is either a typo or an attempt to
// set a computed field), and a coarse JSON-type match per field.
func conformIntent(c resourceContract, intent schemaPayload) []providerError {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(intent.Data, &obj); err != nil {
		return []providerError{{Class: classSchema, FieldPath: "intent.data", Message: "data is not a JSON object"}}
	}
	var errs []providerError
	for _, r := range c.required {
		if _, ok := obj[r]; !ok {
			errs = append(errs, providerError{
				Class: classValidation, FieldPath: "intent.data." + r, Message: "required field missing",
			})
		}
	}
	for name, raw := range obj {
		fd, ok := c.fields[name]
		if !ok {
			errs = append(errs, providerError{
				Class: classSchema, FieldPath: "intent.data." + name,
				Message: "unknown field: not in the config contract (provider-computed fields cannot be set)",
			})
			continue
		}
		if fd.jsonType != "" && !jsonKindMatches(fd.jsonType, raw) {
			errs = append(errs, providerError{
				Class: classSchema, FieldPath: "intent.data." + name,
				Message: "type mismatch: want " + fd.jsonType,
			})
		}
	}
	return errs
}

// jsonKindMatches does a coarse type check by the first significant byte of the
// raw JSON value. null is accepted for any type (tri-state edits). Adequate for
// catching gross type errors without a full JSON-Schema engine in the guest.
func jsonKindMatches(want string, raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return true
	}
	c := s[0]
	switch want {
	case "string":
		return c == '"'
	case "array":
		return c == '['
	case "object":
		return c == '{'
	case "boolean":
		return c == 't' || c == 'f'
	case "integer", "number":
		return c == '-' || (c >= '0' && c <= '9')
	default:
		return true
	}
}

// checkEnvelope validates a schema-payload's structural contract (provider-abi.md
// "Envelope"): required tags present, a known encoding, and — for canonical-json
// — a decodable data instance. Returns one providerError per problem, field_path
// rooted at field.
func checkEnvelope(field string, p schemaPayload) []providerError {
	var errs []providerError
	add := func(path, msg string) {
		errs = append(errs, providerError{Class: classSchema, FieldPath: field + "." + path, Message: msg})
	}
	if p.SchemaID == "" {
		add("schema_id", "schema_id is required")
	}
	if p.SchemaVersion == "" {
		add("schema_version", "schema_version is required")
	}
	if _, err := hex.DecodeString(p.SchemaHash); p.SchemaHash == "" || err != nil {
		add("schema_hash", "schema_hash must be a hex sha256")
	}
	switch p.Encoding {
	case encCanonicalJSON:
		if len(p.Data) == 0 || !json.Valid(p.Data) {
			add("data", "data must be a valid JSON instance for canonical-json encoding")
		}
	case encCBOR:
		if len(p.Data) == 0 {
			add("data", "data is required for cbor encoding")
		}
	default:
		add("encoding", "encoding must be canonical-json or cbor")
	}
	return errs
}

// ---- plan (noop implemented; diff scaffold) ----

type planRequest struct {
	Kind     string        `json:"kind"`
	Desired  schemaPayload `json:"desired"`
	Observed schemaPayload `json:"observed"`
}

type executionPlan struct {
	PlanID             string              `json:"plan_id"`   // sha256:... over the canonical plan inputs
	Operation          string              `json:"operation"` // create|update|replace|action|noop
	ProviderOperations []providerOperation `json:"provider_operations,omitempty"`
	Notes              []string            `json:"notes,omitempty"`
}

// providerOperation is a concrete OCI operation the plan will run (P2.2 registry
// names + HTTP method/path). A plan carrying these is reviewable and signable
// before any mutation (provider-abi.md: plan/execute split).
type providerOperation struct {
	Operation string `json:"operation"` // e.g. UpdateVcn, ChangeVcnCompartment
	Method    string `json:"method,omitempty"`
	Path      string `json:"path,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// planProviderOps maps the classified operation + changed fields to the concrete
// OCI operations, by the SDK naming convention (Create/Update/Delete<Resource>)
// and each action field's own operation (x-cic-action), attaching each one's HTTP
// method+path from the embedded registry. A replace supersedes everything with
// Delete+Create; otherwise a single Update carries the mutable changes and each
// action field contributes its own operation.
func planProviderOps(c resourceContract, op string, changed []string) []providerOperation {
	mk := func(name, reason string) providerOperation {
		po := providerOperation{Operation: name, Reason: reason}
		if h, ok := c.operations[name]; ok {
			po.Method, po.Path = h.method, h.path
		}
		return po
	}
	switch op {
	case "noop":
		return nil
	case "replace":
		return []providerOperation{
			mk("Delete"+c.resource, "immutable field change requires replacement"),
			mk("Create"+c.resource, "re-create with the desired configuration"),
		}
	}
	var ops []providerOperation
	hasUpdate := false
	for _, name := range changed {
		fd := c.fields[name]
		switch fd.policy {
		case "mutable":
			hasUpdate = true
		case "action-managed":
			if fd.action != "" {
				ops = append(ops, mk(fd.action, "field "+name+" changed"))
			}
		}
	}
	if hasUpdate {
		ops = append([]providerOperation{mk("Update"+c.resource, "mutable fields changed")}, ops...)
	}
	return ops
}

// Plan produces an execution plan from desired + observed. This increment
// computes the trivial, fully-decidable case — desired == observed → noop — with
// a real, hashable plan_id. Any non-noop diff needs the OCI operation registry
// (roadmap P2.3) to map fields to provider_operations, so it returns a typed
// scaffold error rather than a fabricated plan. Pure, no mutation.
func Plan(auth, data []byte) ([]byte, error) {
	var req planRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return errResult(&providerError{
			Class: classValidation, Message: "plan-request is not valid JSON: " + err.Error(),
		})
	}
	if envErrs := checkEnvelope("desired", req.Desired); len(envErrs) > 0 {
		return errResult(&envErrs[0])
	}
	if envErrs := checkEnvelope("observed", req.Observed); len(envErrs) > 0 {
		return errResult(&envErrs[0])
	}

	c, ok := resourceContracts()[req.Kind]
	if !ok {
		// No generated contract for this kind: only the no-op case is decidable.
		if canonicalEqual(req.Desired.Data, req.Observed.Data) {
			return okResult(noopPlan(req))
		}
		return errResult(&providerError{
			Class: classInternal, ProviderCode: "PLAN_CONTRACT_UNAVAILABLE", Retryable: false,
			Message: "plan: no generated contract for kind " + req.Kind + "; only the no-op case is decidable.",
		})
	}

	desired, err := decodeObject(req.Desired.Data)
	if err != nil {
		return errResult(&providerError{Class: classSchema, FieldPath: "desired.data", Message: err.Error()})
	}
	observed, err := decodeObject(req.Observed.Data)
	if err != nil {
		return errResult(&providerError{Class: classSchema, FieldPath: "observed.data", Message: err.Error()})
	}

	// Diff over the intent's declared fields. An absent desired field is
	// unmanaged (tri-state: don't touch — provider-abi.md payload conventions),
	// so only fields the intent sets are compared. Each changed field escalates
	// the operation by its policy: mutable→update, action-managed→action,
	// create-only/input-only→replace.
	var changed []string
	op := "noop"
	for name, dv := range desired {
		fd, ok := c.fields[name]
		if !ok {
			continue // unknown fields are a validate concern, not plan
		}
		if valueEqual(dv, observed[name]) {
			continue
		}
		changed = append(changed, name)
		op = escalateOp(op, policyOp(fd.policy))
	}
	sort.Strings(changed)

	return okResult(executionPlan{
		PlanID:             "sha256:" + planHash(req.Kind, req.Desired, req.Observed),
		Operation:          op,
		ProviderOperations: planProviderOps(c, op, changed),
		Notes:              planNotes(op, changed),
	})
}

func noopPlan(req planRequest) executionPlan {
	return executionPlan{
		PlanID:    "sha256:" + planHash(req.Kind, req.Desired, req.Observed),
		Operation: "noop",
		Notes:     []string{"desired == observed; no provider operations"},
	}
}

func decodeObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("data is not a JSON object")
	}
	return m, nil
}

// valueEqual compares two intent/state field values; an absent value on one side
// only is a change.
func valueEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	return canonicalEqual(a, b)
}

// policyOp maps a field's CIC policy to the plan operation a change to it forces.
func policyOp(policy string) string {
	switch policy {
	case "mutable":
		return "update"
	case "action-managed":
		return "action"
	case "create-only", "input-only":
		return "replace"
	default:
		return "update"
	}
}

// escalateOp keeps the most disruptive operation: replace > action > update > noop.
func escalateOp(cur, next string) string {
	rank := map[string]int{"noop": 0, "update": 1, "action": 2, "replace": 3}
	if rank[next] > rank[cur] {
		return next
	}
	return cur
}

func planNotes(op string, changed []string) []string {
	if op == "noop" {
		return []string{"desired == observed; no provider operations"}
	}
	return []string{op + ": changed fields " + strings.Join(changed, ", ")}
}

// planHash is a deterministic hash over the plan inputs, so a plan is identifiable
// and signable (provider-abi.md: plan_id is hashable).
func planHash(kind string, desired, observed schemaPayload) string {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte(desired.SchemaID))
	h.Write([]byte(desired.SchemaHash))
	h.Write([]byte(desired.Data))
	h.Write([]byte(observed.SchemaHash))
	h.Write([]byte(observed.Data))
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalEqual compares two JSON instances structurally (key order and
// insignificant whitespace ignored), by re-marshalling through Go's map/slice
// model. Adequate for the noop decision on canonical-json payloads.
func canonicalEqual(a, b json.RawMessage) bool {
	na, err := reencode(a)
	if err != nil {
		return false
	}
	nb, err := reencode(b)
	if err != nil {
		return false
	}
	return string(na) == string(nb)
}

func reencode(raw json.RawMessage) ([]byte, error) {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// ---- sign+send ops (scaffold — host capability not yet available) ----

// Observe/Execute/Poll/Invoke/Destroy all require the relay trust-flow sign+send
// host capability (relay-requirements.md R1/R2). Until the host exposes it, each
// returns the typed hostSignSendUnavailable error — an honest scaffold, not a
// faked success.

func Observe(auth, data []byte) ([]byte, error) { return errResult(hostSignSendUnavailable("observe")) }
func Execute(auth, data []byte) ([]byte, error) { return errResult(hostSignSendUnavailable("execute")) }
func Poll(auth, data []byte) ([]byte, error)    { return errResult(hostSignSendUnavailable("poll")) }
func Invoke(auth, data []byte) ([]byte, error)  { return errResult(hostSignSendUnavailable("invoke")) }
func Destroy(auth, data []byte) ([]byte, error) { return errResult(hostSignSendUnavailable("destroy")) }
