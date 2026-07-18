# Design documentation

Consolidated design for `cic-module-oracle-cloud` and the `cic-module-<provider>`
namespace it opens. Written 2026-07-18 from the design session recorded in
[`theads/2026-07-17-oci-api-and-provider-abi.md`](../../theads/2026-07-17-oci-api-and-provider-abi.md).

## Read in order

1. [architecture.md](architecture.md) — the trust model, the layers, why WASM,
   and how the OCI SDK is decomposed. Start here.
2. [roadmap.md](roadmap.md) — the work, broken into phases and concrete pieces.

## Specifications

- [specs/provider-abi.md](specs/provider-abi.md) — the `cic:provider` interface
  (`validate / observe / plan / execute / poll / invoke / destroy`) and the
  schema-payload envelope. The public interface every module implements.
- [specs/capability-manifest.md](specs/capability-manifest.md) — what a module
  declares it needs, and what the host enforces. The security model for
  third-party modules.
- [specs/host-functions.md](specs/host-functions.md) — the relay's capability
  boundary: the actual trust-flow (`cic_ffi_run_flow`), and the gap between it and
  what OCI needs. Corrected against the relay source.
- [specs/oci-schema-pipeline.md](specs/oci-schema-pipeline.md) — turning the
  OCI Go SDK into a build-time schema source: operation registry and model
  extraction.
- [specs/state-model.md](specs/state-model.md) — intent/state object-level
  correspondence and the `effective_config` projection.

## Requirements toward the relay

- [relay-requirements.md](relay-requirements.md) — CIC-Relay is **read-only** from
  this repo. Relay bugs and needs (e.g. exposing the trust-flow to WASM guests,
  OCI RSA signing) are recorded here and raised to the relay's owner — never
  worked around by editing the relay from here.

## One-paragraph summary

CIC provisioner modules are sandboxed WASM guests. The relay host is
provider-agnostic and gives each guest a narrow capability boundary — signed
HTTP egress and Vault-backed signing — as host functions. A module carries
provider logic (build a request, map a response) but never a secret and never
raw network access; the OCI SDK is a build-time schema source, not a runtime
binary. This is what lets **untrusted, third-party** provider modules run
safely: they are confined by the sandbox, bounded by a declared capability
manifest, signed for provenance, and every host call is recorded in the
ProofTrace.
