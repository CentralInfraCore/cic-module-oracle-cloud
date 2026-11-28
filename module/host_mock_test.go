//go:build !wasip1

package main

// host_mock_test.go plays the RELAY'S side of cic-flow.send in the module's tests. It takes the
// guest's structured request, refuses what a real host refuses (a guest-supplied Authorization,
// date, host, content-length, content digest or signature header), and then does the work the host
// owns since O-1 WP-04: build the OCI canonical string, sign it, send it, and answer in the
// sign-send-response shape.
//
// Why the old formulas live HERE and not in the guest: the canonical string, the signed header set
// and the Authorization assembly moved out of module/oci_sign.go into the relay
// (pkg/hostsign/ociprofile). This file keeps a copy ONLY to simulate the relay; the normative
// assertions about that formula are the relay's tests (see output/claim-mapping.md of the WP-04b
// job). Nothing here is reachable from the guest's production path, and nothing here signs or
// sends anything for real.
//
// The signer and the transport stay injectable through testCallHostSign / testCallHostActuate, so
// every existing test that drove the old two-call seam keeps driving the same two steps -- but now
// on the HOST side of the boundary, which is where they belong.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// testCallHostSign and testCallHostActuate are the injectable signer and transport of the MOCK
// HOST (they used to be the guest's own host imports). A test sets them to decide what the
// simulated relay signs with and what the provider answers.
var (
	testCallHostSign    func(req []byte) ([]byte, error)
	testCallHostActuate func(req []byte) ([]byte, error)
)

// init makes the simulated relay the default host of the whole test binary: a test that only wires
// a signer and a transport (the pre-WP-04 way) still drives the full guest path, because the guest
// now talks to cic-flow.send and this mock host is what answers it.
func init() { testCallHostSend = mockRelaySend }

// ociTimeFormat is the RFC 1123 GMT format OCI's Date header requires. The HOST sets this date;
// the guest never does.
const ociTimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

// bodyMethod reports whether a method carries (and signs) a body.
func bodyMethod(method string) bool {
	switch strings.ToUpper(method) {
	case "POST", "PUT", "PATCH":
		return true
	default:
		return false
	}
}

// ociCanonical builds the string-to-sign and the wire headers (minus Authorization) for one
// request. signedHeaders is the ordered list that must appear, in the same order, in the
// Authorization `headers` field.
//
// For body methods the signed set adds x-content-sha256, content-type, content-length (all also
// sent on the wire); non-body methods sign only (request-target) host date.
func ociCanonical(method, host, path, date string, body []byte) (canonical string, signedHeaders []string, wireHeaders map[string]string) {
	reqTarget := strings.ToLower(method) + " " + path
	lines := []string{
		"(request-target): " + reqTarget,
		"host: " + host,
		"date: " + date,
	}
	signedHeaders = []string{"(request-target)", "host", "date"}
	wireHeaders = map[string]string{"date": date}

	if bodyMethod(method) {
		sum := sha256.Sum256(body)
		xcs := base64.StdEncoding.EncodeToString(sum[:])
		ct := "application/json"
		cl := strconv.Itoa(len(body))
		lines = append(lines,
			"x-content-sha256: "+xcs,
			"content-type: "+ct,
			"content-length: "+cl,
		)
		signedHeaders = append(signedHeaders, "x-content-sha256", "content-type", "content-length")
		wireHeaders["x-content-sha256"] = xcs
		wireHeaders["content-type"] = ct
		wireHeaders["content-length"] = cl
	}
	return strings.Join(lines, "\n"), signedHeaders, wireHeaders
}

// ociAuthorization assembles the Authorization: Signature header value. The headers list must
// match ociCanonical's signedHeaders order exactly.
func ociAuthorization(keyID string, signedHeaders []string, signatureB64 string) string {
	return `Signature version="1",keyId="` + keyID +
		`",algorithm="rsa-sha256",headers="` + strings.Join(signedHeaders, " ") +
		`",signature="` + signatureB64 + `"`
}

// mockHostKeyID is the key identifier the simulated host puts in the Authorization header. It is
// the HOST's, resolved from its own configuration: no guest request names it.
const mockHostKeyID = "tenancy/user/fp"

// wireSendRequest is the guest request as the host parses it (relay pkg/hostsign wireRequest v1).
type wireSendRequest struct {
	V              int    `json:"v"`
	SigningProfile string `json:"signing_profile"`
	KeyRef         string `json:"key_ref"`
	Request        struct {
		Method  string            `json:"method"`
		Host    string            `json:"host"`
		Path    string            `json:"path"`
		Query   map[string]string `json:"query"`
		Headers map[string]string `json:"headers"`
		Body    *struct {
			Base64 string `json:"base64"`
		} `json:"body"`
	} `json:"request"`
	Intent struct {
		Operation     string `json:"operation"`
		ResourceID    string `json:"resource_id"`
		BindingDigest string `json:"binding_digest"`
	} `json:"intent"`
}

// mockDenied is a policy refusal: nothing was signed and nothing was sent.
func mockDenied(code, detail string) []byte {
	out, _ := json.Marshal(map[string]any{"v": 1, "outcome": "denied",
		"denial": map[string]string{"code": code, "detail": detail}})
	return out
}

// mockFailed is an admitted call that did not complete (signer or transport).
func mockFailed(code, detail string) []byte {
	out, _ := json.Marshal(map[string]any{"v": 1, "outcome": "failed",
		"failure": map[string]string{"code": code, "detail": detail}})
	return out
}

// mockHostError is a host-level error that is not a v1 answer at all.
func mockHostError(code, msg string) []byte {
	out, _ := json.Marshal(map[string]string{"error": msg, "code": code})
	return out
}

// mockSendTarget is the request target the host signs: path plus the query in sorted key order
// (the relay's PrepRequest.Target()).
func mockSendTarget(path string, query map[string]string) string {
	if len(query) == 0 {
		return path
	}
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+query[k])
	}
	return path + "?" + strings.Join(parts, "&")
}

// mockRelaySend is the simulated cic-flow.send host function: validate what the host validates,
// then sign through testCallHostSign and send through testCallHostActuate.
func mockRelaySend(raw []byte) ([]byte, error) {
	var in wireSendRequest
	if err := json.Unmarshal(raw, &in); err != nil {
		return mockHostError(hostCodeRequestInvalid, "request is not valid JSON"), nil
	}
	if in.V != 1 {
		return mockHostError(hostCodeRequestInvalid, "unsupported request version"), nil
	}
	if in.SigningProfile != ociSigningProfile {
		return mockDenied(hostCodeProfileUnknown, in.SigningProfile), nil
	}
	// The host owns these headers; a guest that sets one is refused before anything is signed.
	for k := range in.Request.Headers {
		switch strings.ToLower(k) {
		case "authorization", "date", "host", "content-length", "x-content-sha256", "signature":
			return mockDenied(hostCodeHeaderForbidden, strings.ToLower(k)), nil
		}
	}
	var body []byte
	if in.Request.Body != nil {
		body, _ = base64.StdEncoding.DecodeString(in.Request.Body.Base64)
	}
	target := mockSendTarget(in.Request.Path, in.Request.Query)

	canonical, signedHeaders, wire := ociCanonical(in.Request.Method, in.Request.Host, target, nowFunc().Format(ociTimeFormat), body)
	if testCallHostSign == nil {
		return mockFailed(hostCodeSignerError, "no signer wired"), nil
	}
	sigReq, _ := json.Marshal(map[string]string{"data_base64": base64.StdEncoding.EncodeToString([]byte(canonical))})
	sigRaw, err := testCallHostSign(sigReq)
	if err != nil {
		return mockFailed(hostCodeSignerError, err.Error()), nil
	}
	var sig struct {
		Signature string `json:"signature"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(sigRaw, &sig)
	if sig.Error != "" || sig.Signature == "" {
		return mockFailed(hostCodeSignerError, sig.Error), nil
	}
	wire["authorization"] = ociAuthorization(mockHostKeyID, signedHeaders, sig.Signature)
	// The guest's own (allowed) headers travel unsigned, the way OCI's fixed signed set works.
	for k, v := range in.Request.Headers {
		wire[strings.ToLower(k)] = v
	}

	if testCallHostActuate == nil {
		return mockFailed(hostCodeTransportError, "no transport wired"), nil
	}
	actReq, _ := json.Marshal(map[string]any{
		"method":      in.Request.Method,
		"url":         "https://" + in.Request.Host + target,
		"headers":     wire,
		"body_base64": base64.StdEncoding.EncodeToString(body),
	})
	actRaw, err := testCallHostActuate(actReq)
	if err != nil {
		return mockFailed(hostCodeTransportError, err.Error()), nil
	}
	var act struct {
		Status     int               `json:"status"`
		Headers    map[string]string `json:"headers"`
		BodyBase64 string            `json:"body_base64"`
		Error      string            `json:"error"`
	}
	_ = json.Unmarshal(actRaw, &act)
	if act.Error != "" {
		return mockFailed(hostCodeTransportError, act.Error), nil
	}
	respBody, _ := base64.StdEncoding.DecodeString(act.BodyBase64)
	sum := sha256.Sum256(respBody)
	out, _ := json.Marshal(map[string]any{"v": 1, "outcome": "completed", "http": map[string]any{
		"status":      act.Status,
		"headers":     act.Headers,
		"body_base64": act.BodyBase64,
		"body_sha256": "sha256:" + hex.EncodeToString(sum[:]),
	}})
	return out, nil
}
