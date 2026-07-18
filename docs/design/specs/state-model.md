# Spec — state model

**Piece:** P0.4, P4.1 · **Status:** spec

How intent and observed state relate, and what `effective_config` is for.
Verification is **object-level correspondence**, not a field-by-field diff.

## Intent and state are two representations of one object

They are not the same document. The state object is the intent object's
observed, provider-bound realization — it carries more:

```
IntentObject ──realization──► StateObject
```

Not equality — **correspondence**:

```
satisfies(stateObject, intentObject) -> true | false
```

### Example

```yaml
# intent branch — logical, desired
kind: Subnet
metadata: { uid: cic-subnet-prod }
spec:
  cidr: 10.10.1.0/24
  route_table: production-private
  public_ip_allowed: false
```

```yaml
# state branch — same logical object, provider-bound
kind: Subnet
metadata: { uid: cic-subnet-prod }
binding:
  provider: oracle-cloud
  provider_id: ocid1.subnet.oc1.eu-frankfurt-1...
observed:
  cidr_block: 10.10.1.0/24
  route_table_id: ocid1.routetable...
  prohibit_public_ip_on_vnic: true      # note: inverted sense
  lifecycle_state: AVAILABLE
revision: { type: etag, value: 7f1c9... }
provenance: { intent_commit: abc123, observer_module_hash: sha256:..., observed_at: ... }
```

Line for line these differ. Object-level they represent the same thing —
`metadata.uid` correlates them, and the resource-kind contract defines the field
correspondences (`route_table` ↔ resolved `route_table_id`;
`public_ip_allowed == !prohibit_public_ip_on_vnic`).

## Two separate steps

### 1. Correlation

Find which state object realizes an intent object:

```
correlate(intentObject) -> stateObject     # by stable CIC uid
```

The **CIC `uid`** is the logical identity (survives recreate). The
**`provider_id`** is the identity of the current realization (changes on
recreate: a new subnet OCID for the same `cic-subnet-prod`). Never correlate by
`provider_id`.

### 2. Correspondence

Once paired, evaluate the resource-kind contract object-level:

```yaml
kind: Subnet
correlation:
  intent_identity: { path: metadata.uid }
  state_identity:  { path: metadata.uid }
requirements:
  - { name: cidr-matches, assert: { equals: { intent: spec.cidr, state: observed.cidr_block } } }
  - { name: route-table-resolves,
      assert: { reference_resolves_to: { intent: spec.route_table, state: observed.route_table_id } } }
  - { name: public-ip-policy,
      assert: { expression: "intent.spec.public_ip_allowed == !state.observed.prohibit_public_ip_on_vnic" } }
acceptable_state:
  lifecycle_state: [AVAILABLE]
```

```yaml
# result
verification:
  intent_uid: cic-subnet-prod
  correspondence: one-to-one
  result: satisfied
  checks: { cidr-matches: passed, route-table-resolves: passed, public-ip-policy: passed, lifecycle-state: passed }
```

## `effective_config` — a projection, not a branch

`effective_config` is the read-only, provider-normalized view of what actually
took effect, used to compare against intent. It is **not** a third source of
truth — that would raise "which branch is authoritative? who writes it? when
does it refresh?". It is derived, deterministically, from the state:

```
provider state + adapter version + schema version = effective_config
```

Why it is needed — without it the reconciler produces false results:

- **Provider defaults.** You asked for `ocpus: 4`; the read also shows
  `memory_in_gbs: 64` (a default). Raw compare → false drift. `effective_config`
  normalizes and can tag origin (`requested` vs `provider_default`).
- **Intent vs result form.** `isIpv6Enabled: true` (instruction) comes back as
  `ipv6CidrBlocks: [...]` (result). Not a field equality — a semantic mapping.
- **Name↔ID resolution.** Intent `security_lists: [frontend]` vs observed
  `securityListIds: [ocid1...]`. Without resolving the OCID back to the logical
  name, every cycle re-updates. `effective_config` resolves it → `desired ==
  effective` → no needless update.
- **Full-replace lists.** OCI list updates often replace the whole list. To add
  one member safely you must know the current effective set, or you drop the
  unmanaged ones.

Compliance is `satisfies`, not `==`:

```
satisfies(effective_config, config_surface) == true    # may hold
effective_config == config_surface                     # need not
```

## Where it lives — in the state, computed by the module

The provider-specific translation (OCI read → CIC-normalized config) is the
**module's** job — only the module knows OCI's mapping, defaults, and multi-
resource composition. Putting it in the host would leak OCI into the CIC core.

So: the **module** returns `raw provider state` + `effective_config` (normalized)
+ `operational state` + `provider_metadata`; the **host** runs the object-level
`satisfies(intent, state)` verification (P4.1) and seals the evidence. The module
does not certify its own work.

Structurally, `effective_config` is a derived, read-only part of the state
branch (role `derived`, `config: false`), not a top-level surface. A precise name
is `applied_config` / `normalized_config` — "effective" can read as a third
desired state, which it is not.

```
ManagedEntity
├── config_surface        # intent: desired
└── state_surface
    ├── effective_config  # derived, read-only, comparable to intent
    ├── operational       # lifecycle, health, metrics
    └── provider_metadata # id, revision(etag), observed_at
```

## Three kinds of drift the model separates

1. **Desired–effective drift** — `config_surface != effective_config`: a real
   configuration drift.
2. **Effective–operational** — config fine, `state_surface` unhealthy
   (`stopped`, `degraded`): a runtime problem, not config drift.
3. **Accepted difference** — byte-different but `satisfies` holds (IPv6 enabled +
   assigned blocks): conformant, not drift.
