package ociextract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// The committed schemas in module/schemas/core are the only OCI coverage this module
// actually ships, and they are generated from a pinned SDK that CI cannot reach
// (no network). So the guard is a frozen expectation of their surface, written
// out here rather than derived from the files — a check that read the answer
// from the artifact it is checking would pass no matter what the extractor did.
//
// vcn and subnet are the two resources proven end-to-end against a real tenancy
// (docs/design/manual-verification.md). Generalising the extractor must not cost
// them a single field, policy, or operation.

type resourceExpectation struct {
	file       string
	resource   string
	required   []string
	configPols map[string]string // json field -> x-cic-policy
	stateProps int
	operations map[string]opExpectation
}

type opExpectation struct {
	method     string
	path       string
	pathParams []string
	role       string
}

var committedSchemas = []resourceExpectation{
	{
		file:     "vcn.json",
		resource: "Vcn",
		required: []string{"compartmentId"},
		configPols: map[string]string{
			"byoipv6CidrDetails":           "input-only",
			"cidrBlock":                    "create-only",
			"cidrBlocks":                   "create-only",
			"compartmentId":                "action-managed",
			"definedTags":                  "mutable",
			"displayName":                  "mutable",
			"dnsLabel":                     "create-only",
			"freeformTags":                 "mutable",
			"ipv6PrivateCidrBlocks":        "create-only",
			"isIpv6Enabled":                "input-only",
			"isOracleGuaAllocationEnabled": "input-only",
			"isZprOnly":                    "mutable",
			"securityAttributes":           "mutable",
		},
		stateProps: 19,
		operations: map[string]opExpectation{
			"GetVcn":               {"GET", "/vcns/{vcnId}", []string{"vcnId"}, RoleRead},
			"CreateVcn":            {"POST", "/vcns", nil, RoleCreate},
			"UpdateVcn":            {"PUT", "/vcns/{vcnId}", []string{"vcnId"}, RoleUpdate},
			"DeleteVcn":            {"DELETE", "/vcns/{vcnId}", []string{"vcnId"}, RoleDelete},
			"ChangeVcnCompartment": {"POST", "/vcns/{vcnId}/actions/changeCompartment", []string{"vcnId"}, RoleAction},
			"AddVcnCidr":           {"POST", "/vcns/{vcnId}/actions/addCidr", []string{"vcnId"}, RoleAction},
		},
	},
	{
		file:     "subnet.json",
		resource: "Subnet",
		required: []string{"compartmentId", "vcnId"},
		configPols: map[string]string{
			"availabilityDomain":      "create-only",
			"cidrBlock":               "mutable",
			"compartmentId":           "action-managed",
			"definedTags":             "mutable",
			"dhcpOptionsId":           "mutable",
			"displayName":             "mutable",
			"dnsLabel":                "create-only",
			"freeformTags":            "mutable",
			"ipv4CidrBlocks":          "create-only",
			"ipv6CidrBlock":           "mutable",
			"ipv6CidrBlocks":          "mutable",
			"prohibitInternetIngress": "create-only",
			"prohibitPublicIpOnVnic":  "create-only",
			"routeTableId":            "mutable",
			"securityListIds":         "mutable",
			"vcnId":                   "create-only",
		},
		stateProps: 23,
		operations: map[string]opExpectation{
			"GetSubnet":               {"GET", "/subnets/{subnetId}", []string{"subnetId"}, RoleRead},
			"CreateSubnet":            {"POST", "/subnets", nil, RoleCreate},
			"UpdateSubnet":            {"PUT", "/subnets/{subnetId}", []string{"subnetId"}, RoleUpdate},
			"DeleteSubnet":            {"DELETE", "/subnets/{subnetId}", []string{"subnetId"}, RoleDelete},
			"ChangeSubnetCompartment": {"POST", "/subnets/{subnetId}/actions/changeCompartment", []string{"subnetId"}, RoleAction},
		},
	},
}

func TestCommittedSchemaCoverageUnchanged(t *testing.T) {
	for _, want := range committedSchemas {
		t.Run(want.file, func(t *testing.T) {
			path := filepath.Join("..", "..", "module", "schemas", "core", want.file)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			var bundle struct {
				Config struct {
					Resource   string                            `json:"x-cic-resource"`
					Required   []string                          `json:"required"`
					Properties map[string]map[string]interface{} `json:"properties"`
				} `json:"config"`
				State struct {
					Properties map[string]interface{} `json:"properties"`
				} `json:"state"`
				Operations map[string]struct {
					Method     string   `json:"method"`
					Path       string   `json:"path"`
					PathParams []string `json:"path_params"`
					Role       string   `json:"role"`
				} `json:"operations"`
			}
			if err := json.Unmarshal(raw, &bundle); err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}

			if bundle.Config.Resource != want.resource {
				t.Errorf("x-cic-resource = %q, want %q", bundle.Config.Resource, want.resource)
			}
			if got := append([]string(nil), bundle.Config.Required...); !equalSorted(got, want.required) {
				t.Errorf("required = %v, want %v", got, want.required)
			}

			// Every config field, and its policy, must still be there. An extra
			// field is a change too: it means the surface moved without review.
			gotPols := map[string]string{}
			for name, prop := range bundle.Config.Properties {
				p, _ := prop["x-cic-policy"].(string)
				gotPols[name] = p
			}
			if !reflect.DeepEqual(gotPols, want.configPols) {
				for name, wp := range want.configPols {
					if gp, ok := gotPols[name]; !ok {
						t.Errorf("config field %q disappeared (policy was %s)", name, wp)
					} else if gp != wp {
						t.Errorf("config field %q policy = %s, want %s", name, gp, wp)
					}
				}
				for name := range gotPols {
					if _, ok := want.configPols[name]; !ok {
						t.Errorf("config field %q is new — the surface changed", name)
					}
				}
			}

			if got := len(bundle.State.Properties); got != want.stateProps {
				t.Errorf("state properties = %d, want %d", got, want.stateProps)
			}

			for name, wo := range want.operations {
				got, ok := bundle.Operations[name]
				if !ok {
					t.Errorf("operation %q disappeared", name)
					continue
				}
				if got.Method != wo.method || got.Path != wo.path {
					t.Errorf("operation %q = %s %s, want %s %s", name, got.Method, got.Path, wo.method, wo.path)
				}
				if !reflect.DeepEqual(got.PathParams, wo.pathParams) {
					t.Errorf("operation %q path_params = %v, want %v", name, got.PathParams, wo.pathParams)
				}
				if got.Role != wo.role {
					t.Errorf("operation %q role = %q, want %q", name, got.Role, wo.role)
				}
			}
			for name := range bundle.Operations {
				if _, ok := want.operations[name]; !ok {
					t.Errorf("operation %q is new — the surface changed", name)
				}
			}
		})
	}
}

// Re-resolving the committed resources from a fixture must still pick the
// Create/Update/Delete lifecycle by name-independent means. This is the half the
// golden file cannot prove: that the generalised resolver, not a leftover
// special case, is what produces the vcn shape.
func TestVcnFixtureStillResolvesStructurally(t *testing.T) {
	models, ops := loadFixture(t, "testdata/vcn_client.go", "testdata/vcn.go")
	res := Resolve(models, ops, "Vcn")

	if !res.Complete() {
		t.Fatalf("Vcn did not resolve cleanly: %v", res.Unresolved)
	}
	if res.CreateOp == nil || res.CreateOp.Name != "CreateVcn" {
		t.Errorf("create op = %+v, want CreateVcn", res.CreateOp)
	}
	if res.DeleteOp == nil || res.DeleteOp.Name != "DeleteVcn" {
		t.Errorf("delete op = %+v, want DeleteVcn", res.DeleteOp)
	}
	if res.Create.Name != "CreateVcnDetails" {
		t.Errorf("create model = %q, want CreateVcnDetails", res.Create.Name)
	}

	pols := map[string]string{}
	for _, p := range PolicyOf(res) {
		pols[p.Field] = p.Policy
	}
	// The rule the whole policy step exists for: absent from Update does not
	// mean immutable when an action governs the field.
	if pols["compartmentId"] != PolicyAction {
		t.Errorf("compartmentId policy = %q, want %q", pols["compartmentId"], PolicyAction)
	}
	if pols["dnsLabel"] != PolicyCreateOnly {
		t.Errorf("dnsLabel policy = %q, want %q", pols["dnsLabel"], PolicyCreateOnly)
	}
}

func equalSorted(a, b []string) bool {
	a, b = append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
	return reflect.DeepEqual(a, b)
}
