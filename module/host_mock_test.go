package main

// host_mock_test.go plays the RELAY'S side of cic-flow.send for host-side tests: it receives the guest's
// structured request, rejects anything a real host would refuse (a guest-built Authorization/date/signature),
// builds the canonical string (the work the guest used to do, now the host profile's), signs through the
// injectable testCallHostSign, sends through testCallHostActuate and answers in the sign-send-response shape.
// The guest-side production code never sees any of this.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
)

// Injectable sign/actuate behaviour of the mock host (the former guest-visible host functions).
var (
	testCallHostSign    func(req []byte) ([]byte, error)
	testCallHostActuate func(req []byte) ([]byte, error)
)

func init() { testCallHostSend = mockRelaySend }

// ociTimeFormat is the RFC 1123 GMT format OCI's Date header requires.
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

// ociCanonical builds the string-to-sign and the wire headers (minus
// Authorization) for one request. signedHeaders is the ordered list that must
// appear, in the same order, in the Authorization `headers` field.
//
// For body methods the signed set adds x-content-sha256, content-type,
// content-length (all also sent on the wire); non-body methods sign only
// (request-target) host date.
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

// ociAuthorization assembles the Authorization: Signature header value. The
// headers list must match ociCanonical's signedHeaders order exactly.
func ociAuthorization(keyID string, signedHeaders []string, signatureB64 string) string {
	return `Signature version="1",keyId="` + keyID +
		`",algorithm="rsa-sha256",headers="` + strings.Join(signedHeaders, " ") +
		`",signature="` + signatureB64 + `"`
}

func mockFailed(code, detail string) ([]byte, error) {
	return json.Marshal(map[string]any{"v": 1, "outcome": "failed", "failure": map[string]string{"code": code, "detail": detail}})
}

func mockRelaySend(raw []byte) ([]byte, error) {
	var in struct {
		V       int    `json:"v"`
		Profile string `json:"signing_profile"`
		KeyRef  string `json:"key_ref"`
		Request struct {
			Method  string            `json:"method"`
			Host    string            `json:"host"`
			Path    string            `json:"path"`
			Query   map[string]string `json:"query"`
			Headers map[string]string `json:"headers"`
			Body    *struct {
				Base64 string `json:"base64"`
			} `json:"body"`
		} `json:"request"`
		Intent map[string]string `json:"intent"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return json.Marshal(map[string]string{"error": "bad request JSON", "code": "REQUEST_INVALID"})
	}
	for k := range in.Request.Headers {
		switch strings.ToLower(k) {
		case "authorization", "date", "host", "content-length", "x-content-sha256", "signature":
			return json.Marshal(map[string]any{"v": 1, "outcome": "denied", "denial": map[string]string{"code": "HEADER_FORBIDDEN"}})
		}
	}
	var body []byte
	if in.Request.Body != nil {
		body, _ = base64.StdEncoding.DecodeString(in.Request.Body.Base64)
	}
	target := in.Request.Path
	if len(in.Request.Query) > 0 {
		var parts []string
		for k, v := range in.Request.Query {
			parts = append(parts, k+"="+v)
		}
		target += "?" + strings.Join(parts, "&")
	}
	canonical, signedHeaders, wire := ociCanonical(in.Request.Method, in.Request.Host, target, nowFunc().Format(ociTimeFormat), body)
	if testCallHostSign == nil {
		return mockFailed("SIGNER_ERROR", "no signer")
	}
	sigReq, _ := json.Marshal(map[string]string{"data_base64": base64.StdEncoding.EncodeToString([]byte(canonical))})
	sigRaw, err := testCallHostSign(sigReq)
	if err != nil {
		return mockFailed("SIGNER_ERROR", err.Error())
	}
	var sig struct {
		Signature string `json:"signature"`
		Error     string `json:"error"`
	}
	_ = json.Unmarshal(sigRaw, &sig)
	if sig.Error != "" || sig.Signature == "" {
		return mockFailed("SIGNER_ERROR", sig.Error)
	}
	wire["authorization"] = ociAuthorization("tenancy/user/fp", signedHeaders, sig.Signature)
	for k, v := range in.Request.Headers {
		wire[strings.ToLower(k)] = v
	}
	if testCallHostActuate == nil {
		return mockFailed("TRANSPORT_ERROR", "no transport")
	}
	actReq, _ := json.Marshal(map[string]any{
		"method": in.Request.Method, "url": "https://" + in.Request.Host + target,
		"headers": wire, "body_base64": base64.StdEncoding.EncodeToString(body),
	})
	actRaw, err := testCallHostActuate(actReq)
	if err != nil {
		return mockFailed("TRANSPORT_ERROR", err.Error())
	}
	var act struct {
		Status     int               `json:"status"`
		Headers    map[string]string `json:"headers"`
		BodyBase64 string            `json:"body_base64"`
		Error      string            `json:"error"`
	}
	_ = json.Unmarshal(actRaw, &act)
	if act.Error != "" {
		return mockFailed("TRANSPORT_ERROR", act.Error)
	}
	sum := sha256.Sum256([]byte(act.BodyBase64))
	_ = strconv.Itoa
	return json.Marshal(map[string]any{"v": 1, "outcome": "completed", "http": map[string]any{
		"status": act.Status, "headers": act.Headers, "body_base64": act.BodyBase64, "body_sha256": "sha256:" + base64.StdEncoding.EncodeToString(sum[:]),
	}})
}
