# Architecture

## Goal

Provision Oracle Cloud Infrastructure (OCI) from CIC, and open a namespace —
`cic-module-<provider>` — in which **third-party** provider modules can be added
safely. `cic-module-oracle-cloud` is the first-party reference implementation.

The third-party goal is the load-bearing requirement. It rules out the simplest
design (a native adapter compiled into the relay) and dictates a sandboxed,
capability-confined, signed-artifact model.

## Two execution models — and why we chose the sandbox

The OCI part could be built two ways:

1. **Native adapter crate.** An `oci-adapter` Rust crate in the relay workspace
   composes the existing `http-executor` and `vault-adapter`. It works today,
   needs no new runtime boundary, and is the pragmatic choice **if every
   provider module is first-party and trusted**.
2. **WASM guest module.** The provider logic runs in a wazero sandbox and
   reaches the network and secrets only through host functions.

Native adapters cannot host untrusted code — you would be compiling a third
party's logic into your relay binary. Since third-party modules are a stated
goal, the sandbox is not an aesthetic choice; it is the only safe one.

What the sandbox buys — none of it about *making OCI work*, all of it about
trust and extensibility:

- **Hard capability confinement.** A guest can do nothing except what its host
  functions allow. It physically cannot open a socket, read a file, or
  exfiltrate a secret. A native crate relies on review and `unsafe_code=forbid`;
  the sandbox is a runtime wall.
- **Independent, signed distribution.** A module is a small, separately built,
  cryptographically signed artifact (`buildHash`, the release pipeline). It
  ships and updates independently of the relay, and the ProofTrace proves which
  exact `module.wasm` ran.
- **Safe third-party code.** Untrusted provider logic runs without being
  compiled into the relay.

## Layers

```
┌─ intent (desired OCI state, CIC canonical) ──────────────────────────┐
│                                                                      │
│  CIC host (relay) — provider-agnostic                                │
│    · loads the signed WASM module, verifies its signature            │
│    · enforces the module's capability manifest                       │
│    · exposes host functions: http.request, vault.sign                │
│    · records every host call into the ProofTrace                     │
│    · runs object-level intent↔state verification                     │
│                                                                      │
│      ▲ host functions (capability boundary)                          │
│      │                                                               │
│  WASM module (this repo, or a third party) — sandboxed               │
│    · provider logic: build request, sign scheme, map response        │
│    · declares its capability needs (egress hosts, secret handles)    │
│    · carries no secret, no raw network access                        │
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
        │ http.request                    │ vault.sign
        ▼                                 ▼
  http-executor (Rust, native)      vault-adapter (Rust, native)
  reqwest + EgressPolicy            Vault transit / countersign
        │                                 (private key stays in Vault)
        ▼
  OCI REST API
```

The host is **provider-agnostic**: it never contains OCI-specific code. Every
provider — Oracle, and later AWS, Azure — is a module behind the same generic
capability boundary. This is the property that keeps the relay from bloating
with each provider's SDK.

## The provider ABI

The module boundary is not `create / update / delete`. A single OCI change can
be a create, an update, an `Add*`/`Remove*` action, a compartment move, or a
multi-step Work Request — but from CIC's side every change is the same shape:

```
desired state → plan → execution → verified observed state
```

So the interface is:

```
validate → observe → plan → execute → poll → invoke → destroy
```

carried over a **fixed** WIT envelope with a schema-validated, hashed payload
(`schema-id`, `schema-version`, `schema-hash`, `encoding`, `data`). New OCI
resources change the payload schemas, never the WASM ABI. Full spec:
[specs/provider-abi.md](specs/provider-abi.md).

## Where the OCI SDK lives — nowhere, at runtime

There is no OCI SDK binary in the running system. The OCI Go SDK is a
**build-time schema source**, decomposed:

| SDK part | Destination |
|---|---|
| Models (`CreateVcnDetails`, `Vcn`) | build-time `go/ast` extraction → generated module types + CIC contracts |
| Operation registry (`CreateVcn` → `POST /vcns`) | build-time extraction → module's operation table |
| HTTP transport | **not the SDK** — the relay's `http-executor` |
| Request signing (RSA over canonical headers) | the module builds the OCI canonical signing string; `vault.sign` signs it; the key stays in Vault |
| Retry / waiter / Work Request | the plan the module emits + host policy |

At runtime the module is self-contained OCI logic (generated models + the OCI
signing scheme); the host provides only generic `sign` + `http`. The build-time
generator is Go (it reads Go AST) but emits only module code and CIC contracts —
nothing Go ships at runtime. Full spec:
[specs/oci-schema-pipeline.md](specs/oci-schema-pipeline.md).

The honest cost: the OCI request-signing scheme (draft-cavage HTTP Signatures,
RSA-SHA256 over canonical headers) is reimplemented in the module, and the
models are generated. The win: the host stays provider-agnostic and the private
key never enters the guest.

## Module language

Because a sandboxed guest reaches `http-executor` / `vault-adapter` through host
functions (JSON over the ABI) rather than by linking, it shares no types with
them — so the guest language is genuinely open. A third party writes theirs in
whatever compiles to WASM and satisfies the ABI; the ABI is language-neutral.

The first-party reference (this repo) is seeded from the base-repo Go/TinyGo
template. Whether it stays Go or moves to Rust is a maintenance choice, not an
architectural one, and does not affect third parties.

## Trust model for third-party modules

Three independent controls, none of which trust the module:

1. **Sandbox** — the module cannot exceed its host functions.
2. **Capability manifest** — the module *declares* the egress hosts and secret
   handles it needs; the host *enforces* that scope. An OCI module that declares
   `*.oraclecloud.com` and `oci/*` can reach nothing else.
   See [specs/capability-manifest.md](specs/capability-manifest.md).
3. **Signature + ProofTrace** — the module artifact is signed for provenance,
   and every host call (request, response, signature) is recorded, so a run is
   provable: this intent, through this module hash, produced this plan, executed
   these recorded calls.

## Verification

Intent and state are not the same document — they are two representations of one
logical object. Verification is object-level correspondence
(`satisfies(stateObject, intentObject)`), correlated by a stable CIC `uid`, not
a field-by-field diff. `effective_config` is a derived, read-only projection of
the state used for that comparison, not a third source of truth. Full spec:
[specs/state-model.md](specs/state-model.md).
