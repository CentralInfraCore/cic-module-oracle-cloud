# Spec — host capability boundary

**Piece:** P1.1–P1.4 · **Status:** todo (CIC-Relay) · **Relay is read-only from
here — see [relay-requirements.md](../relay-requirements.md).**

How a sandboxed provider module reaches the network and credentials. This
describes the relay's **actual** trust-flow as it exists today, then the delta a
provider module needs. An earlier draft of this spec described two clean
`http.request` / `vault.sign` host functions; that was wrong — the relay does not
work that way. This is the corrected account, read from `CIC-Relay/ffi/src/lib.rs`
(feature-008).

## What the relay actually has today

Not two primitives — one **integrated trust-flow**, `cic_ffi_run_flow` (a stable
C-ABI FFI entry, v2). Its `execute_flow` is five atomic steps:

1. **The module mints its own identity.** The `module` crate (a Rust custody
   crate — *not* the WASM guest) generates a per-request P-256 keypair and a
   PKCS#10 CSR. **The private key never leaves this boundary** (zeroizing
   `ModuleIdentity`); only public material (the CSR, later the public key) is
   emitted.
2. **The relay countersigns** the CSR, authorizing the requested scope (CNs) via
   an intermediate CA.
3. **The secret is sealed** to the module's public key.
4. **The credential is opened *inside* the airlock.** The connection typestate
   (`connections` crate) advances; the plaintext credential exists only here,
   never as data outside.
5. **Actuate over HTTP with the opened credential.** `http-executor` runs the
   call under an `EgressPolicy` allowlist + `Limits`, and a
   `HashChainedAuditSink` emits a tamper-evident, non-secret audit entry (on both
   the allow and the deny path) that the Go relay folds into its ProofTrace.

Request shape (`FlowInput`, JSON over the FFI):

```json
{
  "subject": "...", "scope": ["cn1", "cn2"], "secret": "...",
  "method": "POST", "target": "https://...", "headers": {"...": "..."},
  "body_base64": "...", "allow_hosts": ["..."], "insecure": false,
  "trace_id": "..."
}
```

Profiles: `mock` (in-process trust backends) and `live` (only with the
`live-vault` feature — the real Vault chain via `vault-adapter`).

Two facts that shape everything below:

- **The credential is applied as a `Bearer Authorization` header, inside the
  airlock.** `http-executor` strips any caller-supplied `Authorization` and
  applies the opened credential itself; the credential never reaches the caller.
  There is no "sign these bytes, return the signature" primitive.
- **`run_flow` is native-FFI only** (Go ↔ Rust via cgo). It is **not** exposed to
  WASM guests on any branch. The only wazero host module a guest sees today is
  `git` (`cmd/relay/git_host_funcs.go`).

## What a provider module needs — and why the current shape doesn't fit OCI

Two gaps, both recorded as relay requirements
([relay-requirements.md](../relay-requirements.md)):

### 1. The trust-flow is not reachable from a WASM guest

`run_flow` is FFI-only. A WASM provider module cannot drive it. Enabling it means
exposing the trust-flow (or a capability derived from it) as a wazero host
module, following the `git` pattern — this is **P1** and it lives in the relay,
not here. It is bigger than "wrap a crate": it exposes a whole custody flow.

### 2. OCI needs request signing, not a Bearer token

OCI authenticates with **RSA-SHA256 request signing** over canonical headers
(draft-cavage HTTP Signatures), not a Bearer token. The current `actuate` applies
a Bearer credential — that does not authenticate an OCI call.

The natural fit is the **`vault-adapter` transit / countersign** path
(`VaultTransitSigner`, `VaultPkiCountersigner`), which does RSA signing, rather
than the `http-executor` Bearer path. Split cleanly:

- **The module builds the OCI canonical signing string** — which headers, in
  which order, `x-content-sha256` over the body. This is not secret; it is pure,
  provider-specific logic, and it belongs in the guest.
- **The airlock signs that string** with the OCI API key via `vault-adapter`
  transit (RSA-SHA256). The key never enters the guest; the returned signature is
  not secret.
- **The module assembles the `Authorization: Signature ...` header** and the
  request is actuated by `http-executor` under the egress policy — *without* the
  Bearer step.

So the capability a provider module actually needs is a **sign-then-send** shape
(sign a caller-built canonical string with a scoped key, then egress-policed HTTP
with the caller's own auth header), distinct from today's Bearer trust-flow. How
the relay chooses to expose that — extend `run_flow`, or a separate signing +
HTTP host module — is a relay decision. We state the requirement, not the design.

The concrete interface proposal — the `sign`/`actuate` host functions, their JSON
wire contracts, the OCI signing profile (header set + order), and the
capability/audit bindings — is in
[relay-sign-send-interface.md](relay-sign-send-interface.md).

## Capability boundary properties we rely on (already present)

- **Egress allowlist** — `EgressPolicy` bounds outbound hosts; a denied host is
  audited, not silently dropped.
- **Tamper-evident audit** — `HashChainedAuditSink` / `ChainedEntry` records each
  attempt (allow and deny), self-excluding the signature, ready to fold into the
  ProofTrace. The audit never carries the credential, the `Authorization` header,
  or a Vault token.
- **Custody airlock** — the private key never leaves the `module` boundary;
  secrets are sealed and opened inside.

These are the properties that make third-party modules safe. What is missing for
OCI is reachability from WASM and RSA request signing — both in the relay, both
flagged as requirements.

## Language neutrality

Whatever the host functions end up being, their wire form (a `(i32,i32,i32,i32)
-> i32` + JSON-in-linear-memory convention, matching `git`) is language-neutral,
so a guest in Go or Rust imports the same host module. This is the property that
lets third parties choose their own language.
