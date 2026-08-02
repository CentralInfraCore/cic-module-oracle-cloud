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
// dispatch path (abi.go), never exercises the relay's real cic-flow host
// functions (Vault signing, egress-policy enforcement, capability-manifest
// checks), and only calls read-only/pure ops (describe, observe, validate,
// plan) — never execute/invoke/destroy, which would actually mutate the
// target OCI tenancy.
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
//   REAL_OCI_TEST=1 go test -tags manual_real_oci ./module/ -run TestManualRealOCI -v
//
// Realm note: the Host is built as iaas.<region>.oraclecloud.eu — this
// targets the EU Sovereign Cloud realm (tenancy OCIDs with an "oc19" realm
// segment). A commercial-realm ("oc1") tenancy needs oraclecloud.com instead.

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
			Host:       fmt.Sprintf("iaas.%s.oraclecloud.eu", region),
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
			Host:       fmt.Sprintf("iaas.%s.oraclecloud.eu", region),
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
			Host:       fmt.Sprintf("iaas.%s.oraclecloud.eu", region),
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
