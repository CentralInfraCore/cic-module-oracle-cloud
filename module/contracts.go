// Package main — contracts.go embeds the generated CIC payload schemas (P2.3,
// tools/oci-extract) and exposes them as resource contracts the provider ABI
// validates and plans against. This is the bridge that turns the P0.1 validate
// and plan scaffolds into real behaviour: the schemas are generated from the
// pinned OCI SDK, committed here, and compiled into the guest.
//
// No build tag: shared by the wasip1 guest and host-side go test. Regenerate the
// embedded JSON with `make oci.generate` (needs the pinned SDK).
package main

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed schemas/vcn.json
var vcnSchemaJSON []byte

//go:embed schemas/subnet.json
var subnetSchemaJSON []byte

// embeddedSchemas is every generated {config, state} bundle compiled in. The
// resource kind is the config $id minus the "-config" suffix.
var embeddedSchemas = [][]byte{vcnSchemaJSON, subnetSchemaJSON}

// fieldDesc is one config field's contract: its CIC policy and coarse JSON type.
type fieldDesc struct {
	policy   string // mutable | create-only | action-managed | input-only
	jsonType string // string|integer|boolean|array|object|number, or "" if unmapped
}

// resourceContract is the settable (config) surface of one resource kind.
type resourceContract struct {
	kind     string
	required []string
	fields   map[string]fieldDesc
}

var (
	contractCache map[string]resourceContract
	contractDone  bool
)

// resourceContracts lazily parses the embedded schemas into contracts. Lazy (not
// init()) on purpose: the relay host instantiates the guest without running
// _start, so package init() never runs — but //go:embed data is static and
// available. Single-threaded guest (-scheduler=none), so no lock is needed.
func resourceContracts() map[string]resourceContract {
	if contractDone {
		return contractCache
	}
	contractDone = true
	contractCache = map[string]resourceContract{}
	for _, raw := range embeddedSchemas {
		var bundle struct {
			Config struct {
				ID         string   `json:"$id"`
				Required   []string `json:"required"`
				Properties map[string]struct {
					Type   string `json:"type"`
					Policy string `json:"x-cic-policy"`
				} `json:"properties"`
			} `json:"config"`
		}
		if err := json.Unmarshal(raw, &bundle); err != nil || bundle.Config.ID == "" {
			continue
		}
		kind := strings.TrimSuffix(bundle.Config.ID, "-config")
		fields := make(map[string]fieldDesc, len(bundle.Config.Properties))
		for name, p := range bundle.Config.Properties {
			fields[name] = fieldDesc{policy: p.Policy, jsonType: p.Type}
		}
		contractCache[kind] = resourceContract{kind: kind, required: bundle.Config.Required, fields: fields}
	}
	return contractCache
}
