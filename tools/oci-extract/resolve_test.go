package ociextract

import (
	"reflect"
	"strings"
	"testing"
)

// loadFixture reads a client file and any number of model files into a registry.
func loadFixture(t *testing.T, clientFile string, modelFiles ...string) ([]Model, []Operation) {
	t.Helper()
	var ops []Operation
	if clientFile != "" {
		var err error
		ops, err = ExtractClientFile(clientFile)
		if err != nil {
			t.Fatalf("ExtractClientFile(%s): %v", clientFile, err)
		}
	}
	var models []Model
	for _, f := range modelFiles {
		ms, err := ExtractFile(f)
		if err != nil {
			t.Fatalf("ExtractFile(%s): %v", f, err)
		}
		models = append(models, ms...)
	}
	return models, ops
}

// The lifecycle verbs are Launch and Terminate, not Create and Delete. Name
// templates find neither; the HTTP surface finds both.
func TestResolveAlternateLifecycleVerbs(t *testing.T) {
	models, ops := loadFixture(t, "testdata/compute_client.go", "testdata/instance.go")
	res := Resolve(models, ops, "Instance")

	if !res.Complete() {
		t.Fatalf("Instance did not resolve cleanly: %v", res.Unresolved)
	}
	for _, tc := range []struct {
		role string
		op   *Operation
		want string
	}{
		{RoleRead, res.ReadOp, "GetInstance"},
		{RoleCreate, res.CreateOp, "LaunchInstance"},
		{RoleUpdate, res.UpdateOp, "UpdateInstance"},
		{RoleDelete, res.DeleteOp, "TerminateInstance"},
	} {
		if tc.op == nil {
			t.Errorf("%s operation not resolved, want %s", tc.role, tc.want)
			continue
		}
		if tc.op.Name != tc.want {
			t.Errorf("%s operation = %s, want %s", tc.role, tc.op.Name, tc.want)
		}
	}

	// The create model comes from the create operation's body, so it is
	// LaunchInstanceDetails even though nothing is named CreateInstanceDetails.
	if res.Create.Name != "LaunchInstanceDetails" {
		t.Errorf("create model = %q, want LaunchInstanceDetails", res.Create.Name)
	}
	if res.Read.Name != "Instance" {
		t.Errorf("read model = %q, want Instance", res.Read.Name)
	}

	// InstanceAction POSTs to the resource path too; the PUT must win for update.
	if res.UpdateOp.HTTPMethod != "PUT" {
		t.Errorf("update resolved to %s, want the PUT", res.UpdateOp.HTTPMethod)
	}

	// The regression this guards: name-template resolution silently produced a
	// config schema with no required fields at all.
	config, _ := ResourceSchemasFrom(res, "cic:compute:instance", "v0.1.0")
	req, _ := config["required"].([]string)
	if !reflect.DeepEqual(req, []string{"availabilityDomain", "compartmentId"}) {
		t.Errorf("required = %v, want [availabilityDomain compartmentId]", req)
	}
}

// Name-template resolution is what the structural route replaced: it finds no
// create model here, and must say so rather than emit an empty create surface.
func TestResolveWithoutOperationsReportsMissingCreate(t *testing.T) {
	models, _ := loadFixture(t, "", "testdata/instance.go")
	res := Resolve(models, nil, "Instance")

	if res.Create.Name != "" {
		t.Errorf("create model = %q, want none via name convention", res.Create.Name)
	}
	if res.Complete() {
		t.Fatal("resolution reported complete despite having no create model")
	}
	config, _ := ResourceSchemasFrom(res, "cic:compute:instance", "v0.1.0")
	if _, ok := config["required"]; ok {
		t.Error("config carries a required list built from no create model")
	}
}

// A resource addressed by two path parameters, neither an id, and updated with
// POST because the service has no PUT on that path.
func TestResolveMultiPathParamResource(t *testing.T) {
	models, ops := loadFixture(t, "testdata/bucket_client.go", "testdata/bucket.go")
	res := Resolve(models, ops, "Bucket")

	if !res.Complete() {
		t.Fatalf("Bucket did not resolve cleanly: %v", res.Unresolved)
	}
	if res.UpdateOp == nil || res.UpdateOp.Name != "UpdateBucket" || res.UpdateOp.HTTPMethod != "POST" {
		t.Errorf("update op = %+v, want UpdateBucket via POST", res.UpdateOp)
	}
	if res.CreateOp == nil || res.CreateOp.HTTPPath != "/n/{namespaceName}/b" {
		t.Errorf("create op = %+v, want POST on /n/{namespaceName}/b", res.CreateOp)
	}

	opMap := OperationMap(res)
	get, ok := opMap["GetBucket"]
	if !ok {
		t.Fatal("GetBucket missing from the operation map")
	}
	want := []string{"namespaceName", "bucketName"}
	if got, _ := get["path_params"].([]string); !reflect.DeepEqual(got, want) {
		t.Errorf("GetBucket path_params = %v, want %v", got, want)
	}
	// Creating needs the namespace even though it is not the resource's own id.
	create := opMap["CreateBucket"]
	if got, _ := create["path_params"].([]string); !reflect.DeepEqual(got, []string{"namespaceName"}) {
		t.Errorf("CreateBucket path_params = %v, want [namespaceName]", got)
	}
}

// A polymorphic create body must be reported, never mistaken for a struct with
// no fields.
func TestResolveReportsPolymorphicCreateModel(t *testing.T) {
	models, _ := loadFixture(t, "", "testdata/polymorphic.go")

	var iface Model
	for _, m := range models {
		if m.Name == "CreateBackupDestinationDetails" {
			iface = m
		}
	}
	if !iface.IsPolymorphic() {
		t.Fatalf("CreateBackupDestinationDetails kind = %q, want interface", iface.Kind)
	}

	res := Resolve(models, nil, "BackupDestination")
	if res.Complete() {
		t.Fatal("polymorphic create model reported as fully resolved")
	}
	var found bool
	for _, u := range res.Unresolved {
		if strings.Contains(u, "polymorphic") && strings.Contains(u, "CreateBackupDestinationDetails") {
			found = true
		}
	}
	if !found {
		t.Errorf("Unresolved = %v, want a note naming the polymorphic create model", res.Unresolved)
	}
}

// An operation with no request object at all, whose wire call is built by a
// package function rather than a method on the request.
func TestExtractRequestLessOperation(t *testing.T) {
	ops, err := ExtractClientFile("testdata/identity_client.go")
	if err != nil {
		t.Fatalf("ExtractClientFile: %v", err)
	}
	byName := map[string]Operation{}
	for _, o := range ops {
		byName[o.Name] = o
	}

	lr, ok := byName["ListRegions"]
	if !ok {
		t.Fatal("ListRegions dropped: an operation with no *Request parameter")
	}
	if lr.HTTPMethod != "GET" || lr.HTTPPath != "/regions" {
		t.Errorf("ListRegions = %s %s, want GET /regions", lr.HTTPMethod, lr.HTTPPath)
	}
	if lr.Request != "" {
		t.Errorf("ListRegions request = %q, want empty", lr.Request)
	}
	if _, ok := byName["CreateUser"]; !ok {
		t.Error("CreateUser missing: the ordinary shape must still resolve")
	}
	if _, ok := byName["SetRegion"]; ok {
		t.Error("SetRegion is client plumbing and must not be an operation")
	}
}

// The audit is the machine-checkable form of "N/N resolved, 0 missing".
func TestAuditClientFileCoverage(t *testing.T) {
	for _, f := range []string{
		"testdata/vcn_client.go",
		"testdata/compute_client.go",
		"testdata/bucket_client.go",
		"testdata/identity_client.go",
	} {
		a, err := AuditClientFile(f)
		if err != nil {
			t.Fatalf("AuditClientFile(%s): %v", f, err)
		}
		if a.Candidates == 0 {
			t.Errorf("%s: no operation candidates found", f)
		}
		if a.Resolved != a.Candidates {
			t.Errorf("%s: %d/%d resolved, unresolved: %v", f, a.Resolved, a.Candidates, a.Unresolved)
		}
		if len(a.OrphanPrivate) > 0 {
			t.Errorf("%s: private wire methods with no exported half: %v", f, a.OrphanPrivate)
		}
	}
}

func TestPathParams(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/vcns", nil},
		{"/vcns/{vcnId}", []string{"vcnId"}},
		{"/n/{namespaceName}/b/{bucketName}", []string{"namespaceName", "bucketName"}},
		{"/catalogs/{catalogId}/dataAssets/{dataAssetKey}/entities/{entityKey}", []string{"catalogId", "dataAssetKey", "entityKey"}},
	} {
		if got := PathParams(tc.path); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("PathParams(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}
