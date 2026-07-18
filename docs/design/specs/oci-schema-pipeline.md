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

## P2.1 — Pin exactly

OCI ships **breaking changes in minor versions** (only `common` breaks bump
major). So pin precisely and record provenance; never `@latest`:

```yaml
provider_dependency:
  name: oracle-oci-go-sdk
  version: v65.121.0
  source_commit: c46dec5...
  source_archive_sha256: ...     # Oracle publishes checksums
  extracted_schema_hash: ...
```

## P2.2 — Extractor → operation registry

A Go tool (`go/ast`, comments kept) over the pinned source produces a
machine-readable registry joining the three file kinds:

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

## P2.3 — Contract + module type generator

From the registry + model graph, generate:

1. **CIC provider contracts** — the payload schemas (`cic:network:vcn-config`,
   `…-state`) and the field policy, from comparing `Create`/`Update`/`Read`
   models:

   | field | Create | Update | Read | policy |
   |-------|--------|--------|------|--------|
   | displayName | ✓ | ✓ | ✓ | mutable |
   | dnsLabel | ✓ | – | ✓ | create-only |
   | compartmentId | ✓ | – | ✓ | action (`ChangeVcnCompartment`) |
   | lifecycleState | – | – | ✓ | output-only |

   A field absent from `UpdateDetails` is **not automatically immutable** — a
   second pass searches action request models (`Add*`/`Remove*`/`Change*`)
   before classifying it immutable.

2. **Module types** — request/response structs in the module's language
   (generated Go or Rust), so the module marshals payloads without the SDK
   runtime.

3. Optionally a **CIC YANG** model (Oracle publishes none for OCI), carrying CIC
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
