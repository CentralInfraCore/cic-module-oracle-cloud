# Destroy and Invoke: what they do today, and a binding contract proposal

Status: section 1 is a **measurement** of the module at `eda4aeb`. Sections 2 to 5 are a **proposal
(concept)**: nothing in them is implemented in the module or the Relay, and no approval is verified
anywhere by anything described here.

The structure is deliberate. Section 1 records what was observed, without reference to any contract
we would like. Section 2 derives, from those observations only, the properties a contract needs.
Section 3 is the contract that has those properties. Section 4 says what the module can prove and
what stays with the Relay. Section 5 lists the decisions left open, and section 6 the questions that
follow from the survey and are handed to the D5 contract (the origin of the target data).

Why it matters: the plan binding of `execute` (`plan-digest.md`) holds only for `execute`.
`destroy` and `invoke` reach the same host with the same key and are not covered by it. The Relay
requirements R7 and R10 (`../relay-requirements.md`) ask for an approval before any operation that
can change the provider's state; the module has to be able to give the Relay a value to approve for
these two entry points as well.

## 1. Measured current behaviour

### 1.1 Method

Scratch probes in a copy of the module made with `git archive` of `eda4aeb`; the host seam is the
module's own test mock (`sign` returns a fixed signature, `actuate` answers as configured); no OCI
endpoint, credential or network. "sign" and "actuate" are the numbers of calls that reached the
mock. The probe sources, the full output, the exact environment and the way to rerun are in
[`../measurements/destroy-invoke/README.md`](../measurements/destroy-invoke/README.md). Sections
1.2 and 1.3 also rest on reading `module/provider.go` (`Destroy`, `Invoke`, `renderBody`,
`resolveOp`), marked "read".

### 1.2 What the entry points do (read)

- **`destroy`** resolves the kind's single delete-role operation (`resolveOp(kind, roleDelete, ...)`),
  checks that `binding.resource_id` is a well-formed OCID, and sends a `DELETE` with no body and
  `If-Match` = `binding.revision`. A 404 is reported as `not-found`. The request carries no plan, no
  digest and no state; the result echoes no digest.
- **`invoke`** takes `kind`, `operation`, `config` and `binding`. It looks the operation up in the
  kind's registry (`c.operations[req.Operation]`) and does not look at its role. The body is the
  bytes of `config.data` as received (`{}` if there is no `config`). It checks the resource id like
  `destroy`. The registry-conformance and plan-binding gates of `execute` do not apply to it. The
  result echoes no digest.
- **`renderBody`** (used by `execute`) filters by role: a create keeps every contract field present;
  an update keeps only `mutable` fields; an action keeps only the `action-managed` fields whose
  `action` is the operation's name; a delete has no body; any other role (a read) gets `{}`.

### 1.3 Observations

**O1. Destroy: every target dimension changes the request that is signed and sent** (measured;
the resource was `ocid1.vcn.oc1..approved` on the baseline binding).

| Change (one at a time) | sign | actuate | What went out |
|---|---|---|---|
| none (baseline) | 1 | 1 | `DELETE .../vcns/<id>`, no body, `If-Match` = revision |
| `binding.resource_id` | 1 | 1 | another resource |
| `binding.host` | 1 | 1 | another region's host |
| `binding.base_path` | 1 | 1 | `/20990101/...` |
| `binding.key_id` | 1 | 1 | another key id in the signature |
| `binding.revision` | 1 | 1 | `If-Match: etag-STALE` |

**O2. Invoke: the same five dimensions** (`ChangeVcnCompartment`, baseline body
`{"compartmentId": ...}`): each change runs with 1 sign and 1 actuate and the changed value on the wire.

**O3. Invoke runs any registry operation, whatever its role** (measured; same binding, same body;
1 sign and 1 actuate each):

| `operation` | registry role | What went out |
|---|---|---|
| `DeleteVcn` | delete | `DELETE .../vcns/<id>` with the body that was given |
| `UpdateVcn` | update | `PUT .../vcns/<id>` with the body that was given, unfiltered |
| `CreateVcn` | create | `POST .../vcns` with the body that was given |
| `GetVcn` | read | `GET .../vcns/<id>` with a body |
| `AddVcnCidr` | action | `POST .../actions/addCidr` |

**O4. Invoke sends the caller's body text as it is** (measured on raw request text):

| `config.data` | Sent |
|---|---|
| inner whitespace and key order | unchanged (the outer whitespace is trimmed) |
| a duplicated key with two values | **both sent** |
| a u-escape sequence | as written, escape kept |
| `9007199254740993`, `1.0` | as written |
| fields other than the action's (`displayName`, `lifecycleState`) | **all sent** |
| JSON `null`, an array, a string | `null`, the array, the string |
| `config` absent | `{}` |
| not JSON (the whole request is then not JSON) | request refused, nothing signed |

**O5. For the same operation `execute` filters the input and `invoke` does not** (`renderBody` was
measured directly on the input fields `compartmentId`, `displayName`, `lifecycleState`, `evil`;
the `invoke` column is what O3 and O4 measured: the body as it was given, with extra fields sent
for `ChangeVcnCompartment` and `UpdateVcn`, and the given body for the other operations):

| Operation (role) | `renderBody` (execute) | `invoke` |
|---|---|---|
| `ChangeVcnCompartment` (action) | `{"compartmentId":...}` | the body as given |
| `UpdateVcn` (update) | `{"displayName":"X"}` | the body as given |
| `CreateVcn` (create) | `compartmentId` and `displayName` | the body as given |
| `DeleteVcn` (delete) | no body | the body as given |
| `GetVcn` (read) | `{}` | the body as given |

**O6. Replay: each repeat signs and sends again**, separately from what the provider answers
(measured):

| Case | Results | sign | actuate |
|---|---|---|---|
| `destroy`, the same request 3 times; the provider answers 204, then 404 | `ok`, `not-found`, `not-found` | **3** | **3** |
| `invoke`, the same request 5 times | all `ok` | **5** | **5** |

The 404 on the repeats is the provider's idempotent behaviour (the resource is gone). It is not the
module declining to act: every repeat is signed and sent. A repeated `invoke` can have a new side
effect.

**O7. Consumers.** In the locally cloned CentralInfraCore repositories and in the cic-factory
`tools/` there is no consumer of the module's `destroy` or `invoke` entry points apart from the
module's own tests and manual harness. Repositories not cloned here and other environments were not
checked.

### 1.4 What was not measured

The real provider, the Relay (what it does with calls that reach it), whether OCI accepts any of
the bodies above, and the Relay's behaviour when `invoke` names a `delete` operation.

## 2. Properties derived from the observations

| Property | From |
|---|---|
| **P1.** The target dimensions (`host`, `base_path`, `key_id`, `resource_id`, `revision`) are bound to the request by a value the module computes from the request itself, and the module refuses a request whose value does not match, before it signs. | O1, O2 |
| **P2.** `invoke` does not run a delete, update, create or read operation: those have `execute`, `destroy` and `observe`. | O3 |
| **P3.** The body that is sent is a defined function of the value that was bound, not the caller's text. | O4, O5 |
| **P4.** The body of an `invoke` has a defined shape: an object, only the operation's declared fields, strict JSON with no repeated key. | O4 |
| **P5.** Single use is not provided by the module and cannot be: it has to be the Relay's, and the measure of it is the number of sign and actuate calls, not the provider's answer. | O6 |
| **P6.** A result names the digest of what was executed, so a receipt can be tied to what was approved. | 1.2 (no echo today) |

## 3. Proposed contract (concept)

One field name for all three entry points, `execution_digest` (the `execute` one already exists);
the digest envelope's tag `v` tells the kinds apart, so a value of one kind cannot be taken for
another. All envelopes are `cic-canonical-json/1` objects with structural framing, as in
`plan-digest.md`.

- **`destroy`**, `v` = `cic-oracle-destroy/1`: members `action` (`destroy`), `contract` (the kind's
  contract digest), `kind`, `operation` (the resolved registry operation: `name`, `method`, `path`,
  `role`), `target_digest` (the `execute` model: `host`, `base_path`, `key_id`, `resource_id`,
  `revision`), `v`.
- **`invoke`**, `v` = `cic-oracle-invoke/1`: the same members and `body`, the canonical value of
  the body that will be sent.

The `target_digest` function is the one `execute` uses; the full digest still covers the action
kind, the kind, the registry's operation contract and the target. (P1, P6)

**The body (P2, P3, P4).**

1. `invoke` runs only operations whose registry role is `action`; any other role is refused
   (`validation`, `operation`).
2. The body is a JSON object, not an array, a string or `null`.
3. It has only the operation's declared fields; any other field is refused (not silently dropped,
   so that what was approved and what was meant cannot drift apart).
4. It is strict, bounded JSON (`canonical-json.md`): a repeated key or a value beyond the bounds is
   refused.
5. The bytes that are sent are produced by one function from the canonical value, `wire(value)`.
   The digest binds the value, the module sends exactly `wire(value)`, and a test pins that the
   canonical form of the sent bytes is the value that was digested. The digest is therefore about
   the HTTP body that goes out, not about the input text.
6. The number form of `wire` is open (D-DI-3): the canonical `<digits>e<exp>` (valid JSON, but a
   provider may refuse an exponent for an integer field), or a plain decimal when it is lossless
   and bounded (`1`, `0.5`, `100`), refused otherwise. Whether OCI accepts either was not measured.

**Before it signs**, the module resolves the operation from the registry, checks the target (`host`
and `key_id` present, every field bounded, `resource_id` a well-formed OCID and always required here),
checks the body (invoke), computes the digest and requires it to equal the `execution_digest` in the
request. A refusal is `validation` with the dimension in `field_path`, with no sign and no actuate
call. The result echoes `execution_digest`. A request without the field is refused (an ABI break,
as for `execute`; there is no consumer, 1.3 O7).

There is no plan: `destroy` and `invoke` are not derived from desired and observed state, so
there is nothing to derive again. The module therefore cannot judge whether a given delete is
needed; that is a policy question for the Relay.

## 4. What can be proved where

| Statement | Who can prove it |
|---|---|
| The request's content is what the digest was made from; the body's meaning is bound; the operation is the registry's `action` | the module (it recomputes) |
| The five target values are the ones in the digest | the module (it recomputes) |
| That those values name the right OCI environment, tenant or resource | not the digest: the Relay and the D5 contract (section 6) |
| That someone approved it, who, under which policy | the Relay (R7) |
| Single use, concurrency, an unknown outcome | the Relay (R8). O6 is the measured absence of it in the module |
| That the HTTP request that leaves is the one the module built | the Relay's host boundary (R7): `sign` and `actuate` act on the bytes the guest hands them |
| That `destroy` and `invoke` cannot be reached without an approval at all | the Relay (R10). The module's digest is a precondition, not an enforcement |

The module-side digest lets the Relay approve one concrete, content-bound value. It does not by
itself stop a caller from reaching the provider by another path through the host boundary.

## 5. Open decisions

- **D-DI-1.** `invoke` runs only `action` operations (proposed: yes; it ends the O3 bypass).
- **D-DI-2.** A field outside the operation's declared set: refuse (proposed) or drop silently.
- **D-DI-3.** The number form of `wire` (3, item 6). Not decidable without a measurement.
- **D-DI-4.** One field name `execution_digest`, told apart by the tag `v` (proposed).
- **D-DI-5.** Keeping the old digest-less `destroy` and `invoke` request: no (proposed; no consumer found).

## 6. Questions handed to D5 (the origin of the target data)

These follow from the survey (O1, O2) and are not answered here. The digest binds the values
together; it does not show that they name the right OCI environment or entitlement.

| Field | Today (measured or read) | What D5 has to settle |
|---|---|---|
| `host` | given by the caller in `binding`; every change in O1 and O2 ran; the only host-side limit read is the Relay's egress allowlist (host, method, path, port, from the module manifest) | where the value comes from (region and realm), who authenticates it, and how the manifest allowlist relates to an approval |
| `base_path` | given by the caller and put in front of the path unchecked by `observe`, `execute`, `destroy` and `invoke`; only `poll` is strict (`/` and 8 digits); the `poll` alias takes the version from the caller when `base_path` is empty | where the service's API version comes from (the registry), and the rule for an empty value |
| `key_id` | given by the caller; the module writes it into the `Authorization` header; the actual signature comes from the Relay's own Vault key (read: `cicFlowDeps.sign`) | how a key is tied to a tenant and a principal: the Relay's (R6, R7); the module only knows the name |
| `revision` | given by the caller (an etag from an observation); the digest binds it | freshness does not follow from the digest; the observation's source and time are needed for that |

## What this does not claim

Nothing here is implemented. The Relay does not require or verify an approval for `destroy` or
`invoke` today, and the proposed digest would not make it do so. The measurements were made
against a mock host seam, not a provider, and show what the module does with a request, not what
OCI or the Relay does with it.
