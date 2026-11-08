//go:build !wasip1

package main

// execute_registry_conformance_test.go pins the second layer of the execution
// boundary (M3): Execute runs only operations the registry defines, with exactly
// the registry's HTTP method and path template. The plan is the caller's input; it
// can name an operation, but it cannot invent one or override what the registry
// says that operation is.
//
// Every refusal is decided for the WHOLE plan before the first request is signed or
// sent, so a multi-step plan never half-executes. Assertions are on the outcome
// only: status, CIC error class, field path, and the count of sign/actuate calls
// seen by the host seam. This says nothing about who may run the plan (M4, Relay).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

const conformanceID = "ocid1.vcn.oc1..conformance"

func runPlan(t *testing.T, kind string, ops []providerOperation, reply string) (providerResult, *cicFlowMock) {
	t.Helper()
	mock := &cicFlowMock{Reply: replyWith(200, reply, nil)}
	restore := mock.wireHostSeam()
	defer restore()
	out, err := Execute(nil, marshalReq(t, executeRequest{
		Kind:    kind,
		Plan:    executionPlan{ProviderOperations: ops},
		Config:  schemaPayload{Data: json.RawMessage(`{"compartmentId":"ocid1.compartment..c","displayName":"prod"}`)},
		Binding: bindingWith(conformanceID),
	}))
	if err != nil {
		t.Fatalf("Execute returned a Go error: %v", err)
	}
	return decodeResult(t, out), mock
}

func wantPlanRefusal(t *testing.T, res providerResult, mock *cicFlowMock, index int) {
	t.Helper()
	if res.Status != "error" || res.Error == nil {
		t.Fatalf("status = %q, want an error", res.Status)
	}
	if res.Error.Class != classValidation {
		t.Errorf("error class = %q, want %q", res.Error.Class, classValidation)
	}
	if want := fmt.Sprintf("plan.provider_operations[%d]", index); res.Error.FieldPath != want {
		t.Errorf("field_path = %q, want %q", res.Error.FieldPath, want)
	}
	if mock.signCount() != 0 || mock.actuateCount() != 0 {
		t.Errorf("%d sign / %d actuate calls, want none: the whole plan must be refused before anything runs",
			mock.signCount(), mock.actuateCount())
	}
}

func TestExecuteRefusesAPlanStepThatTheRegistryDoesNotDefine(t *testing.T) {
	cases := []struct {
		name string
		op   providerOperation
	}{
		{"unknown name, arbitrary DELETE", providerOperation{Operation: "DeleteEverything", Method: "DELETE", Path: "/vcns/{vcnId}"}},
		{"unknown name, plain create path", providerOperation{Operation: "MakeVcn", Method: "POST", Path: "/vcns"}},
		{"empty name", providerOperation{Operation: "", Method: "DELETE", Path: "/vcns/{vcnId}"}},
		{"name of another kind", providerOperation{Operation: "LaunchInstance", Method: "POST", Path: "/instances"}},
		{"unknown name, empty method and path", providerOperation{Operation: "DeleteEverything"}},
		{"empty name, empty method and path", providerOperation{}},
		{"name with different case", providerOperation{Operation: "updatevcn", Method: "PUT", Path: "/vcns/{vcnId}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, mock := runPlan(t, bindingKind, []providerOperation{c.op}, `{"id":"ocid1.vcn.oc1..created"}`)
			wantPlanRefusal(t, res, mock, 0)
		})
	}
}

// A read is Observe's job: Plan never emits one and Execute does not run one, even
// though the registry defines it and the step matches the registry exactly (D-M3-1).
func TestExecuteRefusesAReadRoleStepEvenWhenItMatchesTheRegistry(t *testing.T) {
	for _, kind := range sortedKinds() {
		c := resourceContracts()[kind]
		name, op, ok := c.opByRole(roleRead)
		if !ok {
			t.Fatalf("%s has no read operation: the control would test nothing", kind)
		}
		t.Run(kind+"/"+name, func(t *testing.T) {
			mock := &cicFlowMock{Reply: replyWith(200, `{}`, nil)}
			restore := mock.wireHostSeam()
			defer restore()
			out, err := Execute(nil, marshalReq(t, executeRequest{
				Kind:    kind,
				Plan:    executionPlan{ProviderOperations: []providerOperation{{Operation: name, Method: op.method, Path: op.path}}},
				Config:  schemaPayload{Data: json.RawMessage(`{}`)},
				Binding: bindingWith("ocid1." + kindType(kind) + ".oc1..conformance"),
			}))
			if err != nil {
				t.Fatalf("Execute returned a Go error: %v", err)
			}
			wantPlanRefusal(t, decodeResult(t, out), mock, 0)
		})
	}
}

func TestExecuteRefusesAStepWhoseMethodOrPathDiffersFromTheRegistry(t *testing.T) {
	cases := []struct {
		name string
		op   providerOperation
	}{
		{"UpdateVcn with DELETE", providerOperation{Operation: "UpdateVcn", Method: "DELETE", Path: "/vcns/{vcnId}"}},
		{"DeleteVcn with GET", providerOperation{Operation: "DeleteVcn", Method: "GET", Path: "/vcns/{vcnId}"}},
		{"CreateVcn with PUT", providerOperation{Operation: "CreateVcn", Method: "PUT", Path: "/vcns"}},
		{"method in lower case", providerOperation{Operation: "UpdateVcn", Method: "put", Path: "/vcns/{vcnId}"}},
		{"UpdateVcn on another resource path", providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/subnets/{subnetId}"}},
		{"DeleteVcn on a parent path", providerOperation{Operation: "DeleteVcn", Method: "DELETE", Path: "/vcns"}},
		{"path with traversal", providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}/../../subnets/x"}},
		{"path with a suffix", providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}/x"}},
		{"path with a trailing slash", providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}/"}},
		{"placeholder renamed", providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{id}"}},
		{"query appended", providerOperation{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}?x=1"}},
		{"CreateVcn path with placeholder", providerOperation{Operation: "CreateVcn", Method: "POST", Path: "/vcns/{vcnId}"}},
		{"action path swapped for delete path", providerOperation{Operation: "ChangeVcnCompartment", Method: "POST", Path: "/vcns/{vcnId}"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, mock := runPlan(t, bindingKind, []providerOperation{c.op}, `{"id":"ocid1.vcn.oc1..created"}`)
			wantPlanRefusal(t, res, mock, 0)
		})
	}
}

func TestExecuteRefusesAStepWithAnEmptyMethodOrPath(t *testing.T) {
	for _, op := range []providerOperation{
		{Operation: "UpdateVcn", Method: "", Path: "/vcns/{vcnId}"},
		{Operation: "UpdateVcn", Method: "PUT", Path: ""},
		{Operation: "UpdateVcn"},
	} {
		t.Run(fmt.Sprintf("%q %q", op.Method, op.Path), func(t *testing.T) {
			res, mock := runPlan(t, bindingKind, []providerOperation{op}, `{}`)
			wantPlanRefusal(t, res, mock, 0)
		})
	}
}

// The valid steps that come BEFORE the offending one must not run: the check is
// over the whole plan, not step by step. The index names the offending step.
func TestAMixedPlanWithOneNonConformingStepRunsNoStep(t *testing.T) {
	good := []providerOperation{
		{Operation: "CreateVcn", Method: "POST", Path: "/vcns"},
		{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
	}
	bad := providerOperation{Operation: "DeleteVcn", Method: "PUT", Path: "/vcns/{vcnId}"}
	for _, pos := range []int{0, 1, 2} {
		t.Run(fmt.Sprintf("bad step at %d", pos), func(t *testing.T) {
			ops := append(append(append([]providerOperation{}, good[:min(pos, len(good))]...), bad), good[min(pos, len(good)):]...)
			res, mock := runPlan(t, bindingKind, ops, `{"id":"ocid1.vcn.oc1..created"}`)
			wantPlanRefusal(t, res, mock, pos)
		})
	}
}

// When two steps are wrong the refusal names the first.
func TestTheRefusalNamesTheFirstNonConformingStep(t *testing.T) {
	ops := []providerOperation{
		{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"},
		{Operation: "Nope", Method: "DELETE", Path: "/x"},
		{Operation: "UpdateVcn", Method: "DELETE", Path: "/vcns/{vcnId}"},
	}
	res, mock := runPlan(t, bindingKind, ops, `{}`)
	wantPlanRefusal(t, res, mock, 1)
}

// The registry check accepts every executable operation of every kind with its own
// method and path (a read is refused, see the test above). This is the check alone;
// which plans may run at all is the plan-binding gate's business (m4a_gate_test.go).
func TestRegistryCheckAcceptsEveryExecutableOperationWithItsOwnMethodAndPath(t *testing.T) {
	for _, kind := range sortedKinds() {
		c := resourceContracts()[kind]
		for name, op := range c.operations {
			if op.role == roleRead {
				continue
			}
			if pe := checkPlanAgainstRegistry(c, []providerOperation{{Operation: name, Method: op.method, Path: op.path}}); pe != nil {
				t.Errorf("%s/%s with its own method and path was refused: %+v", kind, name, pe)
			}
		}
	}
}

// Through Execute, every plan Plan can derive still runs, step by step, with exactly
// the method and URL the registry describes.
func TestExecuteStillRunsEveryDerivablePlanWithTheRegistrysMethodAndPath(t *testing.T) {
	type derivable struct{ name, desired, observed string }
	byKind := map[string][]derivable{
		"cic:network:vcn": {
			{"create", `{"compartmentId":"ocid1.compartment..c"}`, `{}`},
			{"update", `{"displayName":"new"}`, `{"displayName":"old"}`},
			{"action", `{"compartmentId":"ocid1.compartment..b"}`, `{"compartmentId":"ocid1.compartment..a"}`},
			{"replace", `{"dnsLabel":"a"}`, `{"dnsLabel":"b"}`},
		},
		"cic:network:subnet": {
			{"create", `{"compartmentId":"ocid1.compartment..c","vcnId":"ocid1.vcn..v"}`, `{}`},
			{"update", `{"displayName":"new"}`, `{"displayName":"old"}`},
			{"action", `{"compartmentId":"ocid1.compartment..b"}`, `{"compartmentId":"ocid1.compartment..a"}`},
		},
		"cic:compute:instance": {
			{"create", `{"compartmentId":"ocid1.compartment..c","availabilityDomain":"AD-1"}`, `{}`},
			{"update", `{"displayName":"new"}`, `{"displayName":"old"}`},
			{"action", `{"compartmentId":"ocid1.compartment..b"}`, `{"compartmentId":"ocid1.compartment..a"}`},
		},
	}
	for _, kind := range sortedKinds() {
		cases := byKind[kind]
		if len(cases) == 0 {
			t.Fatalf("%s has no derivable-plan case: the test would skip a kind", kind)
		}
		c := resourceContracts()[kind]
		for _, d := range cases {
			t.Run(kind+"/"+d.name, func(t *testing.T) {
				b := bindingWith("ocid1." + kindType(kind) + ".oc1..conformance")
				if d.name == "create" {
					b.ResourceID = ""
				}
				req := m4aRequest(t, kind, d.desired, d.observed, b)
				if len(req.Plan.ProviderOperations) == 0 {
					t.Fatalf("test setup: Plan derived no operation (%s)", req.Plan.Operation)
				}
				mock := &cicFlowMock{Reply: replyWith(200, `{"id":"ocid1.`+kindType(kind)+`.oc1..created"}`, nil)}
				restore := mock.wireHostSeam()
				defer restore()
				out, err := Execute(nil, marshalReq(t, req))
				if err != nil {
					t.Fatalf("Execute returned a Go error: %v", err)
				}
				res := decodeResult(t, out)
				if res.Status != "ok" {
					t.Fatalf("status = %q, want ok (error: %+v)", res.Status, res.Error)
				}
				ops := req.Plan.ProviderOperations
				if mock.signCount() != len(ops) || mock.actuateCount() != len(ops) {
					t.Fatalf("%d sign / %d actuate calls, want %d each (one per step)", mock.signCount(), mock.actuateCount(), len(ops))
				}
				for i, po := range ops {
					reg := c.operations[po.Operation]
					call := mock.Actuated[i]
					if call.Method != reg.method {
						t.Errorf("step %d (%s): method = %q, want the registry's %q", i, po.Operation, call.Method, reg.method)
					}
					id := b.ResourceID
					wantURL := "https://" + bindingHost + "/20160918" + templatePath(reg.path, id)
					if call.URL != wantURL {
						t.Errorf("step %d (%s): URL = %q, want %q", i, po.Operation, call.URL, wantURL)
					}
				}
			})
		}
	}
}

// kindType returns the OCID type segment of a kind ("cic:network:vcn" -> "vcn").
func kindType(kind string) string {
	i := strings.LastIndex(kind, ":")
	return kind[i+1:]
}

func sortedKinds() []string {
	var kinds []string
	for k := range resourceContracts() {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}
