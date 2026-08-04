//go:build !wasip1 && manual_real_oci

package main

// manual_real_oci_test.go is an opt-in, manual verification harness that runs
// the module's own handler functions (Describe/Observe/Validate/Plan) against
// a REAL OCI tenancy — using the real OCI API-key signing scheme (the same
// RSA-SHA256/PKCS1v15 draft-cavage profile cic-flow.sign performs in
// production, here computed in-process instead of via Vault) and a real
// net/http call. It wires testCallHostSign/testCallHostActuate (the
// cicflow_host.go test seam — see that file) to real crypto and a real HTTP
// round trip.
//
// WHAT THIS DOES NOT COVER — see docs/design/manual-verification.md for the
// full picture: it never runs through the actual module.wasm/wazero/ABI-
// dispatch path (abi.go), and never exercises the relay's real cic-flow host
// functions (Vault signing, egress-policy enforcement, capability-manifest
// checks). Describe/Observe/Validate/Plan/Poll never mutate; Execute/Destroy/
// Invoke do — see the warning on TestManualRealOCIExecute before running one.
//
// ISOLATION — double-gated, on purpose, so a plain `go test ./...` (no build
// tags) or `make golang.test` never sees or runs this file:
//  1. build tag `manual_real_oci` — absent by default; the file is not even
//     compiled unless explicitly requested (`go test -tags manual_real_oci`,
//     or `make golang.test.manual-real-oci`, see mk/golang.mk).
//  2. runtime env var REAL_OCI_TEST=1 — belt-and-suspenders; even a build with
//     the tag skips every test here unless this is also set.
//
// USAGE (read docs/design/manual-verification.md first):
//   OCI_KEY_PATH=~/.oci/oci_api_key.pem \
//   OCI_TENANCY_OCID=ocid1.tenancy... OCI_USER_OCID=ocid1.user... \
//   OCI_FINGERPRINT=xx:xx:...           OCI_REGION=eu-frankfurt-2 \
//   OCI_TEST_KIND=cic:network:vcn       OCI_TEST_RESOURCE_ID=ocid1.vcn... \
//   REAL_OCI_TEST=1 go test -tags manual_real_oci -count=1 ./module/ -run TestManualRealOCI -v
//
// GOTCHA — always pass -count=1. This test hits live OCI state that changes
// between runs (e.g. re-Observe after an Execute-driven Update) even when the
// env vars are byte-identical to a previous invocation. Go's test result
// cache does not know that "identical inputs" doesn't mean "identical
// outcome" for a test with real external side effects — without -count=1 it
// will silently replay a stale cached result instead of re-running.
//
// Realm note: the Host is built as iaas.<region>.<OCI_REALM_DOMAIN, default
// oraclecloud.com>. A commercial-realm ("oc1") tenancy OCID needs the default;
// an EU Sovereign Cloud realm tenancy ("oc19" in the OCID) needs
// OCI_REALM_DOMAIN=oraclecloud.eu instead — set it explicitly, this harness
// does not infer the realm from the OCID.

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
)

func TestManualRealOCIObserve(t *testing.T) {
	if os.Getenv("REAL_OCI_TEST") == "" {
		t.Skip("set REAL_OCI_TEST=1 to run against real OCI")
	}

	keyPath := os.Getenv("OCI_KEY_PATH")
	tenancy := os.Getenv("OCI_TENANCY_OCID")
	user := os.Getenv("OCI_USER_OCID")
	fingerprint := os.Getenv("OCI_FINGERPRINT")
	region := os.Getenv("OCI_REGION")
	resourceID := os.Getenv("OCI_TEST_RESOURCE_ID")
	if resourceID == "" {
		resourceID = os.Getenv("OCI_TEST_VCN_ID") // back-compat with the first (VCN-only) run
	}
	kind := os.Getenv("OCI_TEST_KIND")
	if kind == "" {
		kind = "cic:network:vcn"
	}
	if keyPath == "" || tenancy == "" || user == "" || fingerprint == "" || region == "" || resourceID == "" {
		t.Fatal("OCI_KEY_PATH, OCI_TENANCY_OCID, OCI_USER_OCID, OCI_FINGERPRINT, OCI_REGION, OCI_TEST_RESOURCE_ID must all be set")
	}

	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("no PEM block in key file")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKCS8 key: %v", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("key is not RSA")
	}

	// Real cic-flow.sign: RSA-SHA256 PKCS1v15 over the canonical string built by
	// oci_sign.go — the exact recipe production uses, minus Vault custody.
	testCallHostSign = func(req []byte) ([]byte, error) {
		var in struct {
			DataBase64 string `json:"data_base64"`
		}
		if err := json.Unmarshal(req, &in); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(in.DataBase64)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, sum[:])
		if err != nil {
			return nil, err
		}
		out, _ := json.Marshal(map[string]string{"signature": base64.StdEncoding.EncodeToString(sig)})
		return out, nil
	}

	// Real cic-flow.actuate: an actual net/http call to OCI.
	testCallHostActuate = func(req []byte) ([]byte, error) {
		var in struct {
			Method     string            `json:"method"`
			URL        string            `json:"url"`
			Headers    map[string]string `json:"headers"`
			BodyBase64 string            `json:"body_base64"`
		}
		if err := json.Unmarshal(req, &in); err != nil {
			return nil, err
		}
		body, _ := base64.StdEncoding.DecodeString(in.BodyBase64)
		httpReq, err := http.NewRequest(in.Method, in.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range in.Headers {
			httpReq.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		hdrs := map[string]string{}
		for k := range resp.Header {
			hdrs[toLowerHeader(k)] = resp.Header.Get(k)
		}
		out, _ := json.Marshal(map[string]interface{}{
			"status":      resp.StatusCode,
			"headers":     hdrs,
			"body_base64": base64.StdEncoding.EncodeToString(respBody),
		})
		return out, nil
	}

	req := observeRequest{
		Kind: kind,
		Binding: execBinding{
			Host:       ociHost(region),
			BasePath:   "/20160918",
			KeyID:      tenancy + "/" + user + "/" + fingerprint,
			ResourceID: resourceID,
		},
	}
	reqJSON, _ := json.Marshal(req)

	resultJSON, err := Observe(nil, reqJSON)
	if err != nil {
		t.Fatalf("Observe returned Go error: %v", err)
	}
	fmt.Println("=== Observe result ===")
	fmt.Println(string(resultJSON))

	var generic map[string]json.RawMessage
	if err := json.Unmarshal(resultJSON, &generic); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if errField, ok := generic["error"]; ok {
		t.Fatalf("provider returned an error: %s", errField)
	}
}

// TestManualRealOCIValidate observes a real resource, then feeds its own
// effective_config back into Validate() as the "intent" — a self-consistency
// check: does the module's generated contract accept the config surface of a
// resource that OCI itself considers valid? Validate is pure (no host calls,
// no network) once we have the data.
func TestManualRealOCIValidate(t *testing.T) {
	if os.Getenv("REAL_OCI_TEST") == "" {
		t.Skip("set REAL_OCI_TEST=1 to run against real OCI")
	}

	keyPath := os.Getenv("OCI_KEY_PATH")
	tenancy := os.Getenv("OCI_TENANCY_OCID")
	user := os.Getenv("OCI_USER_OCID")
	fingerprint := os.Getenv("OCI_FINGERPRINT")
	region := os.Getenv("OCI_REGION")
	resourceID := os.Getenv("OCI_TEST_RESOURCE_ID")
	kind := os.Getenv("OCI_TEST_KIND")
	if kind == "" {
		kind = "cic:network:vcn"
	}
	if keyPath == "" || tenancy == "" || user == "" || fingerprint == "" || region == "" || resourceID == "" {
		t.Fatal("OCI_KEY_PATH, OCI_TENANCY_OCID, OCI_USER_OCID, OCI_FINGERPRINT, OCI_REGION, OCI_TEST_RESOURCE_ID must all be set")
	}

	rsaKey := loadRSAKey(t, keyPath)
	wireRealHostCalls(rsaKey)

	obsReq := observeRequest{
		Kind: kind,
		Binding: execBinding{
			Host:       ociHost(region),
			BasePath:   "/20160918",
			KeyID:      tenancy + "/" + user + "/" + fingerprint,
			ResourceID: resourceID,
		},
	}
	obsReqJSON, _ := json.Marshal(obsReq)
	obsResultJSON, err := Observe(nil, obsReqJSON)
	if err != nil {
		t.Fatalf("Observe returned Go error: %v", err)
	}
	var obsResult struct {
		Status string `json:"status"`
		Result struct {
			EffectiveConfig json.RawMessage `json:"effective_config"`
		} `json:"result"`
		Error *json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(obsResultJSON, &obsResult); err != nil {
		t.Fatalf("observe result not valid JSON: %v", err)
	}
	if obsResult.Status != "ok" {
		t.Fatalf("observe failed: %s", obsResultJSON)
	}

	sum := sha256.Sum256(obsResult.Result.EffectiveConfig)
	valReq := validationRequest{
		Kind: kind,
		Intent: schemaPayload{
			SchemaID:      kind + "-config",
			SchemaVersion: "1.0",
			SchemaHash:    fmt.Sprintf("%x", sum),
			Encoding:      encCanonicalJSON,
			Data:          obsResult.Result.EffectiveConfig,
		},
	}
	valReqJSON, _ := json.Marshal(valReq)

	valResultJSON, err := Validate(nil, valReqJSON)
	if err != nil {
		t.Fatalf("Validate returned Go error: %v", err)
	}
	fmt.Println("=== Validate result (intent = the real resource's own effective_config) ===")
	fmt.Println(string(valResultJSON))

	var valResult validationResult
	var wrapper struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(valResultJSON, &wrapper); err != nil {
		t.Fatalf("validate result not valid JSON: %v", err)
	}
	if err := json.Unmarshal(wrapper.Result, &valResult); err != nil {
		t.Fatalf("validate result.result not valid JSON: %v", err)
	}
	if !valResult.Admissible {
		t.Fatalf("expected admissible=true (own effective_config should conform), got errors: %+v", valResult.Errors)
	}
}

// TestManualRealOCIPlan observes a real resource, then runs Plan() twice: once
// with desired == observed (expect noop), once with one mutable field changed
// (expect update, with a concrete UpdateSubnet/UpdateVcn provider operation).
// Plan is pure (no host calls, no network) — this never touches OCI beyond the
// one real Observe used to seed real data.
func TestManualRealOCIPlan(t *testing.T) {
	if os.Getenv("REAL_OCI_TEST") == "" {
		t.Skip("set REAL_OCI_TEST=1 to run against real OCI")
	}

	keyPath := os.Getenv("OCI_KEY_PATH")
	tenancy := os.Getenv("OCI_TENANCY_OCID")
	user := os.Getenv("OCI_USER_OCID")
	fingerprint := os.Getenv("OCI_FINGERPRINT")
	region := os.Getenv("OCI_REGION")
	resourceID := os.Getenv("OCI_TEST_RESOURCE_ID")
	kind := os.Getenv("OCI_TEST_KIND")
	if kind == "" {
		kind = "cic:network:vcn"
	}
	if keyPath == "" || tenancy == "" || user == "" || fingerprint == "" || region == "" || resourceID == "" {
		t.Fatal("OCI_KEY_PATH, OCI_TENANCY_OCID, OCI_USER_OCID, OCI_FINGERPRINT, OCI_REGION, OCI_TEST_RESOURCE_ID must all be set")
	}

	rsaKey := loadRSAKey(t, keyPath)
	wireRealHostCalls(rsaKey)

	obsReq := observeRequest{
		Kind: kind,
		Binding: execBinding{
			Host:       ociHost(region),
			BasePath:   "/20160918",
			KeyID:      tenancy + "/" + user + "/" + fingerprint,
			ResourceID: resourceID,
		},
	}
	obsReqJSON, _ := json.Marshal(obsReq)
	obsResultJSON, err := Observe(nil, obsReqJSON)
	if err != nil {
		t.Fatalf("Observe returned Go error: %v", err)
	}
	var obsResult struct {
		Status string `json:"status"`
		Result struct {
			EffectiveConfig json.RawMessage `json:"effective_config"`
		} `json:"result"`
	}
	if err := json.Unmarshal(obsResultJSON, &obsResult); err != nil || obsResult.Status != "ok" {
		t.Fatalf("observe failed: %s (err=%v)", obsResultJSON, err)
	}
	observedPayload := payloadFor(kind, obsResult.Result.EffectiveConfig)

	runPlan := func(label string, desiredData json.RawMessage) {
		planReq := planRequest{
			Kind:     kind,
			Desired:  payloadFor(kind, desiredData),
			Observed: observedPayload,
		}
		planReqJSON, _ := json.Marshal(planReq)
		planResultJSON, err := Plan(nil, planReqJSON)
		if err != nil {
			t.Fatalf("[%s] Plan returned Go error: %v", label, err)
		}
		fmt.Printf("=== Plan result (%s) ===\n%s\n", label, planResultJSON)
	}

	// Case 1: desired == observed -> noop.
	runPlan("desired==observed", obsResult.Result.EffectiveConfig)

	// Case 2: mutate one "mutable" field (displayName) -> expect an update plan
	// with a concrete UpdateSubnet/UpdateVcn provider operation.
	var eff map[string]json.RawMessage
	if err := json.Unmarshal(obsResult.Result.EffectiveConfig, &eff); err != nil {
		t.Fatalf("effective_config not an object: %v", err)
	}
	nameB, _ := json.Marshal("plan-test-renamed")
	eff["displayName"] = nameB
	changedData, _ := json.Marshal(eff)
	runPlan("displayName changed", changedData)
}

// ociHost builds the IAAS host for a region, honoring OCI_REALM_DOMAIN
// (default oraclecloud.com — the commercial realm) so this harness works
// against both commercial ("oc1") and EU Sovereign ("oc19", ...) tenancies.
func ociHost(region string) string {
	domain := os.Getenv("OCI_REALM_DOMAIN")
	if domain == "" {
		domain = "oraclecloud.com"
	}
	return fmt.Sprintf("iaas.%s.%s", region, domain)
}

// TestManualRealOCIExecute runs ONE approved provider_operation for real —
// e.g. CreateVcn or UpdateVcn — against a live tenancy. Unlike Observe/
// Validate/Plan, this MUTATES real OCI state: it creates, changes, or (via
// Delete<Resource>) deletes whatever OCI_EXEC_OPERATION/METHOD/PATH describe.
// Never run this without knowing exactly which tenancy/compartment/resource
// it targets — see docs/design/manual-verification.md.
//
// Config (env):
//
//	OCI_EXEC_OPERATION   e.g. CreateVcn, UpdateVcn, DeleteVcn (registry name)
//	OCI_EXEC_METHOD      e.g. POST, PUT, DELETE
//	OCI_EXEC_PATH        e.g. /vcns  or  /vcns/{vcnId} (resourceID fills {..})
//	OCI_EXEC_CONFIG_JSON the config body fields as a JSON object (renderBody
//	                     filters it per the op: Create keeps all contract
//	                     fields present, Update keeps only "mutable" ones)
//	OCI_TEST_RESOURCE_ID required for Update/Delete paths with a {param};
//	                     leave unset for Create (the path has none to fill)
func TestManualRealOCIExecute(t *testing.T) {
	if os.Getenv("REAL_OCI_TEST") == "" {
		t.Skip("set REAL_OCI_TEST=1 to run against real OCI")
	}

	keyPath := os.Getenv("OCI_KEY_PATH")
	tenancy := os.Getenv("OCI_TENANCY_OCID")
	user := os.Getenv("OCI_USER_OCID")
	fingerprint := os.Getenv("OCI_FINGERPRINT")
	region := os.Getenv("OCI_REGION")
	resourceID := os.Getenv("OCI_TEST_RESOURCE_ID") // may be "" for Create
	kind := os.Getenv("OCI_TEST_KIND")
	operation := os.Getenv("OCI_EXEC_OPERATION")
	method := os.Getenv("OCI_EXEC_METHOD")
	path := os.Getenv("OCI_EXEC_PATH")
	configJSON := os.Getenv("OCI_EXEC_CONFIG_JSON")
	if keyPath == "" || tenancy == "" || user == "" || fingerprint == "" || region == "" ||
		kind == "" || operation == "" || method == "" || path == "" || configJSON == "" {
		t.Fatal("OCI_KEY_PATH, OCI_TENANCY_OCID, OCI_USER_OCID, OCI_FINGERPRINT, OCI_REGION, " +
			"OCI_TEST_KIND, OCI_EXEC_OPERATION, OCI_EXEC_METHOD, OCI_EXEC_PATH, OCI_EXEC_CONFIG_JSON must all be set")
	}
	if !json.Valid([]byte(configJSON)) {
		t.Fatalf("OCI_EXEC_CONFIG_JSON is not valid JSON: %q", configJSON)
	}

	rsaKey := loadRSAKey(t, keyPath)
	wireRealHostCalls(rsaKey)

	execReq := executeRequest{
		Kind: kind,
		Plan: executionPlan{
			ProviderOperations: []providerOperation{
				{Operation: operation, Method: method, Path: path},
			},
		},
		Config: payloadFor(kind, json.RawMessage(configJSON)),
		Binding: execBinding{
			Host:       ociHost(region),
			BasePath:   "/20160918",
			KeyID:      tenancy + "/" + user + "/" + fingerprint,
			ResourceID: resourceID,
		},
	}
	execReqJSON, _ := json.Marshal(execReq)

	resultJSON, err := Execute(nil, execReqJSON)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	fmt.Printf("=== Execute result (%s) ===\n%s\n", operation, resultJSON)

	var wrapper struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(resultJSON, &wrapper); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if wrapper.Status != "ok" {
		t.Fatalf("Execute reported status=%s: %s", wrapper.Status, resultJSON)
	}
	var er executionResult
	if err := json.Unmarshal(wrapper.Result, &er); err != nil {
		t.Fatalf("result.result not valid JSON: %v", err)
	}
	if er.Status == "failed" {
		t.Fatalf("execution failed: %+v", er.Steps)
	}
}

// TestManualRealOCIPoll GETs a real OCI Work Request and reports its lifecycle
// (poll's "implemented" status in provider.go's header had never been run
// against real OCI before this test — see docs/design/manual-verification.md).
// Read-only: it never mutates. It needs a still-live Work Request path — an
// async op's execute step surfaces one in work_request_id (see
// TestManualRealOCIExecute); CreateVcn/UpdateVcn are synchronous and never
// produce one, but LaunchInstance/TerminateInstance do (measured — see
// docs/design/manual-verification.md).
//
// Config (env):
//
//	OCI_POLL_PATH   the Work Request GET path, INCLUDING the API version
//	                prefix (Poll does not prepend binding.base_path — it GETs
//	                this path verbatim), e.g.
//	                /20160918/workRequests/ocid1.workrequest.oc1...
func TestManualRealOCIPoll(t *testing.T) {
	if os.Getenv("REAL_OCI_TEST") == "" {
		t.Skip("set REAL_OCI_TEST=1 to run against real OCI")
	}

	keyPath := os.Getenv("OCI_KEY_PATH")
	tenancy := os.Getenv("OCI_TENANCY_OCID")
	user := os.Getenv("OCI_USER_OCID")
	fingerprint := os.Getenv("OCI_FINGERPRINT")
	region := os.Getenv("OCI_REGION")
	pollPath := os.Getenv("OCI_POLL_PATH")
	if keyPath == "" || tenancy == "" || user == "" || fingerprint == "" || region == "" || pollPath == "" {
		t.Fatal("OCI_KEY_PATH, OCI_TENANCY_OCID, OCI_USER_OCID, OCI_FINGERPRINT, OCI_REGION, OCI_POLL_PATH must all be set")
	}

	rsaKey := loadRSAKey(t, keyPath)
	wireRealHostCalls(rsaKey)

	pollReq := pollRequest{
		Binding: execBinding{
			Host:  ociHost(region),
			KeyID: tenancy + "/" + user + "/" + fingerprint,
		},
		Path: pollPath,
	}
	pollReqJSON, _ := json.Marshal(pollReq)

	resultJSON, err := Poll(nil, pollReqJSON)
	if err != nil {
		t.Fatalf("Poll returned Go error: %v", err)
	}
	fmt.Printf("=== Poll result ===\n%s\n", resultJSON)

	var wrapper struct {
		Status string          `json:"status"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal(resultJSON, &wrapper); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	if wrapper.Status != "ok" {
		t.Fatalf("Poll reported status=%s: %s", wrapper.Status, resultJSON)
	}
	var pr pollResult
	if err := json.Unmarshal(wrapper.Result, &pr); err != nil {
		t.Fatalf("result.result not valid JSON: %v", err)
	}
	if pr.WorkStatus == "" {
		t.Fatalf("poll result has no work_status: %+v", pr)
	}
}

// payloadFor wraps raw JSON data into a schemaPayload envelope with a real
// sha256 schema_hash (Plan/Validate only check well-formedness, not that the
// hash matches a specific registry entry).
func payloadFor(kind string, data json.RawMessage) schemaPayload {
	sum := sha256.Sum256(data)
	return schemaPayload{
		SchemaID:      kind + "-config",
		SchemaVersion: "1.0",
		SchemaHash:    fmt.Sprintf("%x", sum),
		Encoding:      encCanonicalJSON,
		Data:          data,
	}
}

// loadRSAKey reads and parses a PKCS8 RSA private key from disk.
func loadRSAKey(t *testing.T, keyPath string) *rsa.PrivateKey {
	t.Helper()
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("no PEM block in key file")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse PKCS8 key: %v", err)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("key is not RSA")
	}
	return rsaKey
}

// wireRealHostCalls wires testCallHostSign/testCallHostActuate to real RSA
// signing and a real net/http call — the same substitution used throughout
// this file.
func wireRealHostCalls(rsaKey *rsa.PrivateKey) {
	testCallHostSign = func(req []byte) ([]byte, error) {
		var in struct {
			DataBase64 string `json:"data_base64"`
		}
		if err := json.Unmarshal(req, &in); err != nil {
			return nil, err
		}
		data, err := base64.StdEncoding.DecodeString(in.DataBase64)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, sum[:])
		if err != nil {
			return nil, err
		}
		out, _ := json.Marshal(map[string]string{"signature": base64.StdEncoding.EncodeToString(sig)})
		return out, nil
	}
	testCallHostActuate = func(req []byte) ([]byte, error) {
		var in struct {
			Method     string            `json:"method"`
			URL        string            `json:"url"`
			Headers    map[string]string `json:"headers"`
			BodyBase64 string            `json:"body_base64"`
		}
		if err := json.Unmarshal(req, &in); err != nil {
			return nil, err
		}
		body, _ := base64.StdEncoding.DecodeString(in.BodyBase64)
		httpReq, err := http.NewRequest(in.Method, in.URL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range in.Headers {
			httpReq.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		hdrs := map[string]string{}
		for k := range resp.Header {
			hdrs[toLowerHeader(k)] = resp.Header.Get(k)
		}
		out, _ := json.Marshal(map[string]interface{}{
			"status":      resp.StatusCode,
			"headers":     hdrs,
			"body_base64": base64.StdEncoding.EncodeToString(respBody),
		})
		return out, nil
	}
}

func toLowerHeader(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// TestManualDescribe needs no OCI account at all — Describe is pure and
// deterministic (module manifest only).
func TestManualDescribe(t *testing.T) {
	resultJSON, err := Describe(nil, nil)
	if err != nil {
		t.Fatalf("Describe returned Go error: %v", err)
	}
	fmt.Println("=== Describe result ===")
	fmt.Println(string(resultJSON))
}
