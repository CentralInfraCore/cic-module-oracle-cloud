//go:build !wasip1

package main

// contracts_test.go proves renderBody keys off the schema's role field, not the
// operation's Go name, using an Instance-shaped fixture whose lifecycle verbs
// are NOT Create/Update/Delete<Resource>: LaunchInstance creates, UpdateInstance
// updates, TerminateInstance deletes (module/testdata/instance-fixture.json,
// generated from tools/oci-extract/testdata/{instance.go,compute_client.go} —
// the same fixture oci-extract-generalize proved the extractor resolves
// structurally). Before this job's fix, renderBody matched on
// strings.HasPrefix(po.Operation, "Create"/"Update"/"Delete"): none of these
// names match, so LaunchInstance fell to the action branch and rendered an
// empty body — the exact failure observed against real OCI (HTTP 400
// CannotParseRequest on LaunchInstance).

import (
	"encoding/json"
	"os"
	"testing"
)

func loadInstanceFixture(t *testing.T) resourceContract {
	t.Helper()
	raw, err := os.ReadFile("testdata/instance-fixture.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	kind, c, ok := parseContractBundle(raw)
	if !ok || kind != "cic:compute:instance" {
		t.Fatalf("parseContractBundle(instance fixture) = %q, %v, want cic:compute:instance, true", kind, ok)
	}
	return c
}

// instanceConfig is every settable field on the fixture's config surface.
func instanceConfig() map[string]json.RawMessage {
	return map[string]json.RawMessage{
		"availabilityDomain": json.RawMessage(`"AD-1"`),
		"compartmentId":      json.RawMessage(`"ocid1.compartment.a"`),
		"displayName":        json.RawMessage(`"web-1"`),
		"freeformTags":       json.RawMessage(`{"env":"dev"}`),
		"shape":              json.RawMessage(`"VM.Standard.E2.1.Micro"`),
	}
}

// TestRenderBodyAltVerbCreate is the fixture-level proof for the bug found
// against real OCI: a create operation named LaunchInstance (not
// CreateInstance) must still render the full settable-field body.
func TestRenderBodyAltVerbCreate(t *testing.T) {
	c := loadInstanceFixture(t)
	po := providerOperation{Operation: "LaunchInstance", Method: "POST", Path: "/instances"}
	body := renderBody(c, po, instanceConfig())

	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("LaunchInstance body is not valid JSON: %v (body: %s)", err, body)
	}
	for _, field := range []string{"availabilityDomain", "compartmentId", "displayName", "freeformTags", "shape"} {
		if _, ok := got[field]; !ok {
			t.Errorf("LaunchInstance body missing field %q — want the full create surface, got %s", field, body)
		}
	}
}

// TestRenderBodyAltVerbDelete is the fixture-level proof for the delete side: a
// delete operation named TerminateInstance (not DeleteInstance) must render nil,
// not an empty-but-present body.
func TestRenderBodyAltVerbDelete(t *testing.T) {
	c := loadInstanceFixture(t)
	po := providerOperation{Operation: "TerminateInstance", Method: "DELETE", Path: "/instances/{instanceId}"}
	body := renderBody(c, po, instanceConfig())

	if body != nil {
		t.Errorf("TerminateInstance body = %s, want nil", body)
	}
}

// TestRenderBodyAltVerbUpdate proves the update side keys off role too: an
// UpdateInstance PUT carries only the mutable fields, never the create-only or
// action-managed ones.
func TestRenderBodyAltVerbUpdate(t *testing.T) {
	c := loadInstanceFixture(t)
	po := providerOperation{Operation: "UpdateInstance", Method: "PUT", Path: "/instances/{instanceId}"}
	body := renderBody(c, po, instanceConfig())

	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("UpdateInstance body is not valid JSON: %v (body: %s)", err, body)
	}
	for _, field := range []string{"displayName", "freeformTags", "shape"} {
		if _, ok := got[field]; !ok {
			t.Errorf("UpdateInstance body missing mutable field %q, got %s", field, body)
		}
	}
	for _, field := range []string{"availabilityDomain", "compartmentId"} {
		if _, ok := got[field]; ok {
			t.Errorf("UpdateInstance body carries non-mutable field %q, want it excluded: %s", field, body)
		}
	}
}
