// Command oci-extract reads OCI Go SDK source files and prints extracted data as
// JSON. Build-time tool for the schema pipeline (P2.2/P2.3).
//
//	go run ./cmd/oci-extract <file.go> [<file.go> ...]
//	go run ./cmd/oci-extract -policy <Resource> <model-file.go> ...
//
// Default: a *_client.go file yields operations (method + HTTP verb/path +
// request/response types); any other file yields models (structs → fields),
// emitted together as {operations, models}. With -policy, the models are
// classified into a field policy for <Resource> (P2.3). JSON (not YAML) keeps
// this stdlib-only — canonical output the later pipeline steps can hash.
package main

import (
	"encoding/json"
	"flag"
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
	policyRes := flag.String("policy", "", "derive the field policy for <Resource> (its read-model name, e.g. Vcn) from the given model files")
	flag.Parse()
	files := flag.Args()
	if len(files) < 1 {
		fmt.Fprintln(os.Stderr, "usage: oci-extract [-policy <Resource>] <file.go> [<file.go> ...]")
		os.Exit(2)
	}

	if *policyRes != "" {
		var models []ociextract.Model
		for _, path := range files {
			ms, err := ociextract.ExtractFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			models = append(models, ms...)
		}
		emit(ociextract.ResourcePolicy(models, *policyRes))
		return
	}

	var reg registry
	for _, path := range files {
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
	emit(reg)
}

func emit(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
