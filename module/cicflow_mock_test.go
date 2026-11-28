//go:build !wasip1

package main

// cicflow_mock_test.go is the configurable `cic-flow` host-module mock the wasm
// host-load tests share. It generalizes the fixed mock that used to be inlined
// in TestHostLoadExecute, where the response was hard-wired to 200 + "{}" and
// only the last request was kept.
//
// Four things it adds, all needed to assert the execute result honestly:
//   - a per-call responder, so a test can answer a create with a real body, a
//     malformed body, or an error status;
//   - every send request the GUEST made recorded IN ORDER and structurally, so a
//     test can check what the guest actually asked for (method, path, intent)
//     rather than only what came back;
//   - every sign and actuate step of the SIMULATED HOST recorded in order, so the
//     old assertions about what left the host still have a subject;
//   - request COUNTS, so a test can assert how many provider requests a run made
//     — the only way to prove the module does not retry a create behind the
//     caller's back (a blind retry would duplicate the resource).
//
// Since O-1 WP-04 the guest's single host import is cic-flow.send, so the mock's
// entry point is handleSend. The sign-then-send steps behind it belong to the
// simulated relay (host_mock_test.go: mockRelaySend), not to the guest; the mock
// keeps recording them because "nothing was signed" and "nothing was sent" are
// exactly the properties the gate tests assert.
//
// The host-function ABI mirrored here is the relay's real one:
// (req_ptr,req_len,out_ptr,out_len)->i32 with JSON in linear memory,
// bytes-written or -1. It is a mock of the relay's interface, never a stand-in
// for the relay itself: nothing here signs or sends anything for real.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// cicFlowSend is one recorded cic-flow.send request, exactly as the GUEST sent it. This is the
// production-side record: every field here was produced by provider.go.
type cicFlowSend struct {
	V              int
	SigningProfile string
	KeyRef         string
	Method         string
	Host           string
	Path           string
	Query          map[string]string
	Headers        map[string]string
	Body           []byte // decoded from request.body.base64
	Operation      string // intent.operation
	ResourceID     string // intent.resource_id
	BindingDigest  string // intent.binding_digest
	Raw            string // the verbatim request JSON, for substring assertions
}

// cicFlowCall is one recorded request the SIMULATED HOST put on the wire (the host's own
// actuate step), including the Authorization header the host built.
type cicFlowCall struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    []byte // decoded from body_base64
	Raw     string // the verbatim request JSON, for substring assertions
}

// cicFlowReply is what the mock answers an actuate with. Body is plain text; the
// mock base64-encodes it the way the relay would.
type cicFlowReply struct {
	Status  int
	Headers map[string]string
	Body    string
	Error   string // non-empty: a host-side failure ({"error": ...}), not an HTTP answer
}

// cicFlowMock records what the guest sent and decides what it gets back.
type cicFlowMock struct {
	// Signature is handed back for every sign call ("FAKESIG==" if empty).
	Signature string
	// Reply answers actuate call n (1-based). Nil means 200 with an empty JSON
	// object — the old inlined mock's behaviour.
	Reply func(n int, call cicFlowCall) cicFlowReply
	// Refuse, when set, decides whether the simulated host refuses send call n (1-based) BEFORE
	// signing or sending anything, the way the relay refuses a target outside the operator policy.
	// An empty code means "admit this call". outcome is "denied" (default) or "failed".
	Refuse func(n int, call cicFlowSend) (outcome, code, detail string)

	Sent     []cicFlowSend // the GUEST's send requests, in order
	Signed   []string      // canonical strings the simulated host signed, in order
	Actuated []cicFlowCall // requests the simulated host put on the wire, in order
}

func (m *cicFlowMock) sendCount() int    { return len(m.Sent) }
func (m *cicFlowMock) signCount() int    { return len(m.Signed) }
func (m *cicFlowMock) actuateCount() int { return len(m.Actuated) }

// lastSend is the most recent cic-flow.send request the guest made; it fails the test if there
// was none, so a silent zero value can never be mistaken for a real request.
func (m *cicFlowMock) lastSend(t *testing.T) cicFlowSend {
	t.Helper()
	if len(m.Sent) == 0 {
		t.Fatalf("no cic-flow.send request was made")
	}
	return m.Sent[len(m.Sent)-1]
}

// handleSend is the mock's cic-flow.send entry point: record the guest's structured request, give
// the test a chance to refuse it with a named contract code, and otherwise let the simulated relay
// sign and send it.
func (m *cicFlowMock) handleSend(req []byte) []byte {
	var in wireSendRequest
	_ = json.Unmarshal(req, &in)
	var body []byte
	if in.Request.Body != nil {
		body, _ = base64.StdEncoding.DecodeString(in.Request.Body.Base64)
	}
	call := cicFlowSend{
		V: in.V, SigningProfile: in.SigningProfile, KeyRef: in.KeyRef,
		Method: in.Request.Method, Host: in.Request.Host, Path: in.Request.Path,
		Query: in.Request.Query, Headers: in.Request.Headers, Body: body,
		Operation: in.Intent.Operation, ResourceID: in.Intent.ResourceID,
		BindingDigest: in.Intent.BindingDigest, Raw: string(req),
	}
	m.Sent = append(m.Sent, call)

	if m.Refuse != nil {
		outcome, code, detail := m.Refuse(len(m.Sent), call)
		if code != "" {
			if outcome == "failed" {
				return mockFailed(code, detail)
			}
			return mockDenied(code, detail)
		}
	}
	// The simulated relay signs and sends through this mock's own sign/actuate steps.
	restore := m.wireMockHostSteps()
	defer restore()
	out, _ := mockRelaySend(req)
	return out
}

// wireMockHostSteps points the simulated host's signer and transport at this mock, leaving any
// previously wired ones restorable.
func (m *cicFlowMock) wireMockHostSteps() (restore func()) {
	oldSign, oldAct := testCallHostSign, testCallHostActuate
	testCallHostSign = func(req []byte) ([]byte, error) { return m.handleSign(req), nil }
	testCallHostActuate = func(req []byte) ([]byte, error) { return m.handleActuate(req), nil }
	return func() { testCallHostSign, testCallHostActuate = oldSign, oldAct }
}

// lastActuate is the most recent actuate request; it fails the test if there was
// none, so a silent zero value can never be mistaken for a real request.
func (m *cicFlowMock) lastActuate(t *testing.T) cicFlowCall {
	t.Helper()
	if len(m.Actuated) == 0 {
		t.Fatalf("no cic-flow.actuate request was made")
	}
	return m.Actuated[len(m.Actuated)-1]
}

func (m *cicFlowMock) handleSign(req []byte) []byte {
	var r struct {
		DataBase64 string `json:"data_base64"`
	}
	json.Unmarshal(req, &r)
	raw, _ := base64.StdEncoding.DecodeString(r.DataBase64)
	m.Signed = append(m.Signed, string(raw))
	sig := m.Signature
	if sig == "" {
		sig = "FAKESIG=="
	}
	out, _ := json.Marshal(map[string]string{"signature": sig})
	return out
}

func (m *cicFlowMock) handleActuate(req []byte) []byte {
	var r struct {
		Method     string            `json:"method"`
		URL        string            `json:"url"`
		Headers    map[string]string `json:"headers"`
		BodyBase64 string            `json:"body_base64"`
	}
	json.Unmarshal(req, &r)
	body, _ := base64.StdEncoding.DecodeString(r.BodyBase64)
	call := cicFlowCall{Method: r.Method, URL: r.URL, Headers: r.Headers, Body: body, Raw: string(req)}
	m.Actuated = append(m.Actuated, call)

	reply := cicFlowReply{Status: 200, Body: "{}"}
	if m.Reply != nil {
		reply = m.Reply(len(m.Actuated), call)
	}
	if reply.Error != "" {
		out, _ := json.Marshal(map[string]string{"error": reply.Error})
		return out
	}
	headers := reply.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	out, _ := json.Marshal(map[string]interface{}{
		"status":      reply.Status,
		"headers":     headers,
		"body_base64": base64.StdEncoding.EncodeToString([]byte(reply.Body)),
	})
	return out
}

// wireHostSeam points the host-build (non-wasm) cic-flow.send seam at this mock, for the fast unit
// tests that call Execute/Observe directly. The caller defers the returned restore func. The guest
// has exactly one seam now, so this wires exactly one function.
func (m *cicFlowMock) wireHostSeam() (restore func()) {
	old := testCallHostSend
	testCallHostSend = func(req []byte) ([]byte, error) { return m.handleSend(req), nil }
	return func() { testCallHostSend = old }
}

// adaptCicFlow wraps a JSON request/response handler as a wazero host function
// following the git/cic-flow ABI: read the request from linear memory, write the
// response back, return bytes written (or -1 if it does not fit).
func adaptCicFlow(handler func(req []byte) []byte) func(context.Context, api.Module, uint32, uint32, uint32, uint32) int32 {
	return func(ctx context.Context, mod api.Module, reqPtr, reqLen, outPtr, outLen uint32) int32 {
		req, ok := mod.Memory().Read(reqPtr, reqLen)
		if !ok {
			return -1
		}
		resp := handler(req)
		if uint32(len(resp)) > outLen || !mod.Memory().Write(outPtr, resp) {
			return -1
		}
		return int32(len(resp))
	}
}

// loadGuestWithCicFlow compiles and instantiates the guest binary against mock
// as its `cic-flow` host module, so a test drives the REAL Call export over the
// real wasmimport surface. Same handle tuple as loadModule.
//
// It loads wasmBinaryPath(): module.wasm by default, or the scratch binary named
// by CIC_MODULE_WASM_PATH — the only way for a test to exercise guest source
// changes that the committed, signed artifact does not contain yet.
func loadGuestWithCicFlow(t *testing.T, mock *cicFlowMock) (context.Context, api.Module, api.Function, api.Function, api.Function) {
	t.Helper()

	path := wasmBinaryPath()
	wasmBytes, skip, err := guestBinaryBytes()
	if skip != "" {
		t.Skip(skip)
	}
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	rt := wazero.NewRuntime(ctx)
	t.Cleanup(func() { rt.Close(ctx) })
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, rt); err != nil {
		t.Fatalf("wasi: %v", err)
	}
	if _, err := rt.NewHostModuleBuilder("cic-flow").
		NewFunctionBuilder().WithFunc(adaptCicFlow(mock.handleSend)).
		WithParameterNames("req_ptr", "req_len", "out_ptr", "out_len").Export("send").
		Instantiate(ctx); err != nil {
		t.Fatalf("cic-flow host module: %v", err)
	}

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		t.Fatalf("compile %s: %v", path, err)
	}
	// No _start: guest modules are libraries, not applications (cicwasm.go:177-178).
	instance, err := rt.InstantiateModule(ctx, compiled,
		wazero.NewModuleConfig().WithName("cicflow_mock_test").WithStartFunctions())
	if err != nil {
		t.Fatalf("instantiate: %v", err)
	}
	t.Cleanup(func() { instance.Close(ctx) })

	callFn := instance.ExportedFunction("Call")
	allocateFn := instance.ExportedFunction("allocate")
	deallocateFn := instance.ExportedFunction("deallocate")
	if callFn == nil || allocateFn == nil || deallocateFn == nil {
		t.Fatalf("guest does not export Call/allocate/deallocate — cicwasm.go:243-247")
	}
	return ctx, instance, callFn, allocateFn, deallocateFn
}
