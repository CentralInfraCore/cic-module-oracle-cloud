package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
)

// captureSend installs a cic-flow.send double that records every structured request the guest code
// sends and answers with the given raw JSON; it restores the previous double afterwards.
func captureSend(t *testing.T, answer string) *[]string {
	t.Helper()
	var got []string
	old := testCallHostSend
	testCallHostSend = func(req []byte) ([]byte, error) {
		got = append(got, string(req))
		return []byte(answer), nil
	}
	t.Cleanup(func() { testCallHostSend = old })
	return &got
}

const okAnswer = `{"v":1,"outcome":"completed","http":{"status":200,"headers":{"etag":"e1"},"body_base64":"e30=","body_sha256":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}}`

func walkJSONKeys(v any, f func(string)) {
	switch x := v.(type) {
	case map[string]any:
		for k, vv := range x {
			f(k)
			walkJSONKeys(vv, f)
		}
	case []any:
		for _, vv := range x {
			walkJSONKeys(vv, f)
		}
	}
}

// The guest's request is the structured form of the contract (method, host, path, intent, body) and
// carries nothing the host owns: no Authorization, signature, date, content digest, key, limit.
func TestSendRequestIsStructuredAndCarriesNoHostOwnedFields(t *testing.T) {
	got := captureSend(t, okAnswer)
	b := execBinding{Host: "iaas.eu-frankfurt-1.oraclecloud.com", KeyID: "t/u/f", ResourceID: "ocid1.vcn..z", Revision: "etag-42",
		BindingDigest: "sha256:" + strings.Repeat("ab", 32)}
	status, headers, body, err := actuateSigned("execute", b, "PUT", "/20160918/vcns/ocid1.vcn..z", []byte(`{"displayName":"x"}`), b.Revision)
	if err != nil || status != 200 || headers["etag"] != "e1" || string(body) != "{}" {
		t.Fatalf("actuateSigned = %d %v %q %v", status, headers, body, err)
	}
	if len(*got) != 1 {
		t.Fatalf("want exactly one cic-flow.send call, got %d", len(*got))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte((*got)[0]), &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"v": float64(1), "signing_profile": "http-signature:oci:draft-cavage@1", "key_ref": "oci/default"}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	rq := m["request"].(map[string]any)
	if rq["method"] != "PUT" || rq["host"] != b.Host || rq["path"] != "/20160918/vcns/ocid1.vcn..z" {
		t.Errorf("request = %v", rq)
	}
	if rq["headers"].(map[string]any)["if-match"] != "etag-42" {
		t.Errorf("if-match not carried: %v", rq["headers"])
	}
	raw, _ := base64.StdEncoding.DecodeString(rq["body"].(map[string]any)["base64"].(string))
	if string(raw) != `{"displayName":"x"}` {
		t.Errorf("body = %q", raw)
	}
	in := m["intent"].(map[string]any)
	if in["operation"] != "execute" || in["resource_id"] != "ocid1.vcn..z" || in["binding_digest"] != b.BindingDigest {
		t.Errorf("intent = %v", in)
	}
	forbidden := map[string]bool{"authorization": true, "signature": true, "date": true, "host-header": true, "content-length": true,
		"x-content-sha256": true, "timeout_ms": true, "max_response_bytes": true, "max_request_bytes": true, "key": true, "key_id": true}
	walkJSONKeys(m, func(k string) {
		if forbidden[strings.ToLower(k)] {
			t.Errorf("the guest request carries the host-owned field %q", k)
		}
	})
	if strings.Contains(strings.ToLower((*got)[0]), "request-target") {
		t.Errorf("the guest must not build the canonical string:\n%s", (*got)[0])
	}
}

func TestSendQueryIsSplitFromPath(t *testing.T) {
	got := captureSend(t, okAnswer)
	_, _, _, err := actuateSigned("observe", execBinding{Host: "h"}, "GET", "/x/y?limit=5&page=2", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Request struct {
			Path  string            `json:"path"`
			Query map[string]string `json:"query"`
		} `json:"request"`
	}
	_ = json.Unmarshal([]byte((*got)[0]), &m)
	if m.Request.Path != "/x/y" || m.Request.Query["limit"] != "5" || m.Request.Query["page"] != "2" {
		t.Errorf("request = %+v", m.Request)
	}
}

// A refusal, a failure and a host-level error each surface as an error to the caller, never as a success.
func TestSendDeniedFailedAndErrorAreErrors(t *testing.T) {
	cases := map[string]string{
		`{"v":1,"outcome":"denied","denial":{"code":"POLICY_METHOD"}}`: "send denied: POLICY_METHOD",
		`{"v":1,"outcome":"failed","failure":{"code":"TIMEOUT"}}`:      "send failed: TIMEOUT",
		`{"error":"cic-flow.send: not granted to this module"}`:        "send: cic-flow.send: not granted",
		`{"error":"request too large","code":"REQUEST_INVALID"}`:       "send: request too large",
		`{"v":1,"outcome":"completed"}`:                                "send: unexpected host answer",
		`not json`:                                                     "send: unreadable host answer",
	}
	for answer, want := range cases {
		captureSend(t, answer)
		_, _, _, err := actuateSigned("observe", execBinding{Host: "h"}, "GET", "/p", nil, "")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("answer %s: err = %v, want containing %q", answer, err, want)
		}
	}
}

// AC-SS-06 (guest side): the observation operations (observe, poll) only ever issue reads. The block on
// writes is the host's (observation profile on the outgoing request); this pins that the guest does not
// even attempt one.
func TestObservationOperationsOnlyIssueReads(t *testing.T) {
	got := captureSend(t, `{"v":1,"outcome":"completed","http":{"status":200,"headers":{},"body_base64":"e30=","body_sha256":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}}`)
	obs, _ := json.Marshal(observeRequest{Kind: "cic:network:vcn", Binding: execBinding{Host: "h", KeyID: "k", ResourceID: "ocid1.vcn..x"}})
	if _, err := Observe(nil, obs); err != nil {
		t.Fatal(err)
	}
	poll, _ := json.Marshal(pollRequest{Binding: execBinding{Host: "h", KeyID: "k"}, Path: "/20160918/workRequests/w1"})
	if _, err := Poll(nil, poll); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("calls = %d, want 2", len(*got))
	}
	for _, c := range *got {
		var m struct {
			Request struct {
				Method string `json:"method"`
			} `json:"request"`
			Intent struct {
				Operation string `json:"operation"`
			} `json:"intent"`
		}
		_ = json.Unmarshal([]byte(c), &m)
		if m.Request.Method != "GET" {
			t.Errorf("%s issued %s, want GET", m.Intent.Operation, m.Request.Method)
		}
	}
}

// The shipped artifact imports exactly one cic-flow function, send: no raw sign or actuate (checked on the
// compiled wasm, not on the declaration).
func TestWasmArtifactImportsOnlyCICFlowSend(t *testing.T) {
	wasm, err := os.ReadFile(wasmPath)
	if os.IsNotExist(err) {
		t.Skip("module.wasm not built")
	}
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	defer rt.Close(ctx)
	cm, err := rt.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	var flow []string
	for _, f := range cm.ImportedFunctions() {
		if mod, name, ok := f.Import(); ok && mod == "cic-flow" {
			flow = append(flow, name)
		}
	}
	if len(flow) != 1 || flow[0] != "send" {
		t.Fatalf("cic-flow imports of module.wasm = %v, want [send]", flow)
	}
}

// project.yaml declares abi.imports = cic-flow: [send] (and nothing else).
func TestProjectYAMLDeclaresOnlySend(t *testing.T) {
	f, err := os.Open(projectYAMLPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	in := false
	var fns []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		tr := strings.TrimSpace(line)
		if tr == "" || strings.HasPrefix(tr, "#") {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent == 2 && tr == "imports:" {
			in = true
			continue
		}
		if in && indent <= 2 {
			break
		}
		if in && strings.HasPrefix(tr, "- ") && !strings.HasPrefix(tr, "- module:") {
			fns = append(fns, strings.TrimSpace(strings.TrimPrefix(tr, "- ")))
		}
	}
	if len(fns) != 1 || fns[0] != "send" {
		t.Fatalf("abi.imports functions = %v, want [send]", fns)
	}
}

// The guest sources hold no signing or Authorization construction any more.
func TestGuestSourcesBuildNoAuthorization(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, _ := os.ReadFile(f)
		for _, bad := range []string{"ociCanonical", "ociAuthorization", "callHostSign", "callHostActuate", "wasmCicFlowSign", "wasmCicFlowActuate", `"authorization"`, "data_base64", "rsa-sha256"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s still contains %q", f, bad)
			}
		}
	}
}
