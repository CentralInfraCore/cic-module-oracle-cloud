# Spec — provider ABI

**Piece:** P0.1, P0.3 · **Status:** spec

The contract every provider module implements and every host drives. Fixed and
provider-neutral: new resources change payload schemas, never this ABI.

## Interface

```wit
// cic:provider@0.1.0
interface provider {
    describe: func() -> module-manifest;
    validate: func(request: validation-request) -> result<validation-result, provider-error>;
    observe:  func(request: observe-request)    -> result<observation, provider-error>;
    plan:     func(request: plan-request)       -> result<execution-plan, provider-error>;
    execute:  func(request: execute-request)    -> result<execution-result, provider-error>;
    poll:     func(request: poll-request)        -> result<execution-result, provider-error>;
    invoke:   func(request: operation-request)  -> result<operation-result, provider-error>;
    destroy:  func(request: destroy-request)    -> result<execution-result, provider-error>;
}
```

### Operation meanings

Host boundary: `none` = pure (no host call) · `sign+send` = a signed,
egress-policed HTTP actuation through the trust-flow. OCI signs **every** request
(reads included), so any op that touches OCI is `sign+send`.

| Op | Meaning | Host boundary |
|----|---------|---------------|
| `describe` | Module manifest: identity, supported resource kinds, required capabilities. | none |
| `validate` | Is this intent well-formed and admissible against the schema? | none |
| `observe` | Read current provider state; return observed state + `effective_config`. | sign+send |
| `plan` | Given desired + observed, produce an ordered, signable execution plan. No mutation. | none |
| `execute` | Carry out an **exact, approved** plan. Returns a receipt or async job ref. | sign+send |
| `poll` | Advance/observe an async job (OCI Work Request) toward a terminal state. | sign+send |
| `invoke` | A named non-CRUD action (`start`, `stop`, `attach`, `changeCompartment`). | sign+send |
| `destroy` | Tear down a resource. | sign+send |

Splitting `plan` from `execute` is deliberate: a plan can be reviewed and signed
before any mutation; `execute` runs the signed plan verbatim.

## Envelope

Resource data does **not** change the ABI. It crosses the boundary as a
schema-tagged, hashed payload:

```wit
record schema-payload {
    schema-id:      string,            // e.g. "cic:network:vcn-config"
    schema-version: string,            // e.g. "v0.1.0"
    schema-hash:    list<u8>,          // sha256 of the schema the payload validates against
    encoding:       payload-encoding,  // canonical-json | cbor
    data:           list<u8>,          // the instance, deterministically encoded
}
```

Both host and module validate the payload against `schema-hash` before use; a
mismatch is a `provider-error` of class `schema`.

Rationale (hybrid model): a fully typed WIT per resource forces a new component
build on every schema change; a fully opaque `list<u8>` gives the runtime no
safety. The fixed envelope + schema-tagged payload keeps the ABI stable while
payloads stay validated and versioned.

## Request/response shapes (informative)

```yaml
observe-request:
  kind: cic:network:vcn
  identity: { uid: cic-vcn-prod }
  binding:  { provider: oracle-cloud, region: eu-frankfurt-1, compartment_id: ocid1.compartment... }

observation:
  effective_config: { schema-payload }   # normalized, comparable to intent
  state:            { schema-payload }   # lifecycle, ids, provider-computed
  provider_metadata:
    resource_id: ocid1.vcn...
    revision: { type: etag, value: ... }  # first-class, for optimistic concurrency
    observed_at: 2026-07-18T...

execution-plan:
  plan_id: sha256:...                    # the plan is hashable and signable
  operation: create | update | replace | action | noop
  provider_operations:
    - { service: core, client: VirtualNetworkClient, operation: CreateVcn, request_model: CreateVcnDetails }
  preconditions:  [ ... ]
  expected_postconditions: [ ... ]
  execution_policy:                      # explicit, not implicit SDK config
    timeout: 60s
    max_attempts: 4
    retryable_status: [429, 500, 502, 503, 504]
    idempotency_token: required

execution-result:
  status: accepted | succeeded | failed
  job: { provider_id: ocid1.workrequest..., state: ACCEPTED, poll_after: 5s }  # if async
  provider_metadata: { opc_request_id, etag, resource_id }
  evidence:
    plan_hash: sha256:...
    request_body_hash: sha256:...
    response_body_hash: sha256:...
    module_hash: sha256:...
    schema_hash: sha256:...
```

## Error model

```yaml
provider-error:
  class: schema | validation | conflict | not-found | permission | transport | provider | internal
  provider_code: "IncorrectState"       # provider-native code, verbatim
  retryable: false
  request_id: ...
  field_path: config_surface.cidr_blocks
  message: ...
```

`class` is CIC-canonical (drives host behaviour); `provider_code` is preserved
for evidence.

## Concurrency

`revision` (the OCI `ETag`) is first-class. `observe` returns it; `plan` targets
it; `execute`/`destroy` pass it as `If-Match`. A resource changed since the
observed revision yields `409/412` → error class `conflict`, never a silent
overwrite.

## Payload conventions (pin before first release)

- **Tri-state edits.** `absent` (don't touch) ≠ `null` (provider null) ≠
  `delete` (remove the value). Required for PATCH semantics.
- **Collection semantics.** Each list declares `{ identity: set|list, order,
  update: replace|add-remove, key }` — OCI list updates are often full replace,
  which must not silently drop unmanaged members.
- **No `type: any` / `TBD`.** Every field is concretely typed before the payload
  schema is an ABI-bearing contract.

## `abi.schema.yaml` extension (P0.3) · done

`abi.schema.yaml` now has an optional `imports` surface and `project.yaml`
declares `abi.imports` (`cic-flow`: `sign`/`actuate`, provisional). `describe()`
reports it, so the host can read the module's required sign+send surface. It is a
**declaration** — the names are settled with the relay when the host surface
lands (R1/R2), and nothing is imported at the wasm level yet (WASI-off; the
trust-flow is native-FFI today), so it is not checked against the binary the way
`exports` is.

```yaml
abi:
  exports: [allocate, deallocate, Call]
  imports:
    # Illustrative names. The relay does not expose these yet — its trust-flow
    # is native-FFI only and Bearer-based. The concrete host surface (a
    # sign-then-send capability for OCI) is a relay requirement, not a given.
    - module: cic-flow          # exact module/function names TBD with the relay
      functions: [sign, actuate]
```

The exact import names are settled with the relay when the host surface lands —
see [relay-requirements.md](../relay-requirements.md) (R1, R2). This module declares
its imports; it does not define the host side.

See [host-functions.md](host-functions.md) for the host side and
[capability-manifest.md](capability-manifest.md) for the enforced scope.
