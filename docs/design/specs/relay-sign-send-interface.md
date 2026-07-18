# Spec — relay sign+send host interface (R1/R2 proposal)

**Piece:** P1.1–P1.4 · **Status:** raised in CIC-Relay
([CIC_Relay#89](https://github.com/CentralInfraCore/CIC_Relay/issues/89)) ·
**Relay is read-only from here** — see
[relay-requirements.md](../relay-requirements.md) (R1, R2) and
[host-functions.md](host-functions.md).

This is the concrete interface a provider module needs from the relay to reach
the network with credentials — the module's half of the contract, stated
precisely so the relay team has something to implement against. **It is a
proposal, not a mandate:** the relay owns the final ABI shape (how it maps onto
`run_flow`, the airlock, and `vault-adapter` is a relay decision). What is fixed
is what the module must be able to express and rely on.

It refines the high-level asks in R1 (reach the trust-flow from WASM) and R2
(RSA-SHA256 request signing, not Bearer) into named host functions, wire
contracts, the OCI signing profile, and the capability/audit bindings.

## Shape: two host functions, sign then send

The module already declares its need in `project.yaml` `abi.imports`:

```yaml
abi:
  imports:
    - module: cic-flow
      functions: [sign, actuate]
```

Two functions, matching the **sign-then-send** split
([host-functions.md](host-functions.md#2-oci-needs-request-signing-not-a-bearer-token)):

- **`sign`** — sign a caller-built canonical string with a scoped key (RSA-SHA256
  via `vault-adapter` transit). The key never enters the guest; the signature is
  not secret.
- **`actuate`** — egress-policed HTTP send that carries the caller's **own**
  `Authorization` header (no forced Bearer step), under the module's
  `EgressPolicy` and `Limits`, audited into the ProofTrace.

Splitting them keeps the **provider-specific canonicalization in the guest**
(only the module knows OCI's header set and order) and the **key in the airlock**
(only the relay holds it). Neither half alone can exfiltrate: `sign` never sees
the network, `actuate` never sees the key.

## Wire convention

Language-neutral, matching the `git` host module and the module's own `Call`
ABI: JSON request in linear memory, a `u64`-packed `(len<<32)|ptr` response the
host writes back through the guest's exported `allocate` (the guest then
`deallocate`s it). Proposed signatures — the exact form conforms to the relay's
`git` host module:

```
cic-flow.sign(reqPtr i32, reqLen i32)    -> (packed i64)   // (len<<32)|ptr
cic-flow.actuate(reqPtr i32, reqLen i32) -> (packed i64)
```

Every response is the same `{data, error}` envelope the module already uses
([envelope.md](../../contracts/en/envelope.md)); a host-side denial (egress,
scope, limits) is a typed `error`, never a silent drop.

## `sign`

**Request**

```json
{
  "handle": "oci/prod-api-key",
  "algorithm": "rsa-sha256",
  "string_to_sign": "(request-target): put /20160918/vcns/ocid1.vcn...\nhost: iaas.eu-frankfurt-1.oraclecloud.com\ndate: Thu, 18 Jul 2026 12:00:00 GMT\nx-content-sha256: base64(sha256(body))\ncontent-type: application/json\ncontent-length: 123",
  "trace_id": "..."
}
```

- `handle` — a secret handle the module's signed capability manifest permits
  (`secrets[].handle`, `ops` includes `sign`). The host resolves it to the OCI
  API key in Vault; **the caller never names the key material**.
- `string_to_sign` — the module-built canonical string (see the signing profile
  below). Opaque to the host: it signs the bytes as given.

**Response**

```json
{ "signature": "<base64(RSA-SHA256(string_to_sign))>", "key_id": "<tenancyOCID>/<userOCID>/<fingerprint>" }
```

- `key_id` — the OCI `keyId` the module needs to assemble the `Authorization`
  header. It is public (OCIDs + key fingerprint), not secret. The host knows it
  from the handle→key mapping; returning it keeps that mapping out of the guest.

**Host obligations.** Reject a `handle` outside the module's manifest `secrets`
(scope error). Sign only — no network. Emit an audit entry binding the `handle`,
`key_id`, `algorithm`, and `sha256(string_to_sign)` (not the string if it could
carry sensitive query params — the hash suffices) to the module hash + trace.

## `actuate`

**Request**

```json
{
  "method": "PUT",
  "url": "https://iaas.eu-frankfurt-1.oraclecloud.com/20160918/vcns/ocid1.vcn...",
  "headers": {
    "authorization": "Signature version=\"1\",keyId=\"...\",algorithm=\"rsa-sha256\",headers=\"(request-target) host date x-content-sha256 content-type content-length\",signature=\"...\"",
    "date": "Thu, 18 Jul 2026 12:00:00 GMT",
    "x-content-sha256": "base64(sha256(body))",
    "content-type": "application/json"
  },
  "body_base64": "...",
  "trace_id": "..."
}
```

**Response**

```json
{ "status": 200, "headers": { "etag": "...", "opc-request-id": "..." }, "body_base64": "..." }
```

**Host obligations.**

- **Carry the caller's `Authorization` verbatim** — do **not** strip it or apply
  a Bearer credential. This is the core of R2: the module's
  `Authorization: Signature …` is the authenticator.
- **Enforce egress** from the module's manifest `egress` (`EgressPolicy`): the
  request host must be allow-listed and the method permitted, else a typed
  `error` (audited), not a send.
- **Enforce limits** (`Limits`: timeout, max request/response bytes) from the
  manifest.
- **Never trust caller `allow_hosts`** — the allowlist is the signed manifest,
  not request input.
- Emit an audit entry binding `method`, request host, `sha256(body)`,
  `status`, `sha256(response_body)`, to the module hash + trace.

## OCI signing profile (R2 detail)

The canonicalization the **module** performs before `sign`. Scheme:
draft-cavage-http-signatures-08, `algorithm="rsa-sha256"`.

| Method | Headers signed (in order) |
|--------|---------------------------|
| GET / HEAD / DELETE | `(request-target)` `host` `date` |
| POST / PUT / PATCH | `(request-target)` `host` `date` `x-content-sha256` `content-type` `content-length` |

- `(request-target)` = `<lowercase-method> <path-and-query>`.
- `date` = RFC 1123 (`Date` header), also sent on the wire.
- `x-content-sha256` = `base64(sha256(body))`; `content-length` = body length;
  both also sent on the wire for body methods.
- `keyId` = `<tenancyOCID>/<userOCID>/<fingerprint>` (from `sign`'s `key_id`).
- `Authorization: Signature version="1",keyId="…",algorithm="rsa-sha256",headers="…",signature="…"`.

Only the RSA-SHA256 over `string_to_sign` needs the key — that one step is
`sign`. Everything else is pure guest logic.

## Capability + audit bindings

- **R3 (enforcement).** `sign` `handle` ∈ manifest `secrets`; `actuate` host ∈
  manifest `egress`. The manifest is the *signed* one bound at load, not request
  input. A mismatch is a typed, audited denial.
- **R4 (ProofTrace).** Each `sign`/`actuate` yields a `HashChainedAuditSink`
  entry bound to the module hash + intent/trace, carrying only non-secret hashes
  (never the key, the signature-as-credential beyond what the chain self-excludes,
  or a Vault token). The relay folds it into the run's ProofTrace.

## End-to-end: how `execute` consumes a plan

The provider ABI's `execute` runs an already-approved `plan` verbatim. The plan
now carries concrete `provider_operations` with HTTP method+path (P2.2), so
`execute` is a direct loop over them:

```
for each provider_operation po in plan.provider_operations:
    req_body   = render(po, config)                      # module: build OCI body
    canonical  = oci_string_to_sign(po.method, po.path, host, date, req_body)
    sig        = cic-flow.sign({handle, "rsa-sha256", canonical})   # airlock signs
    authz      = oci_authorization_header(sig.key_id, headers, sig.signature)
    resp       = cic-flow.actuate({po.method, url(po.path), {authz, date, ...}, req_body})
    record(resp.status, resp.headers.etag, resp.headers["opc-request-id"])   # evidence
```

The plan already told us `po.method`/`po.path` (e.g. `PUT /vcns/{vcnId}`,
`POST /vcns/{vcnId}/actions/changeCompartment`); this interface is the only
missing piece between a signed plan and a signed, audited OCI actuation.

## What we are NOT prescribing

- The relay's internal design (extend `run_flow`, or a fresh signing + HTTP host
  module; how the airlock and `vault-adapter` compose).
- Rust types, crate boundaries, or the exact `(i32,i32,…)` ABI beyond "match the
  `git` host module convention".
- The handle→key resolution and manifest-signing mechanism — relay-owned (R3).

We state the capability the module must be able to express — `sign` a canonical
string with a scoped key, then `actuate` egress-policed HTTP carrying its own
auth — and rely on (egress allowlist, custody airlock, tamper-evident audit,
already present per [host-functions.md](host-functions.md)).
