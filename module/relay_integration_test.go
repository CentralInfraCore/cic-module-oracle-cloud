//go:build !wasip1

package main

// relay_integration_test.go is the real end-to-end proof of the sign+send path:
// a cryptographically real cic-flow host (RSA-SHA256 pkcs1v15 signing + real HTTP)
// drives the wasm guest's execute against a fake OCI server that VERIFIES the
// draft-cavage HTTP Signature the way OCI would — reconstructing the signing
// string from the wire headers and RSA-verifying it. If the guest's canonical
// string disagrees with the headers it sends, verification fails.
//
// It imports only stdlib + wazero — never the relay's Go packages (CIC-Relay is
// read-only from here). The relay's own cic-flow does the same signing over a
// Vault key; here a local RSA key stands in.

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

func TestRelayIntegrationExecuteSigned(t *testing.T) {
	wasmBytes, err := os.ReadFile(wasmPath)
	if os.IsNotExist(err) {
		t.Skipf("module.wasm not built — run `make wasm.build` first")
	}
	if err != nil {
		t.Fatalf("read %s: %v", wasmPath, err)
	}

	// The OCI API key stands in as a local RSA keypair (real crypto).
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa key: %v", err)
	}

	// Fake OCI: verify the draft-cavage RSA-SHA256 signature, like OCI does.
	var sawValidSignature bool
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if verifyOCISignature(r, &key.PublicKey) {
			sawValidSignature = true
			w.Header().Set("etag", "srv-etag")
			w.Header().Set("opc-request-id", "srv-req")
			_, _ = w.Write([]byte(`{"id":"ocid1.vcn..z","displayName":"prod-vcn"}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://") // 127.0.0.1:port
	client := srv.Client()                          // trusts the test server's cert

	// Real cic-flow: sign = RSA-SHA256 (pkcs1v15) over the canonical string;
	// actuate = real HTTPS carrying the guest's own Authorization.
	sign := func(req []byte) []byte {
		var rr struct {
			DataBase64 string `json:"data_base64"`
		}
		json.Unmarshal(req, &rr)
		data, _ := base64.StdEncoding.DecodeString(rr.DataBase64)
		sum := sha256.Sum256(data)
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		if err != nil {
			out, _ := json.Marshal(map[string]string{"error": err.Error()})
			return out
		}
		out, _ := json.Marshal(map[string]string{"signature": base64.StdEncoding.EncodeToString(sig)})
		return out
	}
	actuate := func(req []byte) []byte {
		var rr struct {
			Method     string            `json:"method"`
			URL        string            `json:"url"`
			Headers    map[string]string `json:"headers"`
			BodyBase64 string            `json:"body_base64"`
		}
		json.Unmarshal(req, &rr)
		body, _ := base64.StdEncoding.DecodeString(rr.BodyBase64)
		hreq, err := http.NewRequest(rr.Method, rr.URL, bytes.NewReader(body))
		if err != nil {
			out, _ := json.Marshal(map[string]string{"error": err.Error()})
			return out
		}
		for k, v := range rr.Headers {
			hreq.Header.Set(k, v)
		}
		resp, err := client.Do(hreq)
		if err != nil {
			out, _ := json.Marshal(map[string]string{"error": err.Error()})
			return out
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(resp.Body)
		hdrs := map[string]string{}
		for k := range resp.Header {
			hdrs[strings.ToLower(k)] = resp.Header.Get(k)
		}
		out, _ := json.Marshal(map[string]interface{}{
			"status": resp.StatusCode, "headers": hdrs,
			"body_base64": base64.StdEncoding.EncodeToString(rb),
		})
		return out
	}

	ctx, instance, callFn, allocateFn, deallocateFn := loadWithCICFlow(t, wasmBytes, sign, actuate)

	exReq, _ := json.Marshal(executeRequest{
		Kind: "cic:network:vcn",
		Plan: executionPlan{
			Operation:          "update",
			ProviderOperations: []providerOperation{{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"}},
		},
		Config:  schemaPayload{Encoding: encCanonicalJSON, Data: json.RawMessage(`{"displayName":"prod-vcn"}`)},
		Binding: execBinding{Host: host, KeyID: "tenancy/user/fp", ResourceID: "ocid1.vcn..z"},
	})
	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "execute", "{}", string(exReq))

	if string(env.Error) != "null" {
		t.Fatalf("execute transport error = %s", env.Error)
	}
	var res struct {
		Status string `json:"status"`
		Result struct {
			Status string `json:"status"`
			Steps  []struct {
				HTTPStatus int    `json:"http_status"`
				Etag       string `json:"etag"`
				Error      string `json:"error"`
			} `json:"steps"`
		} `json:"result"`
	}
	if err := json.Unmarshal(env.Data, &res); err != nil {
		t.Fatalf("decode: %v (raw: %s)", err, env.Data)
	}
	if !sawValidSignature {
		t.Fatalf("fake OCI never saw a valid RSA-SHA256 signature")
	}
	if res.Status != "ok" || res.Result.Status != "succeeded" {
		t.Fatalf("execute = %s/%s, want ok/succeeded (raw: %s)", res.Status, res.Result.Status, env.Data)
	}
	if len(res.Result.Steps) != 1 || res.Result.Steps[0].HTTPStatus != 200 || res.Result.Steps[0].Etag != "srv-etag" {
		t.Errorf("step = %+v, want 200/srv-etag", res.Result.Steps)
	}
}

// TestRelayIntegrationRejectsBadSignature proves execute surfaces a provider auth
// failure: a cic-flow whose signature does not verify gets a 401, and execute
// reports the step as failed (not a faked success).
func TestRelayIntegrationRejectsBadSignature(t *testing.T) {
	wasmBytes, err := os.ReadFile(wasmPath)
	if os.IsNotExist(err) {
		t.Skipf("module.wasm not built")
	}
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyOCISignature(r, &key.PublicKey) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	client := srv.Client()

	// sign returns a well-formed but WRONG signature (signs a different string).
	sign := func(req []byte) []byte {
		sum := sha256.Sum256([]byte("not the canonical string"))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		out, _ := json.Marshal(map[string]string{"signature": base64.StdEncoding.EncodeToString(sig)})
		return out
	}
	actuate := func(req []byte) []byte {
		var rr struct {
			Method, URL string
			Headers     map[string]string `json:"headers"`
			BodyBase64  string            `json:"body_base64"`
		}
		json.Unmarshal(req, &rr)
		body, _ := base64.StdEncoding.DecodeString(rr.BodyBase64)
		hreq, _ := http.NewRequest(rr.Method, rr.URL, bytes.NewReader(body))
		for k, v := range rr.Headers {
			hreq.Header.Set(k, v)
		}
		resp, err := client.Do(hreq)
		if err != nil {
			out, _ := json.Marshal(map[string]string{"error": err.Error()})
			return out
		}
		defer resp.Body.Close()
		out, _ := json.Marshal(map[string]interface{}{"status": resp.StatusCode, "headers": map[string]string{}, "body_base64": ""})
		return out
	}

	ctx, instance, callFn, allocateFn, deallocateFn := loadWithCICFlow(t, wasmBytes, sign, actuate)
	exReq, _ := json.Marshal(executeRequest{
		Kind:    "cic:network:vcn",
		Plan:    executionPlan{Operation: "update", ProviderOperations: []providerOperation{{Operation: "UpdateVcn", Method: "PUT", Path: "/vcns/{vcnId}"}}},
		Config:  schemaPayload{Encoding: encCanonicalJSON, Data: json.RawMessage(`{"displayName":"x"}`)},
		Binding: execBinding{Host: strings.TrimPrefix(srv.URL, "https://"), KeyID: "t/u/f", ResourceID: "ocid1.vcn..z"},
	})
	env := callOp(t, ctx, instance, callFn, allocateFn, deallocateFn, "execute", "{}", string(exReq))

	var res struct {
		Result struct {
			Status string `json:"status"`
			Steps  []struct {
				HTTPStatus int    `json:"http_status"`
				Error      string `json:"error"`
			} `json:"steps"`
		} `json:"result"`
	}
	json.Unmarshal(env.Data, &res)
	if res.Result.Status != "failed" || len(res.Result.Steps) != 1 || res.Result.Steps[0].HTTPStatus != 401 {
		t.Errorf("bad-signature execute = %+v, want failed with a 401 step", res.Result)
	}
}

// verifyOCISignature reconstructs the draft-cavage signing string from the
// request's headers (per the Authorization `headers` list) and RSA-SHA256
// verifies it against pub — exactly the check OCI performs.
func verifyOCISignature(r *http.Request, pub *rsa.PublicKey) bool {
	authz := r.Header.Get("Authorization")
	headersList := sigParam(authz, "headers")
	sigB64 := sigParam(authz, "signature")
	if headersList == "" || sigB64 == "" {
		return false
	}
	var lines []string
	for _, h := range strings.Fields(headersList) {
		var v string
		switch h {
		case "(request-target)":
			v = strings.ToLower(r.Method) + " " + r.URL.RequestURI()
		case "host":
			v = r.Host
		case "content-length":
			// Go moves Content-Length into r.ContentLength, not r.Header.
			if v = r.Header.Get("content-length"); v == "" {
				v = strconv.Itoa(int(r.ContentLength))
			}
		default:
			v = r.Header.Get(h)
		}
		lines = append(lines, h+": "+v)
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return false
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig) == nil
}

// sigParam extracts key="value" from a `Signature …` Authorization header.
func sigParam(authz, key string) string {
	i := strings.Index(authz, key+`="`)
	if i < 0 {
		return ""
	}
	rest := authz[i+len(key)+2:]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// loadWithCICFlow instantiates the guest with a cic-flow host module backed by
// the given sign/actuate handlers (the git/cic-flow ABI).
func loadWithCICFlow(t *testing.T, wasmBytes []byte, sign, actuate func([]byte) []byte) (context.Context, api.Module, api.Function, api.Function, api.Function) {
	t.Helper()
	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { rt.Close(ctx) })
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		t.Fatalf("wasi: %v", err)
	}
	adapt := func(h func([]byte) []byte) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
		return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
			req, ok := mod.Memory().Read(reqPtr, reqLen)
			if !ok {
				return -1
			}
			resp := h(req)
			if uint32(len(resp)) > outLen || !mod.Memory().Write(outPtr, resp) {
				return -1
			}
			return int32(len(resp))
		}
	}
	if _, err := rt.NewHostModuleBuilder("cic-flow").
		NewFunctionBuilder().WithFunc(adapt(sign)).WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").Export("sign").
		NewFunctionBuilder().WithFunc(adapt(actuate)).WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").Export("actuate").
		Instantiate(ctx); err != nil {
		t.Fatalf("cic-flow host module: %v", err)
	}
	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	instance, err := rt.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithName("relay_integration").WithStartFunctions())
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(func() { instance.Close(ctx) })
	return ctx, instance, instance.ExportedFunction("Call"), instance.ExportedFunction("allocate"), instance.ExportedFunction("deallocate")
}
