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
| P0.1 | `cic:provider` ABI: `validate/observe/plan/execute/poll/invoke/destroy` + envelope | this repo | spec — **unblocked** (trust model is signature-based on `buildHash`, not cross-env rebuild; CI verifies artifact integrity, [#2](https://github.com/CentralInfraCore/cic-module-oracle-cloud/issues/2) downgraded to supply-chain enhancement) | [provider-abi](specs/provider-abi.md) |
| P0.2 | Capability manifest schema | this repo | **done** | [capability-manifest](specs/capability-manifest.md) |
| P0.3 | Extend `abi.schema.yaml` with an `imports:` surface | this repo | todo | [provider-abi](specs/provider-abi.md) |
| P0.4 | Intent/state correspondence + `effective_config` model | this repo | spec | [state-model](specs/state-model.md) |

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
| P2.2 | `go/ast` extractor → operation registry (method, path, req/resp models) | this repo | **in progress** — model/tag extraction done (`tools/oci-extract`); client method+path join pending | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |
| P2.3 | Model → CIC provider contract + module type generator | this repo | todo | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |
| P2.4 | Schema diff / breaking-change gate on SDK bump | this repo | todo | [oci-schema-pipeline](specs/oci-schema-pipeline.md) |

**Exit criterion:** `CreateVcn` / `GetVcn` / `UpdateVcn` / `DeleteVcn` are
extracted from the pinned SDK into a machine-readable operation contract, and a
version bump that removes a field fails the gate.

---

## Phase 3 — The reference module (this repo)

Implement the ABI for a first real resource, then a PoC set. Depends on Phases
0–2.

| ID | Piece | Status |
|----|-------|--------|
| P3.1 | OCI request builder from generated models — first resource: **VCN** | todo |
| P3.2 | OCI signing scheme (canonical request) in the module, via `vault.sign` | todo |
| P3.3 | `observe` → `effective_config` / state mapping for VCN | todo |
| P3.4 | `plan` (diff) → `execute` → `poll` (Work Request) for VCN | todo |
| P3.5 | PoC resource set: VCN, Subnet, RouteTable, SecurityList, NSG (+ rules), Internet/NAT/Service Gateway | todo |

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
