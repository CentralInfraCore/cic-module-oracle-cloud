# Roadmap

Work is grouped into phases. Within a phase, pieces are independently
reviewable. A piece's ID (`P1.2`) is stable and can be referenced from issues,
commits, and sub-job specs.

Two repositories are involved:

- **this repo** (`cic-module-oracle-cloud`) — the reference module and the
  contracts.
- **`CIC-Relay`** — the host: the capability boundary and its enforcement.

Legend: **scaffold** = seed exists, unwired · **spec** = specified, not built ·
**todo** = not started.

---

## Phase 0 — Contracts (the public interface)

The interface every module — ours and third parties' — builds against. Nothing
downstream is stable until these are.

| ID | Piece | Where | Status | Spec |
|----|-------|-------|--------|------|
| P0.1 | `cic:provider` ABI: `describe/validate/observe/plan/execute/poll/invoke/destroy` + envelope | this repo | **all 8 ops implemented** (`module/provider.go` + embedded generated schema `module/schemas/vcn.json`): `validate` enforces schema-conformance (required/unknown/type), `plan` computes a real diff → update/replace/action/noop and emits concrete `provider_operations` (Update/Delete+Create/Change… with each op's HTTP method+path from the P2.2 registry), making the plan reviewable/signable; **`execute` implemented** against the relay `cic-flow` host module (R1/R2 landed in CIC_Relay#91) — renders body, builds the OCI draft-cavage canonical string, `cic-flow.sign` → assembles `Authorization` → `cic-flow.actuate`, tested end-to-end via a mock host; `observe` (signed GET → raw state + `effective_config` + revision), `destroy` (signed DELETE), `invoke` (named action), `poll` (Work Request GET → lifecycle) all real on the same core | [provider-abi](specs/provider-abi.md) |
| P0.2 | Capability manifest schema | this repo | **done** | [capability-manifest](specs/capability-manifest.md) |
| P0.3 | Extend `abi.schema.yaml` with an `imports:` surface | this repo | **done** — `abi.schema.yaml` `imports` schema + `project.yaml` `abi.imports` (`cic-flow`: `sign`/`actuate`, provisional); `describe()` reports it. Declaration only (names settled with relay R1/R2) | [provider-abi](specs/provider-abi.md) |
| P0.4 | Intent/state correspondence + `effective_config` model | this repo | **mostly done** — `observe` returns `effective_config` = the structural projection + hand-authored semantic **derivations** (`module/correspondence/*.json`: input-only→output realizations like `isIpv6Enabled`←non-empty `ipv6CidrBlocks`, boolean inversion, rename); name↔ID resolution (needs a provider lookup) and host-side `satisfies` (P4.1) remain | [state-model](specs/state-model.md) |

**Exit criterion:** a third party could read Phase 0 and know exactly what a
module must export, import, declare, and validate.

---

## Phase 1 — Host capability boundary (CIC-Relay)

The runtime side of the Phase 0 contract. Every module depends on this. Follows
the proven `cmd/relay/git_host_funcs.go` pattern.

| ID | Piece | Where | Status | Spec |
|----|-------|-------|--------|------|
| P1.1 | `http` host function → `http-executor` + `EgressPolicy` | CIC-Relay | todo | [host-functions](specs/host-functions.md) |
| P1.2 | `vault` host function → `vault-adapter` (transit sign) | CIC-Relay | todo | [host-functions](specs/host-functions.md) |
| P1.3 | Capability-manifest enforcement (egress/secret scope per module) | CIC-Relay | todo | [capability-manifest](specs/capability-manifest.md) |
| P1.4 | Module signature verification + ProofTrace recording of host calls | CIC-Relay | todo | [architecture](architecture.md) |

**Exit criterion:** a trivial WASM guest can, through host functions, make one
signed, egress-policed HTTP call and have it appear in the ProofTrace — enforced
against a declared manifest.

---

## Phase 2 — OCI schema pipeline (build-time)

Turn the OCI Go SDK into a build-time schema source. Produces generated module
code and CIC contracts; nothing Go ships at runtime.

| ID | Piece | Where | Status | Spec |
|----|-------|-------|--------|------|
| P2.1 | Pin the OCI Go SDK exactly + record source hash | this repo | **done** (`oci-sdk.lock.yaml`) | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |
| P2.2 | `go/ast` extractor → operation registry (method, path, req/resp models) | this repo | **done** — models + client method/path join (`tools/oci-extract`); validated on the real pinned SDK (271 VCN ops, 0 missing method/path) | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |
| P2.3 | Model → CIC provider contract + module type generator | this repo | **in progress** — field-policy + payload-schema emission done (`tools/oci-extract/{policy,schema}.go`); generated schemas for **two** kinds embedded into the guest (`module/schemas/core/{vcn,subnet}.json`, `make oci.generate`) and consumed by `validate`/`plan`, proving the pipeline is not resource-specific; native module-type structs todo | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |
| P2.4 | Schema diff / breaking-change gate on SDK bump | this repo | **done** — integrity gate, per service (`extracted_schema_hashes[<service>]` = sha256 of that service's committed schemas, checked in `test_oci_sdk_lock.py`; oci-extract-full-sweep task C) + semantic `oci-extract -diff` classifying breaking vs compatible per resource (exit 3 on breaking) | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |
| P2.5 | Make the extractor service-agnostic, not just resource-agnostic | this repo | **done** — lifecycle operations and models resolved from the HTTP surface instead of Go name templates (`tools/oci-extract/resolve.go`); **8048/8048 operations resolved, 0 missing method/path across all 171 services** of the pinned SDK (`make oci.audit`); per-operation `path_params` emitted; polymorphic and unresolvable surfaces reported (exit 5) instead of silently emitted; `vcn`+`subnet` config surface unchanged (`oci-extract -diff` reports no change) | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |
| P2.6 | Full-SDK resource classification + coverage sweep | this repo | **done** — `tools/oci-extract/cmd/oci-sweep` resolves every `Get<X>` resource candidate across all 168 service packages (federation_client.go's directory counted separately, see task B exclusions): **685 class A** (mechanical create+read, current pipeline handles it), **621 class B** (read only, no usable create surface — mostly legitimate read-only byproducts like `WorkRequest`), **62 class C** (no read model — not a resource), **106 class D** (polymorphic create and/or read, 83/90 respectively — reported, not expanded); operation coverage unchanged at 8048/8048 (see `output/service-coverage.md` in `oci-extract-full-sweep` for the full per-service table) | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |

**Exit criterion:** `CreateVcn` / `GetVcn` / `UpdateVcn` / `DeleteVcn` are
extracted from the pinned SDK into a machine-readable operation contract, and a
version bump that removes a field fails the gate.

---

## Phase 3 — The reference module (this repo)

Implement the ABI for a first real resource, then a PoC set. Depends on Phases
0–2.

| ID | Piece | Status |
|----|-------|--------|
| P3.1 | OCI request builder from generated models — first resource: **VCN** | **verified** — `execute` (Create) builds `CreateVcn`/`CreateSubnet` from the generated models and both succeed against a real tenancy (`docs/design/manual-verification.md`, `execute (Create)` row) |
| P3.2 | OCI signing scheme (canonical request) in the module, via `vault.sign` | **verified** — every real-OCI `execute` call in `manual-verification.md` (Create/Update/Delete, `http_status` 200/204) is a real signed request accepted by OCI; the signing itself is R2 (`relay-requirements.md`, done) |
| P3.3 | `observe` → `effective_config` / state mapping for VCN | **verified** — `manual-verification.md`, `observe` row: real signed GET against a live tenancy for `cic:network:vcn` and `cic:network:subnet`, `state` confirmed OCI's raw response, `effective_config` the derived projection |
| P3.4 | `plan` (diff) → `execute` → `poll` (Work Request) for VCN | **partial** — `plan` verified on `cic:network:subnet` (noop + `displayName` update, `manual-verification.md`); `execute` verified on both `vcn`+`subnet` (Create/Update/Delete); `poll` **blocked** — no live Work Request found for `CreateVcn`/`UpdateVcn` (both synchronous, no `opc-work-request-id`). oci-extract-full-sweep task E named a concrete unblocking candidate: `core:LaunchInstance` (`output/async-operations.md`) — same `core` package, real `opc-work-request-id` response header, not yet tried |
| P3.5 | PoC resource set | **todo** — was a hand-picked list (VCN, Subnet, RouteTable, SecurityList, NSG + rules, Internet/NAT/Service Gateway); the extraction mechanism is no longer the reason that list was hand-picked (oci-extract-full-sweep task B: the build-time registry now mechanically classifies **685** class-A resources — has both a read model and a resolvable create operation — across all 168 pinned-SDK services, not just `core`/network). The hand-picked list above is a **deliberate PoC-scope subset** of that mechanical registry, not a workaround for what the extractor could reach: it stays the right list because it is what `manual-verification.md` has actually proven against real OCI so far, and because embedding stays selective by design (`output/embedding-strategy.md` — even the full 685-resource surface fits under the relay's default 16 MiB cap, but "fits" is not a reason to embed services a module never imports) |

**Exit criterion:** a declared VCN intent is provisioned end-to-end and a second
`observe` reports it satisfied, with no false drift.

---

## Phase 4 — Verification & proof

| ID | Piece | Where | Status |
|----|-------|-------|--------|
| P4.1 | Object-level `satisfies(state, intent)` verification | CIC-Relay (host) | todo |
| P4.2 | ProofTrace evidence: plan hash, request/response hash, module hash, schema hash | CIC-Relay | todo |
| P4.3 | End-to-end signed provisioning demo (VCN + subnet + route + gateway) | both | todo |

**Exit criterion:** a provisioning run produces a signed ProofTrace linking
intent commit → module hash → plan → recorded calls → verified state.

---

## Phase 5 — Third-party enablement

The reason for the sandbox. Turns the pattern into a program others can join.

| ID | Piece | Status |
|----|-------|--------|
| P5.1 | Module packaging + signing flow for external authors | todo |
| P5.2 | Capability-manifest review/approval flow | todo |
| P5.3 | Module registry / distribution | todo |
| P5.4 | Second provider (`cic-module-aws`) to validate namespace generality | todo |

**Exit criterion:** an external author can build, sign, declare, and submit a
provider module, and a second provider proves the host stayed provider-agnostic.

---

## Critical path

```
P0.1 provider ABI ─┬─► P0.3 imports surface ─► P3.x reference module
                   │
                   ├─► P1.1/P1.2 host functions ─► P1.3 manifest enforce ─► P1.4 signature+proof
                   │
P0.2 manifest ─────┘

P2.x schema pipeline ─────────────────────────► P3.x reference module

P3.x ─► P4.x verification/proof ─► P4.3 demo ─► P5.x third-party
```

The natural starting point is **P0.1 + P0.2** (the contracts), because they bind
everything else: P1 is their runtime, P3 implements them, P5 opens them to
others.
