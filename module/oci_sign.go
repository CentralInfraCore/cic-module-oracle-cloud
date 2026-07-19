// Package main — oci_sign.go builds the OCI draft-cavage HTTP Signatures signing
// material. Pure, no build tag (host-testable): the module owns the
// canonicalization (which headers, in which order, x-content-sha256 over the
// body); only the RSA-SHA256 over the canonical string needs the key, and that
// one step goes to the host (cic-flow.sign). See
// docs/design/specs/relay-sign-send-interface.md.
package main

import (
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

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
