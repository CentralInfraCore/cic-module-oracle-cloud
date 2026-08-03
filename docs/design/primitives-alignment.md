# Alignment with cic-primitives (the repo named "cic-yang" on GitHub)

This module has **zero code-level connection** to `CentralInfraCore/cic-yang`
(internally called `cic-primitives`; local clone under
`primitives-group/cic-yang` in this machine's checkout layout) — verified by
grepping this repo's `.go`/`.yaml`/`.md` files for `cic-primitives`,
`cic-yang`, `ManagedEntity`, `config-surface`: no hits outside plain English
prose ("config-surface projection" is just a descriptive phrase in
`provider.go`, not a reference to the other repo's schema).

This is not an oversight — see "Recommended direction" below.

## What cic-primitives actually is

Not YANG files. It's CIC's meta-schema layer: 8 atomic primitives (Shape,
Role, Behavior, Contract, Address, Identity, Event, Access) and 5 aggregate
primitives (ManagedEntity, ConfigSurface, StateSurface, OperationSurface,
PolicySurface), composed via git-remote inheritance (`git merge base@0.5.0`).
Every domain object is meant to be a specialization of `ManagedEntity`. One
reality-check example exists (`KubernetesPod`, in-repo only — not a real
external domain repo).

**Its own roadmap (`ai/ROADMAP.md`) marks Phase 6 — "primitive YAML
validation / compiler binding / domain-specialization semantic compatibility
check" — as `concept`, unimplemented, for every subphase.** That's the actual
mechanism a real domain repo (this one, or `cic-module-aws` later) would need
to formally specialize from `ManagedEntity`. It doesn't exist yet, not even
for the one in-repo example. Last commit on that repo: 2026-05-31 — no
activity since, unclear if it's an active line of work right now.

## Recommended direction: keep going API-first, project onto primitives later — don't build from primitives now

This module's actual schema source is `tools/oci-extract` reading the pinned
OCI Go SDK directly (P2.1–P2.4, all done/in-progress) — mechanically correct
and complete against the real API (271/271 VCN ops resolved), with a
breaking-change gate on SDK drift, and now verified against real OCI
end-to-end (Observe/Validate/Plan/Execute create→update→delete, see
`manual-verification.md`).

Building this module's resource contracts *from* `cic-primitives` instead
would mean hand-modeling OCI's real operation registry (271 ops, per-field
policy, action-managed compartment changes, async Work Requests, etag
concurrency) against an aggregate-primitive model that has never been
validated against anything that irregular — risking either a distorted
primitive model or a module that quietly drifts from the real API, with no
extraction-time gate to catch it.

**Better order, when/if `cic-primitives` Phase 6 actually lands:** write a
mechanical *projector* from this module's already-correct, already-generated
contract (`module/schemas/{vcn,subnet}.json`) into a `ManagedEntity`
specialization — not by hand-authoring one. The extracted contract already
carries the structured metadata a projection needs:

- `config_surface` ← fields where `x-cic-policy ∈ {mutable, create-only}`
- `state_surface`  ← observed/computed fields (the rest)
- `operation_surface` ← the `operations` map (method + path), already
  registry-derived

This mirrors the existing `tools/oci-extract` pattern (project a trusted
source into the shape another layer needs) instead of introducing a second,
hand-maintained source of truth that can silently disagree with the first.

**Until then:** keep extending resource coverage (RouteTable, SecurityList,
NSG, gateways — the PoC set from the module's own `roadmap.md` P3.5) the same
way — API-first, extraction-driven, real-OCI-verified. Don't block on
`cic-primitives` Phase 6.

## An inconsistency found in cic-primitives' own docs

`cic-yang/README.md` states: *"CIC-Relay | runtime — primitívekből épülő
sémákat futtatja"* ("CIC-Relay — runtime, runs schemas built from
primitives"). Grepping `CIC-Relay`'s entire Go source for
`cic-primitives`/`cic-yang`/`managed-entity`/`managed_entity` returns **zero
hits**. That claimed relationship isn't reflected anywhere in the relay's
actual code — recorded here as evidence for whoever revisits either repo;
not raised as an `R#` item anywhere since it's not a relay *requirement* gap,
just a stale/aspirational claim in a doc.

## How this got misread at first (worth remembering)

The first-pass read of `cic-yang/CLAUDE.md` took its own disclaimer —
*"Nem domain modell. Nem IaC tool. Nem YANG leíró."* ("Not a domain model.
Not an IaC tool. Not a YANG descriptor.") — at face value and concluded the
repo had nothing to do with YANG at all. That's wrong in a way that matters:
every aggregate primitive (e.g. `schemas/aggregate/config-surface.yaml`)
carries an explicit `semantic_mapping.yang` block (`construct`, `name`,
`flag: "config true"`, etc.) alongside `semantic_mapping.api` and
`semantic_mapping.runtime` — the repo is deliberately YANG-*aware* by design,
even though it stores YAML meta-schemas, not `.yang` files. Its git tag
history (`yang/@v0.1.x` → `cic-yang@0.1.x` → `cic-primitives@0.1.x` →
`primitives/@v0.1.x`) confirms it: the repo really did start as "yang" and
was rebranded to "primitives" without the GitHub remote name following.

**Lesson:** a repo's own top-of-file disclaimer prose is not the same as its
actual schema content — the "not X" framing can be true at the file-format
level while X is still a deliberate, load-bearing design concern one level
down. Read the schemas, not just the README, before asserting what a repo
is or isn't about.
