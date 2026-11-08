# Approval, authority and execution state (M4b)

Status: **concept**. Nothing in this document is implemented in the module or the Relay.
The module side (M4a, `plan-digest.md`) proves that a request is internally consistent and
echoes an `execution_digest`; this document says what the Relay has to add so that an
execution is also *authorized*, happens *at most once*, and leaves *evidence*. The Relay is
read-only from this repository: what follows is the behaviour we need from it
(`relay-requirements.md`, R6 to R10), not a design of it. Relay facts are from CIC-Relay at
commit `55a899f`, read, not executed; anything not read is marked NOT VERIFIED.

## The three questions, kept apart

| Question | Answered by | Where |
|---|---|---|
| Is this exactly the plan and target we mean? | the module, by deriving it again | M4a, implemented |
| Did an authenticated principal allow this execution, under which policy, within which limits? | the Relay | M4b, concept |
| Was this approval used before, by whom, and what happened? | the Relay, durably | M4b, concept |

A request that is consistent is not approved; an approval is not proof that the request
matches it (the Relay compares its digest); and a used approval says nothing about whether
the provider acted. They are checked independently and tied together by one value, the
`execution_digest`.

## What an approval names

An approval is a record the Relay can authenticate. It names, at least:

- `execution_digest` (the module's: it binds `plan_digest` and `target_digest`); an approval
  for any other digest is not an approval for this execution;
- the approver, and the policy decision that allowed it (an identifier of the decision, not
  a bare "yes");
- its scope: the kind, the operation class, and optionally a specific authenticated run;
- its validity window (not before, not after) and its revocation state;
- the digest of the module artifact that was verified when the approval was bound, so an
  approval cannot be spent on a different module build (see the negative tests).

The module never decides whether an approval is genuine. It only produces the digest.

## The execution state of one approval

The Relay keeps one durable record per approval and moves it through these states. Every
transition is recorded before the action it permits, and acknowledged by the store.

```
ISSUED --reserve--> RESERVED --about to send step i--> SEND_ATTEMPTED(i) --answer--> ANSWERED(i)
   |                    |                                    |
   |                    +--run ends, zero sends--> RELEASED   +--no answer recorded--> UNKNOWN
   +--expired / revoked--> VOID                               
```

- **Reservation is atomic and durable.** `reserve` is a compare-and-set from ISSUED to
  RESERVED keyed by the approval, recording the authenticated run, the `execution_digest`
  and the verified module digest. Of any number of concurrent requests for one approval,
  at most one wins; the others are refused. The reservation is committed to durable state
  and acknowledged **before the first `sign` or `actuate` of that execution**. A reservation
  that only lives in memory is not a reservation.
- **Write-ahead before every provider request.** Before each request that can cause a
  side effect leaves the Relay, the Relay records SEND_ATTEMPTED for that step; when the
  answer arrives it records ANSWERED with what the provider said. This is what makes a
  crash or a lost answer visible afterwards.
- **Zero sends may be released.** The Relay brokers every `actuate`. If a run ends with no
  SEND_ATTEMPTED at all (the module refused the request in its own gates, or the run failed
  before sending), nothing can have reached the provider, and the reservation may return to
  ISSUED (RELEASED) so the approval can be used again, within its validity. Once any
  SEND_ATTEMPTED exists the reservation is never released automatically.
- **A component restart** must find the state as it left it: a RESERVED record with no
  SEND_ATTEMPTED older than its lease may be released; a SEND_ATTEMPTED with no recorded
  answer becomes UNKNOWN.

The reservation and the write-ahead record are **safety state, not telemetry**. Recording
them durably is fail-closed: if a reservation or a write-ahead record cannot be made durable,
no provider request is sent. They cannot be best-effort, and they are kept apart from any
optional trace processing (see "Evidence").

### UNKNOWN is not failure and is not retryable

The case that decides the design: the Relay sent a provider request and lost the answer
(timeout, restart, a transport error after the request left). It does not know whether the
operation happened.

- The execution is **not** recorded as failed and the approval is **not** released.
- Nothing is resent automatically: not by the Relay, not by a retry in the module, not by
  re-running the same request. A second create can create a second resource.
- The state leaves UNKNOWN only through **reconciliation by evidence**, which is itself
  recorded: for an update or an action, an observation compared with the desired state and
  the revision; for a create, evidence that the resource exists or provably does not; for an
  asynchronous operation, the Work Request correlated with this execution (its id from the
  provider's answer; if the answer with the id was the lost one, a correlation by other
  evidence, which has to be defined and measured before it is relied on).
- A provider answer that proves the request had no effect (a validation or authorization
  refusal) may be ANSWERED as rejected; an ambiguous answer (a 5xx, a gateway timeout) is
  not such a proof and stays UNKNOWN until reconciled.
- NOT MEASURED: whether the OCI operations used here accept an idempotency token that the
  `execution_digest` could be derived into; nothing here assumes one.

Reading the Relay at `55a899f` (R8): the guest-facing `actuate` returns every transport
error as one error text, so a request that was never sent and one whose answer was lost look
alike to the guest. That is why the UNKNOWN rule has to be the Relay's.

## The host boundary

The module's own gate (M4a) protects only the module's `execute` path. The thing that causes
an external side effect is not that path but the host boundary: `cic-flow.sign` signs the
bytes the guest hands it, and `cic-flow.actuate` forwards the guest's own method, URL,
headers (its `Authorization` verbatim) and body (read at `55a899f`). A valid
`execution_digest` and a verified approval therefore prove nothing about which HTTP request
actually leaves, unless the relay ties the host calls to the approved execution.

**Required property (not an implementation).** Before every `actuate` that can cause a side
effect the relay checks, from its own authenticated context, that the actual method, target,
request body and step belong to the reserved, approved execution; the requests an execution
may make are determined from the approved execution (for instance through the plan its
digest covers), never from what the guest asks for. A `sign` that serves no step of a
reserved execution is refused too, because a signature over bytes the relay cannot tie to a
step is a capability on its own. A digest or a trace id supplied by the guest is not proof of
authority. Another guest path, another call, or a modified request must not be able to go
around the approval.

## Evidence (ProofTrace)

The trace of an execution keeps five things apart, as separate kinds of entry, never
merged into one "result":

| Kind | What it says | Minimum content |
|---|---|---|
| **requested** | what was asked | authenticated requester, authenticated run id, `execution_digest`, `plan_digest`, `target_digest` |
| **authorized** | what was allowed | `approval_id`, approver, policy decision id, scope and validity, the reservation transition (ISSUED to RESERVED) |
| **sent** | what went to the provider | per step: index, operation, method, URL without secrets, a digest of the request, time, the digest of the verified module artifact |
| **confirmed** | what the provider said back | status, `opc-request-id`, etag, Work Request id, a digest of the answer |
| **uncertain** | what is not known | the UNKNOWN state, why, and what evidence is missing |

A signed **observation** is its own kind: it says what a read returned, from which source,
at which time. It is never recorded as the proven physical state of the resource.

The entries are hash-chained as the existing audit mechanism does (R4), and the trace names
the same `execution_digest` the module echoes in its result.

Two kinds of recording are kept apart. The evidence that makes an execution safe (the
reservation, the write-ahead record, the transitions above) is durable and fail-closed. Richer
or analytic processing of the trace (display, aggregation, export) may stay optional and
best-effort. In the Relay at `55a899f` the cabinet's ProofTrace recorder is optional (`nil`
skips it, `core/cabinet/service.go:99`, `:105-106`) and runs once, after all steps have run,
with a failure logged and the workflow continuing (`:299-307`): a post-hoc, best-effort
record, which is not what the reservation and the write-ahead record need to be.

## Entry points

`execute` is the one with a plan and a digest. `destroy` and `invoke` are separate entry
points of the module with no plan: a delete and an action on a named resource. They must
not remain a way around the approval model, so an approval has to be required for them too:

- Relay side: R10. NOT VERIFIED whether the Relay sees the operation name (`execute`,
  `destroy`, `invoke`) before the guest runs; the cabinet at `55a899f` dispatches only
  `init`, `process`, `get` and `notify` to a guest (`core/cabinet/service.go:228-240`).
- Module side (**concept**, not built): a digest for `destroy` and `invoke` over the same
  framing (operation, kind, the target, and for `invoke` the canonical body), so an approval
  can name it. Until it exists the module cannot hand the Relay a value to approve for them.

`observe` and `poll` only read; whether they need an approval is a policy choice of the
Relay, not decided here.

## Acceptance tests (to be written when the Relay side exists)

Each is a test the Relay's owner can run against the mocked provider; "no provider request"
means zero `sign` and zero `actuate` calls at the provider seam.

| Case | Given | Expected |
|---|---|---|
| No approval | an `execute` with no approval, or an unauthenticated one | no provider request |
| Wrong digest | an approval for another `execution_digest` | refused, no provider request |
| Expired or revoked | an approval outside its window or revoked | refused, no provider request |
| Two concurrent uses | N parallel requests for one approval (N >= 2, repeated) | exactly one reserves; the rest are refused; the provider sees the requests of one execution only |
| Lost answer | the provider seam drops the answer to a sent request | the state is UNKNOWN; after any number of retries and a restart the provider has still seen that request once; no automatic resend |
| Reconciliation needed | an UNKNOWN execution | it leaves UNKNOWN only through a recorded evidence entry |
| Crash after reservation | the component stops after RESERVED, before any send | after restart the approval is releasable (zero sends), never executed twice |
| Crash after write-ahead | the component stops after SEND_ATTEMPTED | after restart the state is UNKNOWN, not RELEASED |
| Module refused | the module refuses the request in its own gates (zero sends) | RESERVED returns to ISSUED; the approval is still usable within its window |
| Changed module artifact | the module digest differs from the one verified when the approval was bound | the approval is not usable; no provider request |
| Destroy or invoke without approval | `destroy` or `invoke` called directly | refused, no provider request |
| A different request under a valid approval | a reserved, approved execution; the guest sends another method, target, body or step than the approved one | refused, no provider request |
| The host function called directly | the guest calls `cic-flow.actuate` or `cic-flow.sign` outside any reserved, approved execution context | refused, no provider request |
| Durable record cannot be written | the store fails at the reservation, and again at a write-ahead record | no provider request in either case |
| Trace shape | any execution, including an UNKNOWN one | the five kinds are present where they apply and distinguishable; an observation is not labelled as proven state |

## What this does not claim

Not that the Relay does any of this today (it does not: see R6 to R10), not that the
store, the policy engine or the identity source exist, not that an approval, once
authenticated, makes the plan safe, and not that a reconciled UNKNOWN proves what the
physical state is: it records the evidence that was found. `module.wasm` is not rebuilt,
and a production integration stays on hold until the Relay side exists and is measured.
