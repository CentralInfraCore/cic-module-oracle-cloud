//go:build !wasip1

package main

// binding_resource_id_test.go pins the syntactic contract of the resource id a CALLER
// supplies in binding.resource_id (the first layer of the binding contract; M1).
//
// The id is later spliced into a SIGNED request path, so every operation that takes
// one must accept only a well-formed OCID of the kind's type, and must decide that
// BEFORE anything is signed or sent. The same table is run through all four entry
// points (Observe, Invoke, Destroy and the non-create steps of Execute), so the
// rule is one rule, not four.
//
// Assertions are on the outcome only: the result status, the CIC error class, the
// field path, and that the host seam saw no sign and no actuate call. They do not
// match message text. This says nothing about whether the caller may act on the
// resource, nor whether the operation is approved: those are other layers.

import (
	"encoding/json"
	"strings"
	"testing"
)

const bindingKind = "cic:network:vcn"

var bindingHost = "iaas.eu-frankfurt-1.oraclecloud.com"

func bindingWith(id string) execBinding {
	return execBinding{Host: bindingHost, BasePath: "/20160918", KeyID: "t/u/f", ResourceID: id}
}

// updateVcnOp is the real UpdateVcn operation of the registry (PUT /vcns/{vcnId}).
var updateVcnOp = providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"}

func marshalReq(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// bindingOps are the four entry points that take a caller-supplied resource id.
var bindingOps = []struct {
	name string
	call func(t *testing.T, id string) ([]byte, error)
}{
	{"Observe", func(t *testing.T, id string) ([]byte, error) {
		return Observe(nil, marshalReq(t, observeRequest{Kind: bindingKind, Binding: bindingWith(id)}))
	}},
	{"Invoke", func(t *testing.T, id string) ([]byte, error) {
		return Invoke(nil, marshalReq(t, invokeRequest{
			Kind: bindingKind, Operation: "ChangeVcnCompartment",
			Config:  schemaPayload{Data: json.RawMessage(`{"compartmentId":"ocid1.compartment..c"}`)},
			Binding: bindingWith(id),
		}))
	}},
	{"Destroy", func(t *testing.T, id string) ([]byte, error) {
		return Destroy(nil, marshalReq(t, destroyRequest{Kind: bindingKind, Binding: bindingWith(id)}))
	}},
	{"Execute (update step)", func(t *testing.T, id string) ([]byte, error) {
		// A consistent M4a request for a well-formed id; for a bad one, the same
		// request with the id replaced by hand, so the id check is what refuses it.
		build := func(id string) executeRequest {
			return m4aForOp(t, bindingKind, updateVcnOp, `{"displayName":"prod"}`, bindingWith(id))
		}
		req := build("ocid1.vcn.oc1..base")
		if wellFormedOCID(bindingKind, id) {
			req = build(id)
		} else {
			req.Binding.ResourceID = id
		}
		return Execute(nil, marshalReq(t, req))
	}},
}

func callBindingOp(t *testing.T, mock *cicFlowMock, call func(*testing.T, string) ([]byte, error), id string) providerResult {
	t.Helper()
	restore := mock.wireHostSeam()
	defer restore()
	out, err := call(t, id)
	if err != nil {
		t.Fatalf("the operation returned a Go error: %v", err)
	}
	return decodeResult(t, out)
}

func badBindingIDs() []struct{ name, id string } {
	return []struct{ name, id string }{
		{"path traversal", "ocid1.vcn.oc1..a/../../subnets/x"},
		{"query and fragment", "ocid1.vcn.oc1..a?x=1#f"},
		{"space inside", "ocid1.vcn.oc1..a b"},
		{"newline inside", "ocid1.vcn.oc1..a\nb"},
		{"brace placeholder", "ocid1.vcn.oc1..{a}"},
		{"percent escape", "ocid1.vcn.oc1..a%2fb"},
		{"non-ASCII", "ocid1.vcn.oc1.." + string(rune(0xe9))},
		{"another type", "ocid1.subnet.oc1..a"},
		{"bare prefix", "ocid1.vcn."},
		{"not an OCID at all", "vcn-42"},
		{"too long", "ocid1.vcn.oc1.." + strings.Repeat("a", 300)},
	}
}

func goodBindingIDs() []string {
	return []string{
		"ocid1.vcn..z",
		"ocid1.vcn.oc1..xyz",
		"ocid1.vcn.oc1.eu-frankfurt-1.amaaaaaaexamplevcn",
	}
}

func TestEveryOperationRefusesABadBindingResourceIDBeforeSigningAnything(t *testing.T) {
	for _, op := range bindingOps {
		for _, bad := range badBindingIDs() {
			t.Run(op.name+"/"+bad.name, func(t *testing.T) {
				mock := &cicFlowMock{Reply: replyWith(200, `{}`, nil)}
				res := callBindingOp(t, mock, op.call, bad.id)
				if res.Status != "error" || res.Error == nil {
					t.Fatalf("status = %q, want an error for id %q", res.Status, bad.id)
				}
				if res.Error.Class != classValidation {
					t.Errorf("error class = %q, want %q", res.Error.Class, classValidation)
				}
				if res.Error.FieldPath != "binding.resource_id" {
					t.Errorf("field_path = %q, want binding.resource_id", res.Error.FieldPath)
				}
				if mock.signCount() != 0 || mock.actuateCount() != 0 {
					t.Errorf("%d sign / %d actuate calls, want none: the id must be refused before anything is signed or sent",
						mock.signCount(), mock.actuateCount())
				}
			})
		}
	}
}

func TestEveryOperationAcceptsAWellFormedBindingResourceID(t *testing.T) {
	for _, op := range bindingOps {
		for _, id := range goodBindingIDs() {
			t.Run(op.name+"/"+id, func(t *testing.T) {
				mock := &cicFlowMock{Reply: replyWith(200, `{}`, nil)}
				res := callBindingOp(t, mock, op.call, id)
				if res.Status != "ok" {
					t.Fatalf("status = %q, want ok for id %q (error: %+v)", res.Status, id, res.Error)
				}
				if mock.signCount() != 1 || mock.actuateCount() != 1 {
					t.Fatalf("%d sign / %d actuate calls, want exactly 1 each", mock.signCount(), mock.actuateCount())
				}
				if url := mock.lastActuate(t).URL; !strings.Contains(url, id) {
					t.Errorf("request URL %q does not carry the id %q", url, id)
				}
			})
		}
	}
}

func TestAnEmptyBindingResourceIDIsRefusedByEveryOperationToo(t *testing.T) {
	for _, op := range bindingOps {
		t.Run(op.name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(200, `{}`, nil)}
			res := callBindingOp(t, mock, op.call, "")
			if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation {
				t.Fatalf("status %q error %+v, want a validation error for an empty id", res.Status, res.Error)
			}
			if mock.signCount() != 0 || mock.actuateCount() != 0 {
				t.Errorf("%d sign / %d actuate calls, want none", mock.signCount(), mock.actuateCount())
			}
		})
	}
}

// A plan that creates and then updates must not run its create step when the id the
// update needs is bad: the check happens before any step, not step by step.
func TestAMixedPlanWithABadBindingIDRunsNoStep(t *testing.T) {
	mock := &cicFlowMock{Reply: replyWith(200, `{"id":"ocid1.vcn.oc1..created"}`, nil)}
	restore := mock.wireHostSeam()
	defer restore()
	out, err := Execute(nil, marshalReq(t, executeRequest{
		Kind: bindingKind,
		Plan: executionPlan{ProviderOperations: []providerOperation{
			{Operation: "CreateVcn", Method: "POST", Path: "/vcns"}, updateVcnOp,
		}},
		Config:  schemaPayload{Data: json.RawMessage(`{"compartmentId":"ocid1.compartment..c","displayName":"prod"}`)},
		Binding: bindingWith("ocid1.vcn.oc1..a/../../x"),
	}))
	if err != nil {
		t.Fatalf("Execute returned a Go error: %v", err)
	}
	res := decodeResult(t, out)
	if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation {
		t.Fatalf("status %q error %+v, want a validation error", res.Status, res.Error)
	}
	if mock.actuateCount() != 0 {
		t.Errorf("%d provider requests: the create step ran before the id was refused", mock.actuateCount())
	}
}

// A create has no id to splice (its path is /vcns), so the M1 id check does not ask
// for one, and a create with no resource id runs. A create that names a resource id
// is not a create at all: the plan's lifecycle (M4a) refuses it, because a resource
// that does not exist yet has no id. M1 alone would ignore it; the refusal is M4a's.
func TestACreateRunsWithoutAResourceIDAndIsRefusedWithOne(t *testing.T) {
	create := providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns"}
	t.Run("no resource id", func(t *testing.T) {
		mock := &cicFlowMock{Reply: replyWith(200, `{"id":"ocid1.vcn.oc1..created"}`, nil)}
		restore := mock.wireHostSeam()
		defer restore()
		req := m4aForOp(t, bindingKind, create, `{"compartmentId":"ocid1.compartment..c"}`, bindingWith(""))
		out, err := Execute(nil, marshalReq(t, req))
		if err != nil {
			t.Fatalf("Execute returned a Go error: %v", err)
		}
		if res := decodeResult(t, out); res.Status != "ok" {
			t.Fatalf("status = %q, want ok (error: %+v)", res.Status, res.Error)
		}
		if mock.actuateCount() != 1 {
			t.Errorf("%d provider requests, want exactly 1", mock.actuateCount())
		}
	})
	for _, id := range []string{"not-an-ocid", "ocid1.vcn.oc1..existing"} {
		t.Run("resource id "+id, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(200, `{"id":"ocid1.vcn.oc1..created"}`, nil)}
			restore := mock.wireHostSeam()
			defer restore()
			req := m4aForOp(t, bindingKind, create, `{"compartmentId":"ocid1.compartment..c"}`, bindingWith(""))
			req.Binding.ResourceID = id // changed by hand after the plan was built
			out, err := Execute(nil, marshalReq(t, req))
			if err != nil {
				t.Fatalf("Execute returned a Go error: %v", err)
			}
			res := decodeResult(t, out)
			if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation {
				t.Fatalf("status %q error %+v, want a validation error", res.Status, res.Error)
			}
			if mock.signCount() != 0 || mock.actuateCount() != 0 {
				t.Errorf("%d sign / %d actuate calls, want none", mock.signCount(), mock.actuateCount())
			}
		})
	}
}

func TestTheRefusalDoesNotCarryTheRawBadID(t *testing.T) {
	mock := &cicFlowMock{Reply: replyWith(200, `{}`, nil)}
	res := callBindingOp(t, mock, bindingOps[0].call, "ocid1.vcn.oc1..a\nb"+strings.Repeat("x", 500))
	if res.Error == nil {
		t.Fatalf("want a refusal")
	}
	if strings.Contains(res.Error.Message, "\n") || len(res.Error.Message) > 400 {
		t.Errorf("the refusal message must stay one short line, got %d bytes: %q", len(res.Error.Message), res.Error.Message)
	}
}

// The decision must follow the path that is actually executed, not the role the
// registry gives the operation NAME: a plan can carry the name of a create with a
// path that splices binding.resource_id (review of PR #39, F1).
func TestACreateNamedStepWhosePathSplicesTheIDIsRefusedOnABadID(t *testing.T) {
	cases := []struct {
		name string
		op   providerOperation
	}{
		{"create name, PUT with placeholder", providerOperation{Operation: "CreateVcn", Method: "PUT", Path: "/vcns/{vcnId}"}},
		{"create name, DELETE with placeholder", providerOperation{Operation: "CreateVcn", Method: "DELETE", Path: "/vcns/{vcnId}"}},
		{"create name, placeholder after a prefix", providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns/x/{id}/y"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(200, `{"id":"ocid1.vcn.oc1..created"}`, nil)}
			restore := mock.wireHostSeam()
			defer restore()
			out, err := Execute(nil, marshalReq(t, executeRequest{
				Kind:    bindingKind,
				Plan:    executionPlan{ProviderOperations: []providerOperation{c.op}},
				Config:  schemaPayload{Data: json.RawMessage(`{"compartmentId":"ocid1.compartment..c"}`)},
				Binding: bindingWith("ocid1.vcn.oc1..a/../../x"),
			}))
			if err != nil {
				t.Fatalf("Execute returned a Go error: %v", err)
			}
			res := decodeResult(t, out)
			if res.Status != "error" || res.Error == nil || res.Error.Class != classValidation || res.Error.FieldPath != "binding.resource_id" {
				t.Fatalf("status %q error %+v, want a binding.resource_id validation error", res.Status, res.Error)
			}
			if mock.signCount() != 0 || mock.actuateCount() != 0 {
				t.Errorf("%d sign / %d actuate calls, want none", mock.signCount(), mock.actuateCount())
			}
		})
	}
}
