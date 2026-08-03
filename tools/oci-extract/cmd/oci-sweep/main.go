// Command oci-sweep runs the extractor + resolver across every service
// directory of the pinned OCI Go SDK and reports, per service:
//
//   - operation-resolution coverage (same measure as `oci-extract -audit`,
//     aggregated per service instead of per client file)
//   - the resource classification from the full-sweep job spec: A (mechanical
//     create+read), B (read, no usable create surface), C (no read model —
//     not a resource), D (polymorphic create or read)
//   - the path-parameter histogram (addressing shape)
//   - operations whose response carries an opc-work-request-id header (async
//     candidates)
//   - the service's endpoint host template (egress-host inventory input)
//
// This is build-time tooling (roadmap P2.x sweep), not a runtime dependency —
// same status as tools/oci-extract itself. It does not hand-curate any
// resource's file list: for each service directory it feeds the resolver
// every *_client.go (operations) and every other non-test .go file (models) in
// that directory, exactly as `make oci.generate`/`make oci.audit` do per
// resource/per file, just looped over all 171 services instead of one.
//
//	go run ./cmd/oci-sweep -sdk <path-to-oci-go-sdk-root> [-write-schemas <dir>] [-ns-prefix cic]
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	ociextract "github.com/CentralInfraCore/cic-module-oracle-cloud/tools/oci-extract"
)

// namedZeroOpExclusions are *_client.go files known, and documented, to carry
// zero operations: they are plumbing (auth federation, a proxy alias, an OAuth
// helper), not service clients. Named explicitly so their absence from the
// registry is a recorded exclusion, not silence (job spec, sweep task B/§4).
var namedZeroOpExclusions = map[string]string{
	"federation_client.go": "common/auth — token-exchange plumbing, not a service client",
	"database_tools_connection_oracle_database_proxy_client.go": "database_tools — a connection-proxy alias client, no operations of its own",
	"cloud_gate_oauth_client.go":                                "cloudgate — OAuth helper client, no provisioning operations",
}

type ServiceReport struct {
	Service       string   `json:"service"`
	ClientFiles   int      `json:"client_files"`
	ModelFiles    int      `json:"model_files"`
	OpCandidates  int      `json:"op_candidates"`
	OpResolved    int      `json:"op_resolved"`
	OpUnresolved  []string `json:"op_unresolved,omitempty"`
	OrphanPrivate []string `json:"orphan_private,omitempty"`

	ResourceCandidates int      `json:"resource_candidates"`
	ClassA             int      `json:"class_a"`
	ClassB             int      `json:"class_b"`
	ClassC             int      `json:"class_c"`
	ClassD             int      `json:"class_d"`
	ClassDCreatePoly   int      `json:"class_d_create_polymorphic"`
	ClassDReadPoly     int      `json:"class_d_read_polymorphic"`
	ClassAResources    []string `json:"class_a_resources,omitempty"`
	ClassBResources    []string `json:"class_b_resources,omitempty"`
	ClassDResources    []string `json:"class_d_resources,omitempty"`

	PathParamHistogram map[int]int `json:"path_param_histogram"`

	AsyncCandidateOps []string `json:"async_work_request_ops,omitempty"`

	EndpointServiceName string `json:"endpoint_service_name,omitempty"`
	EndpointTemplate    string `json:"endpoint_template,omitempty"`
}

type SweepReport struct {
	SDKVersion string          `json:"sdk_version"`
	Services   []ServiceReport `json:"services"`

	TotalOpCandidates int `json:"total_op_candidates"`
	TotalOpResolved   int `json:"total_op_resolved"`

	TotalResourceCandidates int `json:"total_resource_candidates"`
	TotalClassA             int `json:"total_class_a"`
	TotalClassB             int `json:"total_class_b"`
	TotalClassC             int `json:"total_class_c"`
	TotalClassD             int `json:"total_class_d"`
	TotalClassDCreatePoly   int `json:"total_class_d_create_polymorphic"`
	TotalClassDReadPoly     int `json:"total_class_d_read_polymorphic"`

	ExcludedZeroOpClients map[string]string `json:"excluded_zero_op_clients"`
}

var endpointTemplateRe = regexp.MustCompile(`EndpointForTemplate(?:DottedRegion)?\("([^"]*)",\s*"([^"]*)"`)

func main() {
	sdkRoot := os.Args[1:]
	var sdk, writeSchemas, nsPrefix string
	for i := 0; i < len(sdkRoot); i++ {
		switch sdkRoot[i] {
		case "-sdk":
			i++
			sdk = sdkRoot[i]
		case "-write-schemas":
			i++
			writeSchemas = sdkRoot[i]
		case "-ns-prefix":
			i++
			nsPrefix = sdkRoot[i]
		}
	}
	if sdk == "" {
		fmt.Fprintln(os.Stderr, "usage: oci-sweep -sdk <path-to-oci-go-sdk-root> [-write-schemas <dir>] [-ns-prefix cic]")
		os.Exit(2)
	}
	if nsPrefix == "" {
		nsPrefix = "cic"
	}

	entries, err := os.ReadDir(sdk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading sdk root: %v\n", err)
		os.Exit(1)
	}

	report := SweepReport{ExcludedZeroOpClients: map[string]string{}}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(sdk, e.Name())
		clientFiles, modelFiles, err := listServiceFiles(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error listing %s: %v\n", dir, err)
			os.Exit(1)
		}
		if len(clientFiles) == 0 {
			continue // not a service package (e.g. common/, example/)
		}

		sr := ServiceReport{Service: e.Name(), PathParamHistogram: map[int]int{}}

		var ops []ociextract.Operation
		var models []ociextract.Model
		endpointFound := false

		for _, cf := range clientFiles {
			a, err := ociextract.AuditClientFile(cf)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error auditing %s: %v\n", cf, err)
				os.Exit(1)
			}
			base := filepath.Base(cf)
			if a.Candidates == 0 {
				if reason, known := namedZeroOpExclusions[base]; known {
					report.ExcludedZeroOpClients[filepath.Join(e.Name(), base)] = reason
				} else {
					report.ExcludedZeroOpClients[filepath.Join(e.Name(), base)] = "0 operations (undocumented — flag for review)"
				}
			}
			sr.ClientFiles++
			sr.OpCandidates += a.Candidates
			sr.OpResolved += a.Resolved
			sr.OpUnresolved = append(sr.OpUnresolved, a.Unresolved...)
			sr.OrphanPrivate = append(sr.OrphanPrivate, a.OrphanPrivate...)

			fops, err := ociextract.ExtractClientFile(cf)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error extracting %s: %v\n", cf, err)
				os.Exit(1)
			}
			ops = append(ops, fops...)

			if !endpointFound {
				if name, tmpl, ok := endpointTemplate(cf); ok {
					sr.EndpointServiceName, sr.EndpointTemplate = name, tmpl
					endpointFound = true
				}
			}
		}

		for _, mf := range modelFiles {
			ms, err := ociextract.ExtractFile(mf)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error extracting %s: %v\n", mf, err)
				os.Exit(1)
			}
			models = append(models, ms...)
			sr.ModelFiles++
		}

		byName := map[string]ociextract.Model{}
		for _, m := range models {
			byName[m.Name] = m
		}

		// Resource candidates: every distinct GetX operation, X stripped.
		seen := map[string]bool{}
		var candidates []string
		for _, op := range ops {
			if op.HTTPMethod != "GET" || !strings.HasPrefix(op.Name, "Get") || len(op.Name) <= 3 {
				continue
			}
			res := op.Name[3:]
			if seen[res] {
				continue
			}
			seen[res] = true
			candidates = append(candidates, res)
			sr.PathParamHistogram[len(op.PathParams)]++
		}
		sort.Strings(candidates)
		sr.ResourceCandidates = len(candidates)

		for _, res := range candidates {
			resn := ociextract.Resolve(models, ops, res)
			switch {
			case resn.Read.Name == "":
				sr.ClassC++
			case resn.Read.IsPolymorphic() || resn.Create.IsPolymorphic():
				sr.ClassD++
				sr.ClassDResources = append(sr.ClassDResources, res)
				if resn.Create.IsPolymorphic() {
					sr.ClassDCreatePoly++
				}
				if resn.Read.IsPolymorphic() {
					sr.ClassDReadPoly++
				}
			case resn.CreateOp != nil && resn.Create.Name != "":
				sr.ClassA++
				sr.ClassAResources = append(sr.ClassAResources, res)
				if writeSchemas != "" {
					writeResourceSchema(writeSchemas, e.Name(), res, resn, nsPrefix)
				}
			default:
				sr.ClassB++
				sr.ClassBResources = append(sr.ClassBResources, res)
			}
		}

		// Async candidates: any operation whose response model has a header
		// field named opc-work-request-id.
		for _, op := range ops {
			if op.Response == "" {
				continue
			}
			m := byName[op.Response]
			for _, f := range m.Fields {
				if f.PresentIn == "header" && f.HTTPName == "opc-work-request-id" {
					sr.AsyncCandidateOps = append(sr.AsyncCandidateOps, e.Name()+":"+op.Name)
					break
				}
			}
		}

		report.Services = append(report.Services, sr)
		report.TotalOpCandidates += sr.OpCandidates
		report.TotalOpResolved += sr.OpResolved
		report.TotalResourceCandidates += sr.ResourceCandidates
		report.TotalClassA += sr.ClassA
		report.TotalClassB += sr.ClassB
		report.TotalClassC += sr.ClassC
		report.TotalClassD += sr.ClassD
		report.TotalClassDCreatePoly += sr.ClassDCreatePoly
		report.TotalClassDReadPoly += sr.ClassDReadPoly
	}

	sort.Slice(report.Services, func(i, j int) bool { return report.Services[i].Service < report.Services[j].Service })

	// common/auth/federation_client.go is the one *_client.go in the whole SDK
	// nested two levels below the SDK root (every other service's client files
	// sit directly in a top-level directory, which is what the main loop
	// above walks). It is already a named, documented exclusion (0
	// operations — token-exchange plumbing, not a service client); audited
	// here explicitly so the sweep's total op-candidate count matches the
	// full 319-client-file surface `make oci.audit` reports, not 318.
	if nested := filepath.Join(sdk, "common", "auth", "federation_client.go"); fileExists(nested) {
		a, err := ociextract.AuditClientFile(nested)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error auditing %s: %v\n", nested, err)
			os.Exit(1)
		}
		report.TotalOpCandidates += a.Candidates
		report.TotalOpResolved += a.Resolved
		report.ExcludedZeroOpClients["common/auth/federation_client.go"] = namedZeroOpExclusions["federation_client.go"]
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(report)
}

// listServiceFiles splits a service directory's .go files into *_client.go
// (operations) and every other non-test .go file (models) — the same split
// oci-extract's CLI makes per invocation, applied per directory here.
func listServiceFiles(dir string) (clientFiles, modelFiles []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if strings.HasSuffix(e.Name(), "_client.go") {
			clientFiles = append(clientFiles, full)
		} else {
			modelFiles = append(modelFiles, full)
		}
	}
	return clientFiles, modelFiles, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// endpointTemplate greps a client file's constructor for its
// EndpointForTemplate(serviceName, template) or …DottedRegion call — the
// egress-host input (job spec task F). AST-exact extraction is unnecessary for
// a literal string-argument call; the regex only ever needs to match the one
// documented shape.
func endpointTemplate(path string) (serviceName, template string, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", "", false
	}
	m := endpointTemplateRe.FindStringSubmatch(string(b))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// writeResourceSchema emits a class-A resource's {config, state} bundle to
// disk — the input to the Task D embedding-size measurement. Not committed;
// scratch output for measurement only.
func writeResourceSchema(dir, service, resource string, res ociextract.Resolution, nsPrefix string) {
	stem := nsPrefix + ":" + strings.ToLower(service) + ":" + strings.ToLower(resource)
	config, state := ociextract.ResourceSchemasFrom(res, stem, "v0.1.0")
	bundle := map[string]interface{}{"config": config, "state": state}
	b, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return
	}
	os.MkdirAll(dir, 0o755)
	fname := filepath.Join(dir, fmt.Sprintf("%s-%s.json", strings.ToLower(service), strings.ToLower(resource)))
	os.WriteFile(fname, b, 0o644)
}
