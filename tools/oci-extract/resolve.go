package ociextract

// resolve.go picks a resource's lifecycle operations and models out of the
// extracted registry. It is the service-agnostic half of P2.3: everything the
// schema emitter needs about a resource is derived from the HTTP surface the SDK
// declares, not from the Go identifiers OCI happened to choose.
//
// Why this is not "Create<Resource>Details":
//
//	core.Instance     is created by LaunchInstance  → LaunchInstanceDetails
//	core.Instance     is deleted by TerminateInstance
//	objectstorage.Bucket is updated by POST (not PUT) and addressed by two path
//	                     parameters, neither of which is an id
//
// Name templates get all three wrong, and — worse — get them wrong silently:
// the old code emitted a config schema with no `required` and no create surface
// rather than reporting that it had found no create model.
//
// The structural rule instead:
//
//	read       = GET  <readPath>                     (the resource's own path)
//	collection = readPath minus its trailing /{param}
//	create     = POST <collection>
//	update     = PUT  <readPath>, else POST <readPath>
//	delete     = DELETE <readPath>
//	actions    = POST under <readPath>/…
//
// and each operation's body/response model comes from the request/response
// struct's own `contributesTo:"body"` / `presentIn:"body"` tag.
//
// One name convention survives, and only as a fallback: when no response models
// are supplied there is nothing structural to match the resource against, so the
// entry point is the operation named Get<Resource>. Resolution records which
// route it took in ReadOpSource, so the output never hides the assumption.

import (
	"sort"
	"strings"
)

// Operation roles a resource's lifecycle is built from.
const (
	RoleRead   = "read"
	RoleCreate = "create"
	RoleUpdate = "update"
	RoleDelete = "delete"
	RoleAction = "action"
)

// Resolution is everything the schema emitter knows about one resource.
type Resolution struct {
	Resource string

	Read   Model // observed state
	Create Model // intent at creation
	Update Model // the freely mutable subset
	// Actions are the resolvable action bodies, in operation-name order, paired
	// with ActionOps by index.
	Actions []Model

	ReadOp    *Operation
	CreateOp  *Operation
	UpdateOp  *Operation
	DeleteOp  *Operation
	ActionOps []Operation

	// ReadOpSource is "response-body" when the read operation was matched
	// structurally (its response carries the resource as the body), or
	// "name-convention" when it fell back to Get<Resource>.
	ReadOpSource string

	// Unresolved lists what could not be derived, in a form a human can act on.
	// A resource with entries here is reported, never quietly emitted as if it
	// were complete.
	Unresolved []string
}

// Complete reports whether the resource resolved without gaps.
func (r Resolution) Complete() bool { return len(r.Unresolved) == 0 }

// Resolve derives a resource's lifecycle from the registry. ops may be empty, in
// which case resolution degrades to the SDK's name conventions
// (Create/Update/Delete<Resource>Details) — the pre-P2.5 behaviour, kept so a
// model-only invocation still works, and reported through ReadOpSource.
func Resolve(models []Model, ops []Operation, resource string) Resolution {
	res := Resolution{Resource: resource}
	byName := map[string]Model{}
	for _, m := range models {
		byName[m.Name] = m
	}

	if len(ops) == 0 {
		res.ReadOpSource = "name-convention"
		res.Read = byName[resource]
		res.Create = byName["Create"+resource+"Details"]
		res.Update = byName["Update"+resource+"Details"]
		res.Actions = legacyActionModels(models, resource)
		res.note(byName, resource)
		return res
	}

	res.ReadOp, res.ReadOpSource = findReadOp(ops, byName, resource)
	if res.ReadOp == nil {
		res.Unresolved = append(res.Unresolved,
			"no read operation: expected a GET whose response body is "+resource+", or an operation named Get"+resource)
		return res
	}

	readPath := res.ReadOp.HTTPPath
	collection := collectionPath(readPath)

	for i := range ops {
		op := ops[i]
		switch {
		case op.HTTPMethod == "POST" && op.HTTPPath == collection && collection != "":
			res.CreateOp = pick(res.CreateOp, &ops[i])
		case op.HTTPMethod == "PUT" && op.HTTPPath == readPath:
			res.UpdateOp = &ops[i] // PUT wins over POST for update
		case op.HTTPMethod == "DELETE" && op.HTTPPath == readPath:
			res.DeleteOp = pick(res.DeleteOp, &ops[i])
		case op.HTTPMethod == "POST" && strings.HasPrefix(op.HTTPPath, readPath+"/"):
			res.ActionOps = append(res.ActionOps, ops[i])
		}
	}
	if res.UpdateOp == nil {
		// Object storage updates a bucket with POST on the resource path; the
		// SDK offers no PUT there at all.
		for i := range ops {
			if ops[i].HTTPMethod == "POST" && ops[i].HTTPPath == readPath {
				res.UpdateOp = &ops[i]
				break
			}
		}
	}
	sort.Slice(res.ActionOps, func(i, j int) bool { return res.ActionOps[i].Name < res.ActionOps[j].Name })

	res.Read = responseBodyModel(res.ReadOp, byName, resource)
	res.Create = requestBodyModel(res.CreateOp, byName)
	res.Update = requestBodyModel(res.UpdateOp, byName)

	// An action whose body model was not supplied is out of scope for this
	// invocation, not missing: the caller chooses which model files to feed in.
	// Only the lifecycle core counts as a gap (see note).
	var actionOps []Operation
	for _, op := range res.ActionOps {
		m := requestBodyModel(&op, byName)
		if m.Name == "" || m.IsPolymorphic() {
			continue
		}
		actionOps = append(actionOps, op)
		res.Actions = append(res.Actions, m)
	}
	res.ActionOps = actionOps

	res.note(byName, resource)
	return res
}

// note records the gaps that make a resource's surface untrustworthy. A missing
// create model is the one that used to pass silently: the emitted config schema
// then had no required fields and only whatever Update and Read happened to
// share, which looks like a valid schema and is not one.
func (r *Resolution) note(byName map[string]Model, resource string) {
	if r.Read.Name == "" {
		r.Unresolved = append(r.Unresolved, "no read model for "+resource)
	} else if r.Read.IsPolymorphic() {
		r.Unresolved = append(r.Unresolved, "read model "+r.Read.Name+" is polymorphic (interface); concrete implementations are not expanded")
	}
	switch {
	case r.CreateOp != nil && r.Create.Name == "":
		r.Unresolved = append(r.Unresolved,
			"create operation "+r.CreateOp.Name+" resolved but its body model was not supplied")
	case r.Create.Name == "" && r.ReadOpSource == "name-convention" && len(r.Unresolved) == 0:
		// Model-only invocation: say what was looked for.
		if _, ok := byName["Create"+resource+"Details"]; !ok {
			r.Unresolved = append(r.Unresolved, "no create model: Create"+resource+"Details not among the supplied models")
		}
	}
	if r.Create.IsPolymorphic() {
		r.Unresolved = append(r.Unresolved,
			"create model "+r.Create.Name+" is polymorphic (interface); concrete implementations are not expanded")
	}
	if r.Update.IsPolymorphic() {
		r.Unresolved = append(r.Unresolved,
			"update model "+r.Update.Name+" is polymorphic (interface); concrete implementations are not expanded")
	}
}

// findReadOp locates the operation that reads the resource. Structural first:
// a GET whose response struct carries the resource as its body. If no response
// models were supplied there is nothing to match on, so it falls back to the
// SDK's Get<Resource> convention.
func findReadOp(ops []Operation, byName map[string]Model, resource string) (*Operation, string) {
	for i := range ops {
		if ops[i].HTTPMethod != "GET" || ops[i].Response == "" {
			continue
		}
		if bodyTypeOf(byName[ops[i].Response], "response") == resource {
			return &ops[i], "response-body"
		}
	}
	for i := range ops {
		if ops[i].Name == "Get"+resource {
			return &ops[i], "name-convention"
		}
	}
	return nil, ""
}

// collectionPath strips a path's trailing /{param} segment, giving the path the
// resource is created on. /vcns/{vcnId} → /vcns;
// /n/{namespaceName}/b/{bucketName} → /n/{namespaceName}/b. A read path with no
// trailing parameter (a singleton resource) has no collection.
func collectionPath(readPath string) string {
	i := strings.LastIndex(readPath, "/")
	if i <= 0 {
		return ""
	}
	last := readPath[i+1:]
	if !strings.HasPrefix(last, "{") || !strings.HasSuffix(last, "}") {
		return ""
	}
	return readPath[:i]
}

// requestBodyModel returns the model an operation sends as its request body,
// read from the request struct's contributesTo:"body" field.
func requestBodyModel(op *Operation, byName map[string]Model) Model {
	if op == nil || op.Request == "" {
		return Model{}
	}
	if t := bodyTypeOf(byName[op.Request], "request"); t != "" {
		return byName[t]
	}
	// The request struct itself was not supplied; fall back to the SDK's
	// <Operation>Request → <Operation>Details pairing.
	return byName[strings.TrimSuffix(op.Request, "Request")+"Details"]
}

// responseBodyModel returns the model an operation reads back as its body.
func responseBodyModel(op *Operation, byName map[string]Model, resource string) Model {
	if op != nil && op.Response != "" {
		if t := bodyTypeOf(byName[op.Response], "response"); t != "" {
			return byName[t]
		}
	}
	return byName[resource]
}

// bodyTypeOf returns the type name of the field a request contributes to / a
// response is present in the body as. The SDK embeds that model, so the field
// carries the tag and the type is the model's own name.
func bodyTypeOf(m Model, dir string) string {
	for _, f := range m.Fields {
		placement := f.ContributesTo
		if dir == "response" {
			placement = f.PresentIn
		}
		if placement == "body" {
			return lastIdent(f.Type)
		}
	}
	return ""
}

// pick keeps the first match, so a deterministic operation wins when the SDK
// offers more than one on the same path+verb.
func pick(have, candidate *Operation) *Operation {
	if have != nil && have.Name <= candidate.Name {
		return have
	}
	return candidate
}

// legacyActionModels is the pre-P2.5 action discovery: Change*/Add*/Remove*
// Details models whose name mentions the resource. Used only when no operation
// registry was supplied, since without paths there is nothing structural to key
// on.
func legacyActionModels(models []Model, resource string) []Model {
	var out []Model
	for _, m := range models {
		if actionModelRe.MatchString(m.Name) && strings.Contains(m.Name, resource) && !m.IsPolymorphic() {
			out = append(out, m)
		}
	}
	return out
}
