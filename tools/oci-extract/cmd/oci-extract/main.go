// Command oci-extract reads OCI Go SDK source files and prints extracted data as
// JSON. Build-time tool for the schema pipeline (P2.2/P2.3).
//
//	go run ./cmd/oci-extract <file.go> [<file.go> ...]
//	go run ./cmd/oci-extract -policy <Resource> <model-file.go> ...
//	go run ./cmd/oci-extract -schema <Resource> [-ns <id-stem>] <model-file.go> ...
//	go run ./cmd/oci-extract -diff <old.json> <new.json>   # P2.4, exit 3 if breaking
//	go run ./cmd/oci-extract -audit <file_client.go> ...   # exit 4 if any op unresolved
//
// Default: a *_client.go file yields operations (method + HTTP verb/path +
// request/response types); any other file yields models (structs → fields),
// emitted together as {operations, models}. With -policy, the models are
// classified into a field policy for <Resource>; with -schema, into the
// {config, state} CIC payload schemas for <Resource> (P2.3). JSON (not YAML)
// keeps this stdlib-only — canonical output the later pipeline steps can hash.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ociextract "github.com/CentralInfraCore/cic-module-oracle-cloud/tools/oci-extract"
)

type registry struct {
	Operations []ociextract.Operation `json:"operations"`
	Models     []ociextract.Model     `json:"models"`
}

func main() {
	policyRes := flag.String("policy", "", "derive the field policy for <Resource> (its read-model name, e.g. Vcn) from the given model files")
	schemaRes := flag.String("schema", "", "emit the {config, state} CIC payload schemas for <Resource> from the given model files")
	ns := flag.String("ns", "", "CIC schema id stem for -schema, e.g. cic:network:vcn (default: lower-cased <Resource>)")
	version := flag.String("schema-version", "v0.1.0", "x-cic-schema-version for -schema output")
	diff := flag.Bool("diff", false, "classify the change between two schema bundles: -diff <old.json> <new.json> (P2.4). Exit 3 if breaking.")
	audit := flag.Bool("audit", false, "report operation resolution coverage for the given *_client.go files. Exit 4 if any operation is unresolved.")
	flag.Parse()
	files := flag.Args()

	if *diff {
		if len(files) != 2 {
			fmt.Fprintln(os.Stderr, "usage: oci-extract -diff <old.json> <new.json>")
			os.Exit(2)
		}
		oldB, err1 := os.ReadFile(files[0])
		newB, err2 := os.ReadFile(files[1])
		if err1 != nil || err2 != nil {
			fmt.Fprintf(os.Stderr, "error reading bundles: %v %v\n", err1, err2)
			os.Exit(1)
		}
		d, err := ociextract.DiffConfigSchemas(oldB, newB)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		emit(d)
		if len(d.Breaking) > 0 {
			os.Exit(3) // breaking changes present — the gate fails
		}
		return
	}

	if len(files) < 1 {
		fmt.Fprintln(os.Stderr, "usage: oci-extract [-policy <Resource> | -schema <Resource> [-ns <id-stem>] | -diff <old> <new>] <file.go> ...")
		os.Exit(2)
	}

	if *audit {
		os.Exit(runAudit(files))
	}

	if *policyRes != "" || *schemaRes != "" {
		var models []ociextract.Model
		var operations []ociextract.Operation
		for _, path := range files {
			if strings.HasSuffix(path, "_client.go") {
				ops, err := ociextract.ExtractClientFile(path)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
				operations = append(operations, ops...)
				continue
			}
			ms, err := ociextract.ExtractFile(path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			models = append(models, ms...)
		}
		resource := *policyRes
		if resource == "" {
			resource = *schemaRes
		}
		res := ociextract.Resolve(models, operations, resource)
		if *policyRes != "" {
			emit(ociextract.PolicyOf(res))
			reportUnresolved(res)
			return
		}
		stem := *ns
		if stem == "" {
			stem = strings.ToLower(*schemaRes)
		}
		config, state := ociextract.ResourceSchemasFrom(res, stem, *version)
		bundle := map[string]interface{}{"config": config, "state": state}
		// If a client file was supplied, attach the HTTP method+path for the
		// operations a plan references (P2.2 → concrete, signable plan).
		if len(operations) > 0 {
			bundle["operations"] = ociextract.OperationMap(res)
		}
		emit(bundle)
		reportUnresolved(res)
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

// runAudit reports per-file operation-resolution coverage and returns the exit
// code: 4 when any operation in any file failed to resolve to an HTTP
// method+path. The totals line is the number the P2.2 claim is made against.
func runAudit(files []string) int {
	var candidates, resolved int
	var bad int
	for _, path := range files {
		a, err := ociextract.AuditClientFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		candidates += a.Candidates
		resolved += a.Resolved
		fmt.Printf("%-72s %4d/%-4d resolved\n", filepath.Base(a.File), a.Resolved, a.Candidates)
		if len(a.Unresolved) > 0 {
			bad += len(a.Unresolved)
			fmt.Printf("    UNRESOLVED: %s\n", strings.Join(a.Unresolved, " "))
		}
		if len(a.OrphanPrivate) > 0 {
			bad += len(a.OrphanPrivate)
			fmt.Printf("    ORPHAN-PRIVATE: %s\n", strings.Join(a.OrphanPrivate, " "))
		}
	}
	fmt.Printf("TOTAL %d/%d operations resolved, %d missing method/path\n", resolved, candidates, candidates-resolved)
	if bad > 0 {
		return 4
	}
	return 0
}

// reportUnresolved writes a resolution's gaps to stderr and exits 5 if there are
// any. The gaps go to stderr, not into the emitted JSON, so the artifact on
// stdout stays clean; the non-zero exit is what stops `make oci.generate` from
// committing a schema whose create surface the extractor could not derive.
// Emitting such a schema anyway is the failure mode this whole change is about:
// it looks valid and is not.
func reportUnresolved(res ociextract.Resolution) {
	if len(res.Unresolved) == 0 {
		return
	}
	for _, u := range res.Unresolved {
		fmt.Fprintf(os.Stderr, "unresolved: %s: %s\n", res.Resource, u)
	}
	os.Exit(5)
}

func emit(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
