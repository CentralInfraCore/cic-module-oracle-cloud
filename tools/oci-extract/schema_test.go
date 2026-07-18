package ociextract

import (
	"encoding/json"
	"testing"
)

func vcnSchemas(t *testing.T) (config, state map[string]interface{}) {
	t.Helper()
	models, err := ExtractFile("testdata/vcn.go")
	if err != nil {
		t.Fatalf("ExtractFile: %v", err)
	}
	return ResourceSchemas(models, "Vcn", "cic:network:vcn", "v0.1.0")
}

func props(t *testing.T, schema map[string]interface{}) map[string]interface{} {
	t.Helper()
	p, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema has no properties map")
	}
	return p
}

func TestConfigSchemaSurface(t *testing.T) {
	config, _ := vcnSchemas(t)

	if config["$id"] != "cic:network:vcn-config" {
		t.Errorf("$id = %v, want cic:network:vcn-config", config["$id"])
	}
	if config["additionalProperties"] != false {
		t.Errorf("additionalProperties = %v, want false", config["additionalProperties"])
	}

	p := props(t, config)
	// Settable fields present; output-only fields absent.
	for _, f := range []string{"compartmentId", "displayName", "cidrBlocks", "dnsLabel", "freeformTags"} {
		if _, ok := p[f]; !ok {
			t.Errorf("config missing settable field %q", f)
		}
	}
	for _, f := range []string{"id", "lifecycleState", "timeCreated"} {
		if _, ok := p[f]; ok {
			t.Errorf("config must not contain output-only field %q", f)
		}
	}

	// Policy annotations.
	if got := p["compartmentId"].(map[string]interface{})["x-cic-policy"]; got != "action-managed" {
		t.Errorf("compartmentId x-cic-policy = %v, want action-managed", got)
	}
	if got := p["compartmentId"].(map[string]interface{})["x-cic-action"]; got != "ChangeVcnCompartmentDetails" {
		t.Errorf("compartmentId x-cic-action = %v, want ChangeVcnCompartmentDetails", got)
	}
	if got := p["dnsLabel"].(map[string]interface{})["x-cic-policy"]; got != "create-only" {
		t.Errorf("dnsLabel x-cic-policy = %v, want create-only", got)
	}
	if got := p["displayName"].(map[string]interface{})["x-cic-policy"]; got != "mutable" {
		t.Errorf("displayName x-cic-policy = %v, want mutable", got)
	}

	// Types.
	if got := p["cidrBlocks"].(map[string]interface{})["type"]; got != "array" {
		t.Errorf("cidrBlocks type = %v, want array", got)
	}
	if got := p["freeformTags"].(map[string]interface{})["type"]; got != "object" {
		t.Errorf("freeformTags type = %v, want object", got)
	}
	if got := p["displayName"].(map[string]interface{})["type"]; got != "string" {
		t.Errorf("displayName type = %v, want string", got)
	}

	// required = the create model's mandatory fields (compartmentId only).
	req, _ := config["required"].([]string)
	if len(req) != 1 || req[0] != "compartmentId" {
		t.Errorf("required = %v, want [compartmentId]", config["required"])
	}
}

func TestStateSchemaSurface(t *testing.T) {
	_, state := vcnSchemas(t)

	if state["$id"] != "cic:network:vcn-state" {
		t.Errorf("$id = %v, want cic:network:vcn-state", state["$id"])
	}
	p := props(t, state)
	// Everything read back is present; a write-only field (freeformTags, not in
	// the read model) is absent.
	for _, f := range []string{"id", "compartmentId", "displayName", "dnsLabel", "lifecycleState", "timeCreated"} {
		if _, ok := p[f]; !ok {
			t.Errorf("state missing read field %q", f)
		}
	}
	if _, ok := p["freeformTags"]; ok {
		t.Errorf("state must not contain freeformTags (not read back)")
	}
	if got := p["lifecycleState"].(map[string]interface{})["x-cic-policy"]; got != "provider-computed" {
		t.Errorf("lifecycleState x-cic-policy = %v, want provider-computed", got)
	}
}

func TestSchemasMarshalDeterministically(t *testing.T) {
	config, state := vcnSchemas(t)
	for _, s := range []map[string]interface{}{config, state} {
		b1, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		b2, _ := json.Marshal(s)
		if string(b1) != string(b2) {
			t.Errorf("schema does not marshal deterministically")
		}
		// Must be valid JSON that round-trips to an object.
		var back map[string]interface{}
		if err := json.Unmarshal(b1, &back); err != nil {
			t.Errorf("schema is not valid JSON: %v", err)
		}
	}
}
