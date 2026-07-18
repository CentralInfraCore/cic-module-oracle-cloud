# Spec — OCI schema pipeline

**Piece:** P2.1–P2.4 · **Status:** todo

Turn the OCI Go SDK into a **build-time schema source**. It produces generated
module code and CIC provider contracts. Nothing Go ships at runtime.

## Why the SDK, and why build-time only

OCI publishes no single machine-readable OpenAPI catalogue, but the official Go
SDK is a generated, exact, versioned service model — better structured for
extraction than the HTML docs. The SDK cannot run inside the WASM sandbox (it
needs sockets, `net/http`, crypto), so it is never a runtime dependency: it is
read once, at build time, by a generator that emits module code + contracts.

## Inputs the SDK exposes

Three file kinds per service, all machine-readable via `go/ast` (comments
preserved — `go/doc`):

```
*_client.go            → operation, HTTP method, path, service name
*_request_response.go  → path/query/header/body params via struct tags
*_details.go, models   → type graph, required/optional, enums, nested types
```

Struct tags carry the contract:

```go
type CreateVcnDetails struct {
    CompartmentId *string           `mandatory:"true"  json:"compartmentId"`
    CidrBlock     *string           `mandatory:"false" json:"cidrBlock"`
    FreeformTags  map[string]string `mandatory:"false" json:"freeformTags"`
}

type CreateVcnRequest struct {
    CreateVcnDetails `contributesTo:"body"`
    OpcRetryToken *string `contributesTo:"header" name:"opc-retry-token"`
}
type CreateVcnResponse struct {
    Vcn   `presentIn:"body"`
    Etag  *string `presentIn:"header" name:"etag"`
}
```

| Tag | Meaning |
|-----|---------|
| `mandatory` | required / optional |
| `json` | JSON field name |
| `contributesTo:"body\|header\|path\|query"` | where a request field goes |
| `name` | actual HTTP parameter name |
| `presentIn:"body\|header"` | where a response field is read |

## P2.1 — Pin exactly · done

OCI ships **breaking changes in minor versions** (only `common` breaks bump
major). So pin precisely and record provenance; never `@latest`.

Done: [`oci-sdk.lock.yaml`](../../../oci-sdk.lock.yaml) pins
`github.com/oracle/oci-go-sdk/v65 v65.121.0` at commit
`c46dec5c0e366f206199c1b44a4e090ee1c9af99`, with the go.sum `h1:` module and
`go.mod` hashes recorded from the Go checksum transparency log (sum.golang.org) —
tamper-evident provenance. `extracted_schema_hash` is `null` until the extractor
(P2.2) fills it. `tests/test_oci_sdk_lock.py` checks the lock's shape and
internal consistency (version ↔ VCS ref, full commit sha, h1 hash form).

## P2.2 — Extractor → operation registry · done

A Go tool (`go/ast`, comments kept) over the pinned source produces a
machine-readable registry joining the three file kinds.

**Models** (`tools/oci-extract/extract.go`): each struct's fields with their
`json` name, `mandatory` flag, `contributesTo` / `presentIn` placement, and
`name` HTTP parameter — from request/response/details files, keeping doc
comments.

**Operations** (`tools/oci-extract/client.go`): each public client method joined
with its HTTP verb + path and request/response types. The method+path come from
the private method's `request.HTTPRequest(http.Method*, "<path>", …)` call; the
public↔private link is the SDK's naming convention (`CreateVcn` ↔ `createVcn`).
Helper methods without a `*Request`/`*Response` signature and an HTTPRequest call
are skipped.

The CLI routes `*_client.go` to the operation extractor and any other file to the
model extractor, emitting a single `{operations, models}` registry as canonical
JSON. `make oci.extract.test` (go vet + go test on a VCN client + model fixture)
is enforced in CI — deterministic, no network.

**Validated against the real pinned SDK** (v65.121.0, not just the fixture):
`core_virtualnetwork_client.go` yields **271 operations, zero with a missing
method/path** (verbs: POST 104, GET 103, PUT 33, DELETE 29, PATCH 2), e.g.
`CreateVcn → POST /vcns`. Reproduce:

```sh
go mod download github.com/oracle/oci-go-sdk/v65@v65.121.0   # into GOMODCACHE
go run ./cmd/oci-extract "$SDK/core/core_virtualnetwork_client.go"
```

`oci-sdk.lock.yaml`'s `extracted_schema_hash` stays `null` until the full,
per-service extraction file set is pinned (the input to the P2.4 breaking-change
gate) — filling it from a single client file would not be the artifact that gate
diffs.

The target registry entry:

```yaml
operation: create_vcn
service: core
client: VirtualNetworkClient
http: { method: POST, path: /vcns }
request:
  body:   { type: CreateVcnDetails }
  headers:
    opc-retry-token: { type: string, required: false }
response:
  body:   { type: Vcn }
  headers: { etag: { type: string }, opc-request-id: { type: string } }
```

## P2.3 — Contract + module type generator · in progress

From the registry + model graph, generate:

1. **Field policy** · **done** (`tools/oci-extract/policy.go`) — comparing
   `Create`/`Update`/`Read` models per field:

   | field | Create | Update | Read | policy |
   |-------|--------|--------|------|--------|
   | displayName | ✓ | ✓ | ✓ | mutable |
   | dnsLabel | ✓ | – | ✓ | create-only |
   | compartmentId | ✓ | – | ✓ | action (`ChangeVcnCompartmentDetails`) |
   | lifecycleState | – | – | ✓ | output-only |

   The crucial rule — a field absent from `UpdateDetails` is **not
   automatically immutable** — is implemented: `DeriveFieldPolicy` consults the
   action models (`Change*`/`Add*`/`Remove*…Details`) before ever calling a
   field create-only, and names the action that governs it. Classes: `mutable`,
   `action`, `create-only`, `input-only` (accepted at create, never read back),
   `output-only`. Tested on a fixture and **validated on the real pinned SDK**:
   `ResourcePolicy(models, "Vcn")` over the real VCN models classifies 22 fields
   correctly, including `compartmentId → action (ChangeVcnCompartmentDetails)`.
   Reproduce:

   ```sh
   go run ./cmd/oci-extract -policy Vcn \
     "$SDK/core/create_vcn_details.go" "$SDK/core/update_vcn_details.go" \
     "$SDK/core/vcn.go" "$SDK/core/change_vcn_compartment_details.go"
   ```

2. **Payload schemas** · todo — emit `cic:network:vcn-config` / `…-state` schemas
   from the field policy + model type graph.

3. **Module types** · todo — request/response structs in the module's language
   (generated Go or Rust), so the module marshals payloads without the SDK
   runtime.

4. Optionally a **CIC YANG** model (Oracle publishes none for OCI), carrying CIC
   extensions the SDK tags cannot express: `create-only`, `immutable`,
   `requires-replacement`, `action-managed`, `provider-computed`.

## P2.4 — Breaking-change gate

On an SDK bump: re-extract, diff the schema against the pinned `extracted_schema_hash`,
and fail the gate on any removed/renamed field or newly-required field until an
adapter update + review promotes the new version. This turns OCI's minor-version
breakage from a silent runtime failure into a caught build failure.

## Split, don't monolith

Extract per service and build only what a module imports (Go compiles only
imported packages). The reference module starts with `core` (network); other
providers/services are separate modules — never one binary importing all of OCI.
