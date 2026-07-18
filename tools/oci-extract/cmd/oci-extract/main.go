// Command oci-extract reads OCI Go SDK source files and prints the extracted
// registry as JSON. Build-time tool for the schema pipeline (P2.2).
//
//	go run ./cmd/oci-extract <file.go> [<file.go> ...]
//
// A *_client.go file yields operations (method + HTTP verb/path + request/
// response types); any other file yields models (structs → fields). The two are
// emitted together as {operations, models}. JSON (not YAML) keeps this
// stdlib-only — no external dependency, nothing to vendor, and canonical output
// the later pipeline steps can hash.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	ociextract "github.com/CentralInfraCore/cic-module-oracle-cloud/tools/oci-extract"
)

type registry struct {
	Operations []ociextract.Operation `json:"operations"`
	Models     []ociextract.Model     `json:"models"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: oci-extract <file.go> [<file.go> ...]")
		os.Exit(2)
	}
	var reg registry
	for _, path := range os.Args[1:] {
		if strings.HasSuffix(path, "_client.go") {
			ops, err := ociextract.ExtractClientFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			reg.Operations = append(reg.Operations, ops...)
			continue
		}
		models, err := ociextract.ExtractFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		reg.Models = append(reg.Models, models...)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(reg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
