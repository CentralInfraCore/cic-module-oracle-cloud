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

## R1 — Expose the trust-flow to WASM guests · **done** ([CIC_Relay#87](https://github.com/CentralInfraCore/CIC_Relay/issues/87), landed in #91)

**Delivered.** The relay built the `cic-flow` wazero host module
(`cmd/relay/cic_flow_host_funcs.go`) — a **separate**, guest-facing, **pure-Go**
surface (not an extension of `run_flow`, which stays the integrated Bearer FFI
path). It is per-module live-wired (`cicFlowRegistry` resolves the calling
guest's deps by `mod.Name()`), bound at module load, and fail-closed on
CICSourceCA provenance. A WASM guest imports `cic-flow.sign` / `cic-flow.actuate`.

**Need (original).** A sandboxed provider module must be able to drive a signed,
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

## R2 — OCI request signing (RSA-SHA256 canonical), not Bearer · **done** ([CIC_Relay#88](https://github.com/CentralInfraCore/CIC_Relay/issues/88), landed in #91)

**Delivered.** `VaultCryptoService.SignOCI` pins `pkcs1v15` / `sha2-256` (Vault
Transit's RSA default is PSS, which OCI rejects) and returns the raw base64
signature; the key never leaves Vault. `cic-flow.actuate` carries the caller's
**own** `Authorization` verbatim — no Bearer, no stripping. Proven end-to-end by
the relay's `make demo-oci-sign` (a fake OCI endpoint verifies the draft-cavage
RSA-SHA256 signature → 200; a tampered body → 401). Confirmed: OCI API signing
accepts **RSA only** (PEM, ≥2048-bit), so `rsa-sha256` is correct, not a
placeholder; the signer stays algorithm-parametric for other providers.

**Need (original).** OCI authenticates each request with RSA-SHA256 over canonical
headers (draft-cavage HTTP Signatures). The relay's actuation applies a **Bearer**
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
in [specs/relay-sign-send-interface.md](specs/relay-sign-send-interface.md),
raised as [CIC_Relay#89](https://github.com/CentralInfraCore/CIC_Relay/issues/89)
(covers R1+R2 together).

Maps to roadmap **P1.2**. This is the item that most needs relay-team input,
because it touches the actuation/credential model.

## R3 — Per-module capability-manifest enforcement · **mostly done** (feature-011)

**Delivered.** R3a: capability-manifest parsing from the signed `project.yaml`,
an `abi.imports ⊆ manifest` load-time check, per-module egress policy (host
suffix-glob + method + path + port), sign-only handle binding, and buildHash
integrity — all fail-closed, live-wired per module. R3b: a CICSourceCA provenance
gate (detached companion) activates `pki_verify.go`. Remaining: full `cicSign`
verification once the CA-chain bootstrap lands.

**Need (original).** The host must enforce each module's declared egress hosts and
secret scope (see [specs/capability-manifest.md](specs/capability-manifest.md)),
so an untrusted third-party module is bounded.

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

## R5 — Egress host declaration for tenant/domain-resolved endpoints · needed (scoped: `identitydomains` coverage only — does not block the current core/network PoC)

**Need.** A module whose target service resolves its endpoint from tenant or
deployment configuration at runtime, not from a region/realm template, cannot
declare that host in a static, build-time-signed capability manifest at all —
regardless of glob syntax.

**Evidence** (oci-extract-full-sweep, task F — read from the pinned SDK, not
grepped-and-guessed; and from `CIC-Relay/cmd/relay/cic_flow_manifest_test.go`,
the only place the egress-glob contract is pinned down in the relay tree):

Of the pinned SDK's 168 service packages, 162 build their host from
`common.StringToRegion(region).EndpointForTemplate(<name>, <template>)`, where
`<template>` always ends in the literal `{region}.{secondLevelDomain}` —
`{secondLevelDomain}` resolves per realm (`common/regions.go`: 21 realms, e.g.
`oc1`→`oraclecloud.com`, `oc19`→`oraclecloud.eu`, `oc2`/`oc3`→
`oraclegovcloud.com`, `oc4`→`oraclegovcloud.uk`, `oc8`..`oc52`→
`oraclecloudN.com`). Every one of these 162 is expressible today, with **no**
relay change, as one `egress.host` glob entry per realm the module supports —
`hostGlobMatch` (`cic_flow_manifest_test.go` `TestHostGlobMatch`) matches a
single leading `*` label against a fixed literal suffix, which is exactly this
shape. (4 more services — `osubbillingschedule`, `osuborganizationsubscription`,
`osubsubscription`, `osubusage` — hardcode `https://csaap-e.oracle.com`
regardless of realm; also expressible today, as one exact-host entry with no
wildcard.) **None of this needs an R# — it is a module-side `Describe()` fix
(emit one `egress.host` entry per supported realm instead of one hardcoded
`*.oraclecloud.com`), left for a follow-up job, not a relay gap.**

Two services do not fit that shape at all:

- `objectstorage` (`objectstorage_client.go:100-111`,
  `getEndpointTemplatePerRealm`): when
  `IsOciRealmSpecificServiceEndpointTemplateEnabled` is on **and** the realm is
  `oc1`, the host is `{namespaceName}.objectstorage.{region}.oci.customer-oci.com`
  — a *different apex domain* (`customer-oci.com`, not `oraclecloud.com`) with
  the OCI Object Storage **namespace** (tenant-specific, discovered at runtime
  via a `GetNamespace` call — not a build-time constant) as the leading label.
  A manifest entry `*.objectstorage.<region>.oci.customer-oci.com` (one per
  supported region) *is* expressible with today's single-leading-wildcard glob
  — the namespace is the only variable, and it sits exactly where `hostGlobMatch`
  allows a wildcard. This is a **nice-to-have** clarification, not a blocker: a
  module that declares support for a fixed, enumerable set of regions can list
  one entry per region without any relay change. It would only become a real
  gap if a module needed to declare "any region" from one static entry — that
  needs a second wildcard segment, which `TestHostGlobMatch`'s
  `{"a.*.com", "a.b.com", false} // infix wildcard rejected` case confirms the
  current matcher does not support.
- `identitydomains` (`identitydomains_client.go:29-64`,
  `NewIdentityDomainsClientWithConfigurationProvider`): the host is not derived
  from `common.StringToRegion` at all — it is a required `endpoint string`
  constructor parameter, `client.Host = endpoint` verbatim. Identity Domains
  (IDCS) endpoints are per-domain URLs assigned when a domain is provisioned
  (e.g. `idcs-<guid>.identity.oraclecloud.com`), unknowable at module-build
  time and not derivable from region/realm at all. **No static manifest entry
  can express this**, with any glob syntax the relay might add — the host is a
  runtime value, not a build-time constant.

**Ask.** Not a specific implementation (the relay team owns the design, as with
R2) — the open question is whether/how the capability-manifest model can
support an egress host that is *bounded but not fully static*: e.g. resolved
once at `plan` time from the intent, pinned into the signed plan, and enforced
against that pinned value at `execute` time, rather than checked against a
manifest glob at load time. This is not required for the current PoC (VCN/
Subnet live entirely in `core`/network, which resolves the standard way); it
would block any future module surface that touches `identitydomains`. Maps to
roadmap **P1.3 / P3.5** (future PoC surface expansion) and
**docs/design/roadmap.md**'s Phase 5 third-party enablement, since a
third-party module author has no way to hit this problem today either.

---

## How to raise these

1. Keep this file current as needs are discovered or resolved.
2. For each `needed` item, open an issue in `CIC-Relay` referencing the `R#` id
   and this file; flip the item to `raised` with the issue link.
3. When it lands in the relay, flip to `done` and note the relay commit/tag.

Never work around a relay gap by editing the relay from this repo, or by
duplicating relay logic here to avoid the dependency. If a gap blocks progress,
that is a signal to raise — not to route around.
