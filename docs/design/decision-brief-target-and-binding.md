# Decision brief: target data and execution binding

Status: **a brief for decisions.** It decides nothing. Every "recommendation" is the author's; the
decision is the owner's, and where a decision touches another repository's contract it is joint (see the
codes below). It gathers what the documents of this series left open, in one place, in the order in which
the decisions depend on each other.

Documents it draws on: `specs/plan-digest.md` (M4a, implemented), `specs/approval-and-execution-state.md`
and `relay-requirements.md` R6 to R10 (M4b, concept), `specs/destroy-invoke-binding.md`,
`specs/target-origin-and-path-safety.md` (D5), `specs/realization-context.md` (D5-2, with the Forms side in
its section 6).

## 0. How to read it

Each decision carries one or more codes for how it can be settled:

- **[S]** decidable from the sources: the answer is a fact that can be read;
- **[M]** needs a measurement that has not been made;
- **[O]** the owner's choice, there is no fact to read;
- **[J]** joint: it changes the contract of another repository (Forms library, engine, Relay) and cannot be
  decided in this repository alone.

The three layers depend on each other in one direction: **architecture decisions constrain contract
decisions, and contract decisions constrain the order of implementation.** The layers are kept apart so
that a module-level step is not read as an answer to an architecture question.

## 1. Where things stand

| Work | State |
|---|---|
| M1 (resource id syntax), M3 (registry-conformant execute), M5 (poll target), exact JSON equality, M4a (plan, target and execution digests, derive-again gate) | **implemented**, merged, CI green |
| M4b (approval, authority, single use, evidence), R6 to R10 | **concept**: requirements toward the Relay, not raised in CIC-Relay, nothing implemented there |
| `destroy` and `invoke` binding | **measured** (they bypass the `execute` gate), contract **proposed** |
| Target data origin (D5) and the realization context (D5-2) | **measured / surveyed**, contract **proposed** (concept) |

The M4a gate proves a plan and a target are consistent with each other. It does not prove approval,
authority, single use, or where the target came from.

## 2. Layer A: architecture decisions

These decide what the realization context is and where it lives. Nothing below them can be settled until
A1 is.

| ID | Decision | Code | Recommendation |
|---|---|---|---|
| **A1** (D5-6) | The realization context **inside** the materialized object, or **outside** it, bound at the execution layer | **[J][O]** | Work on (ii), outside, as an assumption to be confirmed, because it keeps the object provider-independent and the receipt contract out of it. (i) is not excluded: it means reconsidering the object's schema and boundary, and whether the existing receipt can represent the derivation without an extension is **not verified**. Not a choice for this repository alone: it touches the object boundary and the Forms library and engine contracts |
| A2 (D5-2 Q1) | Where a realization context is authored and stored: deployment repository, Relay configuration, a schema kind in the registry, elsewhere | [O][J] | none: the sources are silent |
| A3 (Q2) | Granularity: one context per environment shared by many objects (object binding kept in the state), or one per object | [O] | per environment with the object binding in the state, since the state model already separates the logical identity (`uid`) from the provider identity (`provider_id`) |
| A4 (Q3) | Who issues and signs a context, and what authenticates it | [O][J] | the layer that realizes the object issues it; its authenticator reaches the module through the Relay's authenticated execution context (R6) |
| A5 (Q5) | Routing and decomposition: how one intent spanning providers or regions becomes several realizations | [O][J] | defer: the data path calls it concept, and no first slice needs it |
| A6 (Q6) | Lifecycle: rotation and revocation of the execution identity binding; an approved but unexecuted plan whose context changed | [O] | an approval names `execution_digest`, which commits to the target, so a changed context yields a different digest and the old approval does not match (R7); rotation and revocation policy is still the owner's |
| A7 (D5-7) | A distinct name for the realization environment, so it is not confused with the engine's "environment" (the processing layer) | [O] | decide first: it is cheap and every later document uses the word |

## 3. Layer B: contract decisions, by boundary

### 3.1 Provider module

| ID | Decision | Code | Recommendation / what settles it |
|---|---|---|---|
| D-DI-1 | `invoke` runs only `action`-role operations | [O] | yes: ends the measured bypass (an `invoke` of a delete, update, create or read operation) |
| D-DI-2 | A field outside the operation's declared set: refuse or drop silently | [O] | refuse |
| D-DI-3 | The number form of the body that is sent (canonical `<digits>e<exp>`, or a plain decimal when lossless and bounded) | **[M]** | cannot be decided without measuring what OCI accepts for an integer field; the canonical-JSON round trip alone is not enough, the request must also be valid under the target OCI request schema (review of #46, F2) |
| D-DI-4 | One field name `execution_digest` for `execute`, `destroy` and `invoke`, told apart by the digest's tag | [O] | yes |
| D-DI-5 | Keep the old, digest-less `destroy` and `invoke` request | [O] | no: no consumer was found in the repositories cloned here; repositories not cloned here were not checked |
| **Source of an action's fields** (review of #46, F1) | Where the declared fields of an `invoke` come from | **[S]** | **the registry already carries it**: each of the four `action`-role operations (`AddVcnCidr`, `ChangeVcnCompartment`, `ChangeSubnetCompartment`, `ChangeInstanceCompartment`) has an `input` schema with declared `properties` and `additionalProperties: false` (read from `module/schemas/core/*.json`); the other roles have none. The module's `resourceContract` does not load it today (it keeps `method`, `path`, `role`). So the source is the operation's own `input` schema, not the config's `action-managed` fields. Whether every future action operation will carry one was not checked |
| D5-1 | Record the service API version per service in the registry, so `base_path` is derived and no longer given | [S][M] | needs the extractor and the SDK read (how the Go SDK exposes a service's API version is **not verified**); it is independent of A1 because the provider contract lives in the registry under both alternatives |
| D5-4 | Structural checks of `host` and `base_path` before `sign` (one path builder, no URL syntax, no control characters): refuse or normalize | [O] | refuse; it is defense in depth at the single place that builds the path (four entry points share it), not the architecture's starting point |
| D5-5 | The `poll` alias with an empty `base_path` (it takes the API version from the caller today) | [O] | once D5-1 lands the version comes from the registry; until then leave as it is |
| D-M5-3 | A retirement date for the `poll` `path` alias | [O] | none: it was left to a separate compatibility decision |

### 3.2 Relay (read-only from here)

| ID | Decision | Code | Note |
|---|---|---|---|
| R-raise | Raise R6 to R10 as issues in CIC-Relay (the file says "not yet raised") | [O] | the Relay team owns the design; R6's content (what the authenticated execution context carries) depends on **A1** and **A4** |
| R-host | The host-boundary property of R7 (the approval binds the actual `sign` and `actuate` calls) | [J] | stated as a required property, not a design |

### 3.3 Forms library and engine

Only if **A1** is (i): reconsidering the object's schema and boundary, and checking whether the existing
closed receipt can represent the derivation of context-derived values without an extension. If A1 is (ii)
these two have no change.

## 4. Layer C: order of implementation

Steps 1 to 3 are module-only and do **not** depend on the architecture layer. Step 4 does. None of 1 to 3
is the realization proof, and none of them should be reported as one.

| Step | What | Needs |
|---|---|---|
| 1 | Path-building hardening: one builder, structural checks, nothing that is not a path segment reaches the string that is signed, checked before `sign` | D5-4 |
| 2 | The API version in the registry, so `base_path` is derived | D5-1 (an SDK read) |
| 3 | The `destroy` and `invoke` execution digests, with the action input from the registry and `invoke` limited to `action` operations | D-DI-1, 2, 4, 5 and the registry `input` source; **D-DI-3 needs a measurement first** |
| 4 | The realization context: its record, its digest, its delivery to an execution, the derivation of the target | **A1 to A4** and, depending on A1, the Forms and engine contracts, the Relay's R6 and R7 |
| 5 | M4b on the Relay side | the Relay team, after R-raise |

## 5. Measurements that are missing

| Measurement | Needed for |
|---|---|
| What OCI accepts as the number form of an integer field in a request body | D-DI-3 |
| The request-target that actually goes out over the network (the real Relay, and the Go HTTP client's output for a URL with a query or a fragment), not the mock's URL | step 1; the review of #47 asks for this evidence before an implementation review |
| What OCI or a proxy does with dot segments, an encoded dot segment and a double slash | step 1 |
| Whether the OCI operations used accept an idempotency token the `execution_digest` could be derived into | the UNKNOWN state of M4b |
| The fields of a real Work Request response (the resources it names), and the type segment of its OCID | correlating a Work Request with an execution |
| A run of the rewritten manual harness against real OCI | the harness has only been compiled |

## 6. Still on hold, unchanged

The `module.wasm` rebuild, signature and release; the production integration; the asynchronous create
cases B and C; phase 3 of the CI-integrity issue (#37). This brief does not change any of them.

## What this brief does not do

It does not decide anything, change a contract, or claim that any step is implemented. The
recommendations are one author's reading of the documents of this series. Decisions marked [J] cannot be
closed by this repository: they need the owners of the Forms library, the engine and the Relay.
