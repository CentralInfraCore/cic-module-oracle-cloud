# Requirements toward CIC-Relay

**CIC-Relay is read-only from this repository.** We never edit relay code here.
When provisioning needs a relay change or we find a relay bug, we record it in
this file and raise it to the relay's owner (issue / PR in `CIC-Relay`). This
file is the signal channel; the relay repo is where it gets built.

Each item states the need and the evidence (what we read in the relay), not a
prescribed implementation — the relay team owns the design.

## Status legend

`needed` — required for the OCI PoC · `nice` — improves it · `raised` — an issue
exists in `CIC-Relay` · `done` — landed in the relay.

---

## R1 — Expose the trust-flow to WASM guests · raised ([CIC_Relay#87](https://github.com/CentralInfraCore/CIC_Relay/issues/87))

**Need.** A sandboxed provider module must be able to drive a signed,
egress-policed HTTP actuation. Today it cannot.

**Evidence.** `cic_ffi_run_flow` (`ffi/src/lib.rs`) is a native-FFI-only entry
(Go ↔ Rust via cgo). The only wazero host module a guest sees is `git`
(`cmd/relay/git_host_funcs.go`); there is no `http`/`vault`/`flow` host module on
any branch. The `HostSetupFn` on `main` registers only `makeGitHostSetupFn`.

**Ask.** A wazero host module, following the `git` pattern, that lets a guest
drive the trust-flow (or a capability derived from it). Bigger than wrapping a
crate — it exposes a whole custody flow. A concrete interface proposal (the
`sign`/`actuate` host functions and wire contracts) is in
[specs/relay-sign-send-interface.md](specs/relay-sign-send-interface.md).

Maps to roadmap **P1.1 / P1.4**.

## R2 — OCI request signing (RSA-SHA256 canonical), not Bearer · raised ([CIC_Relay#88](https://github.com/CentralInfraCore/CIC_Relay/issues/88))

**Need.** OCI authenticates each request with RSA-SHA256 over canonical headers
(draft-cavage HTTP Signatures). The relay's actuation applies a **Bearer**
credential, which does not authenticate an OCI call.

**Evidence.** `http-executor/src/lib.rs`: "the opened credential … is applied as a
Bearer `Authorization` header"; the executor strips any caller-supplied
`Authorization` and applies the credential itself. There is no OCI signer in the
tree. `vault-adapter` has `VaultTransitSigner` / `VaultPkiCountersigner` (RSA),
which is the natural home for the signature.

**Ask.** A **sign-then-send** capability: sign a caller-built canonical string
with a scoped key (via `vault-adapter` transit), return the signature, then
actuate egress-policed HTTP carrying the caller's own `Authorization: Signature`
header — i.e. actuation that does **not** force the Bearer step. The provider-
specific canonicalization stays in the module; the key stays in the airlock.

A concrete interface proposal — the OCI signing profile (which headers, in which
order), the `sign`/`actuate` split, and an end-to-end `execute` walkthrough — is
in [specs/relay-sign-send-interface.md](specs/relay-sign-send-interface.md).

Maps to roadmap **P1.2**. This is the item that most needs relay-team input,
because it touches the actuation/credential model.

## R3 — Per-module capability-manifest enforcement · needed

**Need.** The host must enforce each module's declared egress hosts and secret
scope (see [specs/capability-manifest.md](specs/capability-manifest.md)), so an
untrusted third-party module is bounded.

**Evidence.** `EgressPolicy::allowing(hosts)` already bounds egress per
actuation, and the audit sink records denials. What is not yet present is binding
that policy to a **module's signed manifest** at load, and rejecting a module
whose imports exceed its declaration.

**Ask.** Derive the per-module `EgressPolicy` and secret scope from the verified,
signed capability manifest; reject at load on a manifest/imports mismatch; audit
denied attempts. Maps to roadmap **P1.3**.

## R4 — Fold host-call audit into the ProofTrace · nice (partly present)

**Need.** Every host call a module makes is evidence in the run's ProofTrace.

**Evidence.** `HashChainedAuditSink` / `ChainedEntry` already produce a
tamper-evident, non-secret audit entry (allow and deny paths), self-excluding the
signature, and `run_flow` returns it for the Go relay "to fold into its
ProofTrace" (`ffi/src/lib.rs` comments). The mechanism exists on the FFI path.

**Ask.** Ensure the same folding applies when the trust-flow is driven from a
WASM guest (R1) — the audit entry must bind to the module hash and the intent.
Maps to roadmap **P1.4 / P4.2**.

---

## How to raise these

1. Keep this file current as needs are discovered or resolved.
2. For each `needed` item, open an issue in `CIC-Relay` referencing the `R#` id
   and this file; flip the item to `raised` with the issue link.
3. When it lands in the relay, flip to `done` and note the relay commit/tag.

Never work around a relay gap by editing the relay from this repo, or by
duplicating relay logic here to avoid the dependency. If a gap blocks progress,
that is a signal to raise — not to route around.
