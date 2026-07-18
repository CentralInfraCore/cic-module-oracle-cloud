# Spec — host functions

**Piece:** P1.1, P1.2 · **Status:** todo (CIC-Relay)

The capability boundary. The relay exposes two host modules to WASM guests —
`http` and `vault` — backed by the existing native Rust crates. This is the
runtime side of the [provider ABI](provider-abi.md)'s `imports`, scoped by each
module's [capability manifest](capability-manifest.md).

## Pattern

Follows the proven `cmd/relay/git_host_funcs.go`: a `HostSetupFn` registers a
typed host module on the wazero runtime via `NewHostModuleBuilder`, each function
using a uniform ABI:

```
(reqPtr i32, reqLen i32, outPtr i32, outLen i32) -> i32
```

The guest writes a JSON request at `(reqPtr, reqLen)` in linear memory; the host
writes the JSON response at `(outPtr, outLen)` and returns the byte count, or
`-1` on error. The guest never receives arbitrary exec/socket access — only these
functions.

The relay stays **provider-agnostic**: `http` and `vault` know nothing about OCI.

## `http` module → `http-executor`

```
http.request(request) -> response
```

```yaml
request:                      # canonical-json in guest memory
  method: POST
  url: https://iaas.eu-frankfurt-1.oraclecloud.com/20160918/vcns
  headers: { "content-type": "application/json", "authorization": "Signature ...", ... }
  body_b64: ...
  trace_id: ...               # threads into the ProofTrace

response:
  status: 200
  headers: { "etag": "...", "opc-request-id": "...", "opc-work-request-id": "..." }
  body_b64: ...
  attempts:                   # every physical attempt, retries included (proof dispatcher)
    - { at: ..., status: 429 }
    - { at: ..., status: 200 }
```

Backed by `http-executor` (`reqwest` + `EgressPolicy`). The host applies the
calling module's egress allowlist **before** dispatch: a URL whose host is not in
the manifest fails with `permission` and is recorded as denied. Retry / eventual
consistency are driven by the plan's `execution_policy`, not implicit config.

## `vault` module → `vault-adapter`

```
vault.sign(request) -> signature
```

```yaml
request:
  handle: "cic-secret://oci/prod-tenancy"   # a declared secret handle, not a key
  algorithm: rsa-sha256                      # OCI request signing
  input_b64: ...                             # the canonical signing string the module built

signature:
  signature_b64: ...
  key_version: 1
```

Backed by `vault-adapter` (transit). The private key stays in Vault; the module
receives only the signature. The host checks the `handle` against the module's
declared `secrets` scope before signing. This is how the OCI signing scheme works
without the key ever entering the sandbox: the **module** knows *what* to sign
(OCI canonicalization), the **host** performs the signature.

## ProofTrace recording (P1.4)

Every host call is evidence. Around each `http.request` the host records the
sanitized request (method, normalized URL/path/query, selected headers, body
hash), each attempt, and the response (status, `opc-request-id`, `ETag`,
`work-request-id`, body hash). Around each `vault.sign` it records the handle,
algorithm, and the hash of the signed input — never the key or the raw secret.

The result: a run is provable end to end — this intent, through this module hash,
made these signed, egress-policed calls, and got these responses.

## Language neutrality

The `(i32,i32,i32,i32)->i32` + JSON-in-linear-memory convention is
language-neutral. A Go guest imports via `//go:wasmimport http request`; a Rust
guest via `#[link(wasm_import_module = "http")] extern`. Same host module, any
guest language — the property that lets third parties choose their own language.
