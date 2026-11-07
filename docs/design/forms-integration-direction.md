# Direction: the integration of CIC Forms with this provider module, and the quality levels that come with it

Status: **direction, first round**, 2026-11-07. Documentation only. **No code, no schema, no CI change, no new gate is added by this document**, and nothing in it is `implemented` on the module side: every statement about the integration is `concept` unless marked otherwise, and the three-level status (`implemented` / `scaffold` / `concept`) is used throughout. It records the decided direction so that the first Factory jobs have one source to follow, and it says how the quality-assurance levels of `cic-primitive-forms` are to be carried over to this module, **stage by stage and measured, not declared**.

It builds on [`primitives-alignment.md`](primitives-alignment.md) (written when `cic-primitives` Phase 6 was a concept and no Forms engine existed) and partly supersedes it (section 2). It does not change the trust model of [`architecture.md`](architecture.md) or the interface of [`specs/provider-abi.md`](specs/provider-abi.md).

## 1. What is decided

1. **The target is the original goal of CIC Forms:** a provider-independent, primitives-based common object language. The module is the first provider to be attached to it. Not a new infrastructure object model, and not a second validator inside this repository.
2. **The first slice is narrow:** the OCI resource set the module really supports today (VCN first, then Subnet, then Instance), one direction at a time, **with mocked OCI responses**. No SDK update, no full SDK sweep, no new primitive grammar, no live cloud change.
3. **The test harness is not the Relay.** The first slice drives the Forms processing from a test harness at the place where the Relay will stand. The harness proves the contract; it does **not** count as the production Relay integration, and the two states are reported separately. The CIC-Relay stays **read-only** from this repository: what is missing there goes into [`relay-requirements.md`](relay-requirements.md) as an `R#` item with evidence from the Relay source, never into a local imitation of the Relay.
4. **Validation is not authorization.** A Forms-validated object is not permission to execute. Observe, Plan, Decision, Execute and Proof stay separate responsibilities; a difference between intent and state is not a command.
5. **The Forms engine does not move into the WASM guest.** The guest receives an **expanded, canonically serialized CIC object** and does only the decoding and the provider-specific mapping that the contract allows.

## 2. How this relates to `primitives-alignment.md`

That document recommends *API-first, extraction-driven, project onto primitives later, do not build from primitives now*. That stays right for what it covers, and it is **kept**:

* **Kept.** The provider contract (what an OCI resource is, which fields are mutable, create-only, computed; the operations; the Work Request and ETag behaviour) comes from the **extractor over the pinned OCI SDK** (`tools/oci-extract`, `oci-sdk.lock.yaml`), not from hand-modelling. The idea of a *mechanical projector* from the extracted contract into the CIC shape (`config_surface` from the mutable and create-only fields, `state_surface` from the observed and computed fields, `operation_surface` from the operations map) is kept as the way to produce the provider-side half of the mapping.
* **Changed since it was written.** (a) A Forms engine exists, in Go and Rust, with parse, schema compile, presence validation, expansion, defaults with origin, normalization and policy resolution (`cic-primitive-forms`, `docs/P1-SUPPORTED-SUBSET.md`); the statement that Phase 6 is a concept for every sub-phase is out of date for the Forms side. (b) The Forms corpus already pins a provider-neutral **NetworkSpace** schema from `cic-schema-registry` (`registry-network-space`), "a real schema whose `config_surface` compiles, so the OCI VCN intent can be validated against the registry's own definition". (c) The two statements "no code-level connection" and "no reference in the Relay source" were true on the date they were written and are **not re-verified here**; the first Factory job re-measures them.

So the direction is **both**: the provider contract stays extractor-derived and API-first, and the CIC object language is attached to it through an explicit, tested mapping, rather than either replacing the other. A banner at the top of `primitives-alignment.md` points here; the rest of that document is left as written.

## 3. The data path (target; `concept` on the module side)

```
user YAML intent
   -> Relay (host): input preprocessing, object decomposition, module routing        [Relay: not this repo, read-only]
   -> Forms: pinned schema binding, shape and presence validation, primitive expansion,
             defaults with origin, policy resolution, normalization
   -> expanded CIC object: versioned schema contract, canonical payload, schema and object digests
   -> Oracle WASM module: decode, provider-specific mapping, OCI request or execution plan
   -> Relay execution boundary: authorization, plan verification, scoped capability, signing, egress
   -> OCI REST API
   -> GET / Observe
   -> Oracle WASM module: interpret the OCI response, normalize, build the CIC state object
   -> Relay: provider output schema check + correspondence + evidence;   Forms state validation: NOT implemented (section 4)
```

The payload is of variable length, identified by its schema and canonically serialized; the Relay-WASM ABI stays stable. The starting point for the envelope is the existing `schemaPayload` of [`specs/provider-abi.md`](specs/provider-abi.md) (`schema_id`, `schema_version`, `schema_hash`, `encoding`, `data`); further integrity and provenance fields are placed **in agreement with the existing CIC contracts and are not duplicated**, and the payload hash is checked **over the actual canonical bytes**.

### 3.1 The two schema roles, kept apart

| Role | What it is | Where it comes from | Status today |
|---|---|---|---|
| **Intent (config) schema** | the provider-independent CIC object the user declares, for example the NetworkSpace | `cic-schema-registry`, compiled by Forms against pinned primitives | exists in the registry and in the Forms corpus; its use by this module: `concept` |
| **Provider contract** | what OCI actually accepts and returns: operations, field policy (`x-cic-policy`), computed fields, Work Requests, ETags | the extractor over the pinned OCI SDK: `module/schemas/core/{vcn,subnet,instance}.json` | `implemented` (the extractor and the three contracts; SDK-pinned) |
| **The mapping between them** | intent to OCI request, OCI response to CIC state, with coverage | this repository, as an explicit contract | `concept`: to be written and tested |

`module/schemas/core/vcn.json` is an OCI-derived JSON schema, **not** a primitives schema. Which schema is which, and where the mapping lives, is the first thing the baseline job (section 6) states; it does not invent a schema.

### 3.2 What the module-specific contract has to say

For every supported resource, **explicitly**: the supported object types and the supported read and write operations; the **fields and operations that are not implemented**; provider-computed values; create-only, mutable, action-managed and input-only fields; the input-to-provider and provider-to-state mapping; the treatment of **absent, explicit `null` and defaulted** values and the preservation of origin; the identity of the logical CIC object against the OCI resource ID; data that cannot be read back or only partly; and the conditions of normalization and semantic correspondence. **A capability that is not implemented is a contractual fact, not a silent omission**: the module must not drop an unsupported field, treat it as a default, or report it as a successful observation. A missing read-back capability is not a `null`. Provider normalization must not reconstruct the authored intent by guesswork.

## 4. The limits that are stated, not assumed

* **Forms validates the config surface, not the state surface.** `docs/P1-SUPPORTED-SUBSET.md` of the Forms repository puts `state_surface` out of scope on purpose: a missing observation there is a coverage fact, not an authoring-validation error, and the library does not decide it. So the step "Forms output validation" of the target path **cannot be assumed to exist**. In the first slice the output is checked by the module's own output schema and the existing `effective_config` correspondence, and the Forms-side state validation is recorded as an **explicit coverage gap** unless the baseline finds otherwise.
* **The Forms admission contract is a design, not a gate.** The P5 admission contract (`cic-primitive-forms`, `docs/P5-ADMISSION-CONTRACT.md`) is documented; the production raw-YAML admission gate, the decisions on the undetermined constructions and the receipt API are **not implemented**. The slice must not claim the cryptographic input admission. Its inputs are **fixtures chosen inside the documented subset**, and this is a stated line of the claim-evidence record.
* **No live OCI.** No create, update or delete against OCI, no IAM change, no Vault key, no credential export, no direct WASM egress, no execute after a validation, no SDK update. A live check is a separate step approved by the orchestrator; where a test would need the cloud, a mocked reproduction and an exact live recipe are written, not run. `manual_real_oci_test.go` (opt-in, [`manual-verification.md`](manual-verification.md)) is unchanged.
* **A plan is not an authorization**: what the module produces is a plan.

## 5. Quality-assurance levels: what is carried over from Forms, measured against this module

`cic-primitive-forms` holds its code to a set of levels that were built to show that the checks can fail, not only that they pass. The module has a different, lighter set today. The table says what each level is, what the module has **as measured on 2026-11-07**, and what it is to become. A level is `implemented` for the module only when it runs in the module's CI on the module's code and has shown that it can fail.

| # | Level | In Forms (sources: `docs/QUALITY-SHARDING.md`, `DELTA-DIFF-GATE.md`, `FAST-FULL.md`, `TEST-EVIDENCE.md`) | In this module today | To be carried over, and how |
|---|---|---|---|---|
| Q0 | Format, static analysis, vulnerability scan | gofmt and the language linters on every change | **implemented**: `golang.quality` runs gofmt, staticcheck, ineffassign, `go vet` and `govulncheck` in CI | nothing new |
| Q1 | Unit tests (and the race detector) | all tests on every change | **implemented, without the race detector**: CI runs `wasm.test` (`go test -v .` in `module/`: no race detector, no coverage) and `oci.extract.test` (`go vet` and `go test` in `tools/oci-extract`). `golang.test` (with the race detector) and `golang.coverage*` are `make` targets and **not CI steps** | the new code gets its tests with the code; whether the race detector joins the CI is a Stage 2 decision |
| Q2 | **Line coverage** | strictly above 90% on the product code (exactly 90% is red), both languages, a gate (`tools/quality_gate.py`) | **measured, not gated in CI**: `go test -cover` gives **82.7%** for package `module` and **88.7%** for `tools/oci-extract` (its two `cmd/` packages 0.0%: no tests). `make golang.coverage-threshold` exists with `COVERAGE_MIN=85` but is **not a CI step** | the new integration code at strictly above 90% (diff-scoped first); a whole-package ratchet only after the measurement. The denominator (which files, which exclusions) is stated with every number |
| Q3 | **Branch coverage** | strictly above 90% with its own tool (`cic-branch/1`), including the places the tool cannot see, named | **absent** | first establish whether a Go branch-coverage tool exists that can be reused or built (the baseline job); until then branch coverage is not claimed |
| Q4 | **Mutation score**, in two parts that are kept apart | (a) the **diff gate**: the mutants in the lines a pull request touches: **all of them must be killed when there are fewer than 10**, and more than 90% when there are 10 or more; (b) the **nightly full run** on `main` measures the whole repository's mutation score; it is a separate piece of evidence, not a substitute for (a) | **absent** | (a) the same rule on the lines of the integration code, with a Go mutation tool; (b) a whole-code mutation score only as a later, separately measured and separately reported figure (Stage 2). Survived, timeout and equivalent mutants are reported in separate categories, and **every survivor is explained one by one whatever the percentage**: a small diff with a good percentage must not hide a surviving, meaningful mutant |
| Q5 | **The gate can fail** (gate selftest, seeded defects) | `quality-gate-selftest` and `seeded-defects`: deliberately weakened tests must turn the gate red | **absent** | every new gate comes with a proof that a planted weakness turns it red, and the result is recorded; a gate that cannot fail proves nothing |
| Q6 | **Shared vectors and construction** | a corpus of cases shared by both languages with expected answers fixed by construction, independent of the implementations; a differential of Rust, Go and an oracle | **partial**: fixtures and the real-OCI harness exist; no vector set with expected answers fixed outside the module | an input-to-request and response-to-state vector set for the supported resources, with expectations written from the contract, **not computed by the module**, plus the negative cases of the first jobs (wrong `schema_hash`, unknown field, wrong primitive form, missing mandatory, forbidden `null`, unsupported OCI field, malformed or wrongly normalized response, missing OCID binding, stale ETag, object-hash mismatch, unsupported schema or grammar version, input/output contract mismatch, unimplemented GET coverage, state returned but not verified); each important negative case also shows that the wrong behaviour really fails the test |
| Q7 | **Claim, evidence, and the evidence rule** | every claim has a status, evidence and a verification method; a record is **evidence** only for a clean tree, a green comparison **and** a selftest that ran and passed, bound to the commit | **partial**: `manual-verification.md` records real-OCI coverage by hand | `claim-evidence` per job (Claim, Status, Evidence, Method, Risk); the same evidence rule; the commit and the CI head SHA must be the same |
| Q8 | **Reachability** | "the symbol exists and the test passes" is not "implemented": a call path (file:line) or a `deadcode` output is required | **not required today** | required for every Forms function the slice claims to use |
| Q9 | **Determinism and no regression** | repeated runs give the same bytes; the corpus and the differential must not get worse | partly (manifest, WASM integrity, a non-fatal reproducibility probe) | repeated-run checks on the new vectors; the existing unit, WASM, corpus and OCI provider tests must not get worse |

**Round trip.** The encode and decode path is checked for the properties the **contract** promises, taking provider defaults, output-only, create-only and input-only fields, action-managed fields, OCID resolution, normalized formats and partial observation into account. No byte-level symmetry is demanded that the provider contract does not promise.

### 5.1 How the levels are introduced (staged)

1. **Stage 0, measure.** The baseline job records the module's numbers by the method above (and the Forms numbers it will compare with), with denominators and exclusions. **No threshold of the existing CI is changed.**
2. **Stage 1, the new code meets the Forms levels.** Q2 to Q9 apply to the **new and changed integration code** of each job, diff-scoped. The existing code is not retro-fitted in the same change.
3. **Stage 2, ratchet.** Only after Stage 1 has run on several changes: the whole-package thresholds may be raised (for example by making `golang.coverage-threshold` a CI step at a measured value). That is a separate decision and a separate change. **A percentage is never claimed that the tool or the measurement does not support.**

### 5.2 What has to be decided before the tooling is reused

* **Licence.** This repository is CC BY-NC-SA 4.0 (`LICENSE.md`). The Forms repository (`cic-primitive-forms`) is **Apache-2.0 throughout**: measured at commit `2f02a75` (2026-11-07), `LICENSE` is the Apache License 2.0 text and every one of its 69 `SPDX-License-Identifier` headers is `Apache-2.0`; no AGPL or MIT file is present. *Correction (2026-11-07):* the first version of this document described the Forms repository as mixed (CC BY-NC-SA 4.0 content, AGPL-3.0-or-later tooling, two MIT hooks). That is the licence structure of the **`cic-factory` repository** (the factory that runs the jobs), not of Forms; the two were conflated. The `cic-schema-registry` schemas are CC BY-NC-SA 4.0 and the `cic-primitives` bytes are bound by the licence listed in the pin's own manifest. Using Forms from this repository is a separate question from copying it: depending on Forms by reference, or fetching a pinned commit in CI, leaves the Forms files under Apache-2.0 and does not copy them here. Copying Forms tooling (the diff gate, the mutation runner, the quality gate) into this repository is **not** assumed to be permitted: the Apache-2.0 conditions (keep the licence text and any NOTICE, mark changes, keep the notices) apply per file, and whether Apache-2.0 files may be placed in a CC BY-NC-SA 4.0 tree is a licence decision for the owner, taken separately from this direction. The baseline job states the licences and proposes reuse by reference or a pinned fetch in CI, or that separate owner decision.
* **CI is the module agent's.** The module's CI and its rules are those of the `cic-module-wasm-claude` process (see `CLAUDE.md`); a change to them is a separate, reviewed change and is not made by this direction.
* **Where the Forms code comes from.** A pinned Forms commit and the exact dependency mechanism (a Go module version, not a copy), with the Go parser fix that makes `Parse` linear on a long line (`cic-primitive-forms` #163) included.

## 6. The jobs that follow (proposal, nothing started)

The work is split so that an orchestrator review stands between the parts; an unattended agent cannot pause for it inside one job.

| Job | Content | Output | Code? |
|---|---|---|---|
| **01a baseline** | the actual state of the Oracle module (the eight operations, the supported resources with their tests, the contracts), of the Forms API (the real signatures: `Parse`, `CompileSchema`, `Validate`, `Expand`, `Normalize`, `NormalizeWithPolicy`, `PolicyResolution`, the canonical path, origin and schema-binding types), of the Relay boundary (the `Call` ABI, the registry, the capability manifest, `cic-flow.sign` and `cic-flow.actuate`); the schema availability and the roles of section 3.1; the Q0 to Q9 baseline of section 5; the licence question | `baseline.md`, `contract-mapping.md` (coverage), `relay-requirements.md` (requirement ID, file, current behaviour, required behaviour, negative test, acceptance condition), `claim-evidence.md` | **no** |
| **01b VCN slice** | short VCN intent, Forms validation, expansion, canonical expanded object, the module's provider input, the OCI request mapping, a mocked OCI response, normalization, the CIC state object, output check, semantic correspondence | the above plus `test-evidence.md` (tests, mutation, negative controls, CI) | yes, on a feature branch |
| **01c Subnet and Instance** | the same mechanism on the existing Subnet contract, then Instance (different lifecycle, actions and the asynchronous Work Request) | the same, plus `next-slice.md` | yes |

The definition of done of a job names the commit and the CI head SHA, and a failed point is a PARTIAL or HOLD status, never a full GO. A coverage record is written for every resource whose two-way mapping is incomplete; it is not hidden by a mock or a hand-made adapter.

## 7. Not claimed

That any part of the integration exists on the module side; that Forms validates state; that the admission contract is a gate; that the harness is the Relay; that the module meets 90% line, branch or mutation levels (the measured line coverage is 82.7% and 88.7%, branch and mutation are not measured); that the Forms tooling may be copied here; anything about live OCI. The statements about `cic-primitive-forms` come from its repository on 2026-11-07 and the Forms documents named above.
