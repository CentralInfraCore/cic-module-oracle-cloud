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
  plan_digest: sha256:...                # identifies what the plan executes (plan-digest.md)
  target_digest: sha256:...              # identifies where it runs
  execution_digest: sha256:...           # binds the two; the value an approval names
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

### What `execute` emits today

The block above is the design intent. The shape that this module's `execute` (and
`destroy`) actually returns is smaller and is kept per operation:

```yaml
execution-result:                 # as implemented
  status: succeeded | accepted | failed
  steps:                          # one entry per executed provider operation, in plan order
    - operation: CreateVcn
      http_status: 200
      etag: "..."                 # only when the provider sent one
      opc_request_id: "..."
      work_request_id: "..."      # only when the provider answered with an opc-work-request-id
      resource_id: ocid1.vcn...   # role=create steps only, see below
      error: "..."                # failed steps only; diagnostic text, not a contract
      error_class: provider       # failed steps only; the CIC-canonical class
      provider_code: "..."        # failed steps only, when the provider sent one
```

- `job`, `provider_metadata` and `evidence` of the design block are **not** emitted
  by `execute` today (status: concept). The per-step fields above carry the request
  id, the etag and the work request id that those blocks describe.
- `accepted` is a run with no failed step but an outstanding Work Request.
  `execute` stops at the first failed step and then reports `failed`.
- **`resource_id`** is the OCID of the resource that a create step produced. It
  appears on `role=create` steps only, so every other step is byte-identical to a
  result that predates the field. It is read only from the create response's own
  `id`, never from `binding.resource_id`. The module accepts it only if the
  response body is exactly one JSON object with a single `id` member whose value
  is a well-formed OCID of the expected type
  (`ocid1.<type>.<realm>[.<region>][.<future use>].<unique id>`, at most 255
  bytes, the segments limited to `[A-Za-z0-9_-]`).
- A 2xx create answer without a usable id (missing, empty, null, not a string,
  repeated, spelled differently, or not a well-formed OCID) is a **failed** step of
  class `provider`, with no `resource_id`, and `execute` never retries a create.
  If the answer was HTTP 202 with a Work Request, the message says that the
  provider accepted the create and that the caller should check the Work Request;
  any other 2xx says that the resource was created but its id is unknown. In both
  cases it tells the caller not to retry, because a second create could duplicate
  the resource. The message text is diagnostic; the contract is the status, the
  `error_class`, the absent `resource_id` and the kept `work_request_id`.
- **Not decided here:** whether a create that is accepted asynchronously and carries
  no id should end as `accepted` with the id still pending, and what `poll` would
  have to return to deliver the created resource's identifier.

### What `plan` requires of its inputs

`desired.data` and `observed.data` must be strict, bounded JSON with exact numbers and
no repeated object key, as defined in `canonical-json.md` (cic-canonical-json/1);
otherwise `plan` answers `validation` with `field_path: desired.data` or
`observed.data`. Values are compared by their canonical form, so key order, whitespace,
escape spelling and number spelling (`1`, `1.0`, `1e0`) do not make a difference, while
different values always do (two integers above 2^53 are never equal).

### What `execute` accepts before it sends anything

Three independent checks run over the **whole** plan before the first step is signed
or sent, so a plan never half-executes. Each refusal is a `validation` error with
no sign and no actuate call.

- **Resource id (M1, `binding.resource_id`).** A step that splices the id into its
  path, or whose operation the registry does not know as a create, requires
  `binding.resource_id` to be a well-formed OCID of the kind's type
  (`field_path: binding.resource_id`). The decision follows the executed path (a
  placeholder in it), not only the role of the operation name. A plan of create
  steps with plain paths does not need the id. `observe`, `invoke` and `destroy`
  apply the same rule. `poll` has its own target rule, below.
- **Registry conformance (M3, `field_path: plan.provider_operations[i]`).** Every
  step must name an operation the kind's registry defines, and its `method` and
  `path` must equal the registry's exactly (case, placeholder name, trailing slash;
  an empty method or path is a mismatch). The plan can name an operation; it cannot
  invent one or override what the registry says it is. A `read`-role operation
  (for example `GetVcn`) is refused as a step even when it matches the registry: a
  read is `observe`'s job and `plan` never emits one. `i` is the first
  non-conforming step.

- **Plan binding (M4a).** `execute` takes the `desired` and `observed` state the plan was
  made from, derives the plan again, and requires the same operation and the same
  provider operations in the same order; the three digests (`plan_digest`,
  `target_digest`, `execution_digest`) to equal the ones computed from the request;
  `config.data` to be the same JSON value as `desired.data`; and no retired `plan_id`.
  The details are in `plan-digest.md`. This shows the request is internally consistent,
  not that it was approved or that it has not run before.

What these checks do **not** say: whether the id names the right resource, whether
the plan was approved, whether the caller may run it, or whether it ran already. Those
are the Relay-authorization layer (status: concept).

### What `poll` accepts

`poll` sends a signed GET, so the caller does not choose where it goes. The module
builds the request path itself; a refusal is a `validation` error with no sign and
no actuate call.

- **Input.** Exactly one of `work_request_id` (the id an async `execute` step
  returns, the primary input) and `path` (a legacy alias). Both, or neither, is
  refused (`field_path: work_request_id`).
- **Path.** `<api version>/workRequests/<id>`, where the api version is `/` and
  8 digits. With `work_request_id` the version comes from `binding.base_path`, which
  must then be exactly such a version (`field_path: binding.base_path`). The alias
  must be exactly `/<api version>/workRequests/<id>`: no query, fragment, escape,
  extra segment, trailing slash or other collection (`field_path: path`). If
  `binding.base_path` is set, the alias version must equal it. Both inputs go through
  one function that checks the id and builds the canonical path, so the alias is not a
  second, looser route.
- **Id.** A well-formed OCID of **any** type. OCI documents several Work Request
  types (for example `workrequest`, `coreservicesworkrequest`,
  `clustersworkrequest`), so there is no allow-list; the type actually returned by
  the services this module drives has not been measured.
- **Method.** Always `GET`, no body.
- **Unchanged.** The response mapping: `work_status`, `percent_complete`,
  `terminal`, `http_status`; an HTTP error maps to a CIC error; an undecodable body
  is an error.

What this does **not** say: that the Work Request belongs to the plan that started it,
that the caller may poll it, or that `binding.host` is the right service host for
that id. A syntactically valid id is not authorization (plan binding and Relay
authorization: status concept; `host` and `base_path` contract: status concept).

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
