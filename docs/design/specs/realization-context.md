# Realization context (D5-2): what exists, where the chain breaks, and a contract proposal

Status: sections 1 to 3 are a **survey** of what is written and what is in code, every item marked
`implemented`, `scaffold`, `concept` or `not read`. Section 4 is a **proposal (concept)**: nothing in it
is implemented in the module or the Relay.

The question: how does a general, provider-independent intent become a realization context bound
to a provider, an environment and a CIC object, so that the `host`, `base_path`, `key_id`,
`resource_id` and `revision` of an execution are **derived and provable**, not supplied (D5,
`target-origin-and-path-safety.md`, section 3)? Without that, an exactly extracted API version would
only be one more well-formed parameter.

The premise is the project owner's statement of the CIC model: the target data belong to the
realization context, the provider contract and the observed state. This survey tests it against what
is written down and what is in the code; where the sources are silent it says so.

## 1. Method and sources

- The `cic-graph` knowledge base was consulted for orientation only. It is a snapshot (built
  2026-10-16, one source dirty at build, some sources behind upstream); **every claim below was
  re-read from a live file** and is cited by file.
- Read live: this repository's `docs/design/` (`state-model.md`, `forms-integration-direction.md`,
  `architecture.md`, `relay-requirements.md`, the D5 and M4 documents); CIC-Relay at `55a899f`
  (`features/feature-011-oci-provider/design.md`, `core/nexus/iac`, `core/nexus/drift`,
  `core/nexus/ois`, `core/cabinet`, `cmd/relay/cic_flow_*`).
- **Not read in this pass:** the Forms engine and its docs (`cic-primitive-forms`,
  `cic-primitive-engine`), the schema registry (`cic-schema-registry`), `primitives`,
  `CIC-basic-knowledge`, and the OIS repository. Statements about them are second-hand and marked.

## 2. What exists

| Element | Where | State | What it carries |
|---|---|---|---|
| Intent and state are two representations of one logical object; **correlation by the stable CIC `uid`, never by `provider_id`**; correspondence checked object-level | `docs/design/specs/state-model.md` (status: spec), Relay `features/feature-011-oci-provider/design.md` | spec, `concept` as a whole; `observe` (implemented) returns the applied-config projection and the provider metadata | `metadata.uid`; the state object's `binding: {provider, provider_id}`, `revision` (etag), `provenance: {intent_commit, observer_module_hash, observed_at}` |
| Logical references resolved against the provider (`route_table: production-private` to a `route_table_id`) | the same two files | `concept` | a correspondence rule per resource kind |
| Host-side comparison and object-level conformance (intent against state, with an observation-coverage envelope) | Relay `core/nexus/iac` (`compare.go`, `conformance.go`, `observation.go`) | `implemented` as Go code with tests; imported (non-test) by `core/nexus/drift`, which uses it for a scalar drift check; the IaC loader of `cmd/relay/main.go` uses the same package. **Not** imported by the cabinet or the `cic-flow` execution path | comparison and verdicts; `provider_id` is treated as a state-only field |
| The data path: user intent, Relay decomposition and routing, Forms (pinned schema binding, validation, expansion, defaults with origin, policy resolution, normalization), expanded object with digests, module, Relay execution boundary, provider, observe | `docs/design/forms-integration-direction.md`, section 3 | `concept` on the module side (the document says so); the Forms engine exists in Go and Rust (second-hand: **not read**) | the schema and object digests of the expanded object |
| "The module does not attest its own work and holds no custody"; **plan, then execute the exact signed plan; observe and verify are host-side** | Relay `features/feature-011-oci-provider/design.md` ("Boundary") | recorded design decision; the `cic-flow` host module (`sign`, `actuate`) is `implemented`; the approval and signed-plan step is `concept` (`relay-requirements.md` R7) | the host seals evidence, grants scoped capabilities |
| Intent authorization seam (OIS gate: `Intent{Actor, Action, TargetRef, Reason}`, `Policy.Authorize`) | Relay `core/nexus/ois`, `features/feature-010/ois-gate.md` | `implemented` as a seam; its non-test importers are the rollback recorder and the demo, not the module's execution path | an authorization decision on a declared intent |
| Signing identity and reach | the module's `project.yaml` `capability_manifest` (egress `*.oraclecloud.com`, secret handle `oci/*`), Relay `cmd/relay/cic_flow_*` | `implemented` | the module **declares** its own reach in its signed manifest; the key stays in the Relay's Vault signer |
| State commit model: a state is committed only with verifiable provenance, confirmed physical realization (PoSE) and no unresolved drift | `CIC-basic-knowledge` `docs/en/reality/state_commit_model.md` | second-hand from the knowledge base, **not read live** | the conditions for a final state |

## 3. Where the chain breaks

```
concept -> code -> runtime -> audit
```

| Link | State | Evidence |
|---|---|---|
| intent to the expanded object (Forms) | code exists elsewhere, **not read** here | `forms-integration-direction.md` section 3 |
| **realization context: which provider account, environment, contract version and execution identity this object is realized in** | **does not exist** | no document in this repository defines it; no Go type for a realization, deployment context, environment or binding in the Relay's `cmd`, `core`, `pkg` at `55a899f` (searched by type name); in the spec's example the state object's `binding` has only `provider` and `provider_id` |
| derivation of `host`, `base_path`, `key_id` from it | does not exist | measured: all five target values are caller-supplied to every entry point (`destroy-invoke-binding.md`, `target-origin-and-path-safety.md`); the registry records no API version, base path or host |
| routing an object to a module and an environment | `concept` | `forms-integration-direction.md` section 3 ("module routing") |
| an authenticated execution context for the guest | `concept` | `relay-requirements.md` R6; the Relay passes only the component id |
| approval and single use | `concept` | R7, R8 |
| execution of an operation | `implemented` and measured against real OCI for several operations; M1 to M4a gates in the module | `manual-verification.md`, the module tests |
| observed state, correlation, correspondence | `observe` `implemented`; `core/nexus/iac` `implemented` but wired only to drift | section 2 |
| evidence | partial: a post-hoc, best-effort trace in the cabinet | `relay-requirements.md` R9 |

**The model exists, but the implementation bridge breaks at the realization context**: between the
expanded intent object and the execution target. Everything on either side has a written form or code;
the object that says *in which provider environment, under which provider contract and with which
execution identity this logical object is realized* has neither, and the values that should come from
it enter the module as free request fields today.

What the existing material already gives, and the proposal below reuses: the logical identity
(`uid`) and its separation from the provider identity (`provider_id`); the state object's
`provenance` and `revision`; the schema digests and the `contract_digest` already inside
`plan_digest`; the plan-then-execute-signed-plan decision; and the host-side correlation and
conformance code.

## 4. Proposal: a realization context (concept)

### 4.1 What it is

A versioned, authenticated record that says, for one logical object (its CIC `uid`) and one provider,
where and under what contract that object is realized. It is **not** part of the domain intent and
**not** a request parameter of a provider call. It is produced by the layer that realizes the object
(not the provider module) and reaches an execution through the authenticated execution context the
Relay provides (R6).

Content (concept; names open):

| Member | Meaning | Becomes |
|---|---|---|
| `object` | the CIC `uid` (and kind) | the correlation key |
| `provider` | which provider and provider module | module routing |
| `environment` | provider account scope and region/realm | `host`, by the provider contract's host rule |
| `provider_contract` | the registry digest the object is realized under | `base_path` (the API version) and the operation set |
| `execution_identity` | a reference to the signing identity (a handle such as `oci/prod`), never key material | `key_id` and the scoped capability |
| `binding` | the provider identity of the current realization, if any (`provider_id`) | `resource_id` |
| `observation` | the observation the revision comes from (source, time, digest) | `revision` |
| `issued_by`, `policy_decision`, `valid` | who derived and issued it, under which decision, for how long | authority and validity |

### 4.2 How the target is derived

| Target field | Derived from | By |
|---|---|---|
| `host` | `environment` and the provider contract's host rule (the rule recorded in the provider contract, not hard-coded in a harness) | the realization layer; the manifest allowlist stays an upper bound |
| `base_path` | `provider_contract` (the API version recorded per service in the registry: D5-1) | the module, from the registry digest |
| `key_id` | `execution_identity` resolved by the Relay | the Relay |
| `resource_id` | `binding.provider_id`, found by `uid` correlation | the state model |
| `revision` | `observation` | the observer |

### 4.3 How it ties to what already exists

`target_digest` becomes a function of the derived target and of a **realization digest** over the
context, with its own domain tag and the same `cic-canonical-json/1` framing; the plan then rests on
a chain the approval transitively commits to through `execution_digest`:

```
intent digest, schema digests -> plan_digest (with contract_digest)
realization digest             -> target_digest
observation digest             -> revision
plan_digest + target_digest    -> execution_digest   (what an approval names)
```

The module, given the context through the authenticated execution context, recomputes the target from
it and compares; the Relay verifies the context's authenticity and policy (R6, R7); the host-side
correlation and conformance code (`core/nexus/iac`) and the observation are the verification after
the execution; the trace records the context and the digests (R9). A signed observation is
evidence of what was read, not proof of the physical state: the state commit model's physical-
realization condition is not claimed to be met by it.

Until such a context exists, the module's `target` input stays what the M4a gate proves consistent
and nothing more: a caller-supplied value.

### 4.4 Relation to OIS

OIS (the intent authorization seam) decides whether a consequence may be applied. The realization
context says which provider environment, contract and identity the consequence applies to. They are
separate: an approval of a consequence is not an identification of its target, and an identified
target is not an approval.

## 5. Open questions (for the owner; the sources cannot decide them)

1. **Where a realization context is authored and stored**: a deployment repository, the Relay's
   configuration, a schema kind in the schema registry, or elsewhere.
2. **Granularity**: one context per environment shared by many objects (with the object binding kept
   in the state), or one per object.
3. **Who issues and signs it**, and what the authenticator is.
4. **The provider contract's record of the host rule and the API version** (D5-1: extend the registry
   and the extractor), and whether the host rule is a pattern or a list.
5. **Routing and decomposition**: how one intent that spans providers or regions becomes several
   realizations (the data path calls this "object decomposition" and "module routing": `concept`).
6. **Lifecycle**: rotation and revocation of the execution identity binding; what happens to an
   approved but unexecuted plan when its context changes.
7. **The Forms side**: where in "defaults with origin" and "policy resolution" a context-derived value
   would enter; this needs the Forms engine and its documents read, which this pass did not do.

## What this does not claim

The realization context is not implemented anywhere and no document in this repository defined it
before this one. Nothing here is verified against the Forms engine, the schema registry or the OIS
repository, which were not read in this pass. The Relay facts are from `55a899f`, read, not run.
Section 4 is a proposal: the premise it rests on is the owner's statement of the model, and the
open questions above are the points where that statement has to be completed before anything is built.
