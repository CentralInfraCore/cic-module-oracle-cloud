// Command oci-extract reads OCI Go SDK source files and prints the extracted
// model registry as JSON. Build-time tool for the schema pipeline (P2.2).
//
//	go run ./cmd/oci-extract <file.go> [<file.go> ...]
//
// JSON (not YAML) keeps this stdlib-only — no external dependency, nothing to
// vendor, and canonical output the later pipeline steps can hash.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	ociextract "github.com/CentralInfraCore/cic-module-oracle-cloud/tools/oci-extract"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: oci-extract <file.go> [<file.go> ...]")
		os.Exit(2)
	}
	var all []ociextract.Model
	for _, path := range os.Args[1:] {
		models, err := ociextract.ExtractFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		all = append(all, models...)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(all); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
