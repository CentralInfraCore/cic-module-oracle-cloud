# Manual verification against a real OCI tenancy

`module/manual_real_oci_test.go` is an opt-in harness that runs the module's
own handler functions (`Describe`/`Observe`/`Validate`/`Plan`/`Execute`)
directly against a real OCI tenancy, using the real OCI API-key signing
scheme. It exists because the module's other tests
(`relay_integration_test.go`, `module_loadtest_test.go`) prove the
WASM/ABI/signing-format plumbing against a **mock** OCI server and a **local**
RSA key standing in for Vault — they never prove the module's request/response
logic against **real** OCI. This harness fills that specific gap, cheaply,
without Terraform/Vault/a running relay.

Verified against two tenancies with different realms: the commercial realm
("oc1" in the OCID, `oraclecloud.com`) and an EU Sovereign Cloud realm ("oc19",
`oraclecloud.eu`) — set `OCI_REALM_DOMAIN` accordingly (default
`oraclecloud.com`); the harness does not infer it from the OCID.

**Status legend:** `verified` — run against real OCI, passed · `not run` —
possible but not yet tried · `blocked` — tried, needs something this harness
doesn't provide.

## What it does NOT cover

- The real `module.wasm` / wazero / ABI-dispatch path (`abi.go`) — this harness
  calls the Go handler functions directly, under the host (`!wasip1`) build.
- The relay's real `cic-flow` host functions (Vault Transit signing, egress
  policy, capability-manifest enforcement) — `testCallHostSign`/
  `testCallHostActuate` substitute a local RSA key and a plain `net/http` call.
- `invoke` — run for real 2026-08-05 (`ChangeInstanceCompartment`, see the
  coverage row) and it exposed a real defect: `Invoke()` reported `succeeded` on
  an asynchronous `202`, discarding the `opc-work-request-id`, so the caller
  could not poll and was told the work was done when it was not. The fix
  (`operationResult.WorkRequestID`, `status: accepted` when the header is
  present — job `oci-invoke-async-result`) is landed and covered by fixtures
  (`TestInvokeAsync`, `TestInvokeFailedStatusUnchanged` in
  `module/provider_test.go`), but **not yet re-run against real OCI** — that is
  the orchestrator's job (see `orchestrator-verification.md` in the job
  output). Until that re-run happens, this row's real-OCI status still
  reflects the pre-fix measurement below.
- `invoke` on any action other than `ChangeInstanceCompartment` — the extraction
  recipe only admits actions whose request body model was explicitly supplied,
  so body-less (query-param) actions such as instance power state are not in the
  schema at all. See the cic-factory job output `invoke-scope.md`.
- `destroy` — verified for real on both a synchronous kind (`cic:network:vcn`,
  `DeleteVcn`/`204`/`succeeded`) and an asynchronous one with a different delete
  name shape (`cic:compute:instance`, `TerminateInstance`/`204`/`accepted` +
  Work Request), 2026-08-05. Rejected deletes are measured too — see the "Error
  paths" section above (`409` conflict and `404` not-found). What remains
  unmeasured there is permission (`401`/`403`), precondition (`412`), throttling
  (`429`) and `5xx`.
- `execute` is exercised (Create/Update/Delete), but only ever run against an
  empty, personal/non-production trial tenancy, and the resources were torn
  down again afterward — never run against the paynance production tenancy
  used for the read-only ops above, and no resource from this harness is left
  running in either tenancy.

Both of the above are real, separate verification work — see
[`relay-requirements.md`](relay-requirements.md) for the relay-side gaps.

## Error paths

Everything above is the happy path. The error mapping (`ociError`,
`module/provider.go:893`) decides the CIC error class and preserves OCI's native
code — that is what a ProofTrace consumer reads when something goes wrong, so it
is worth measuring rather than assuming. Run 2026-08-05 against real OCI:

| Case | How it was provoked | HTTP | Where the error surfaced | `error_class` | `provider_code` |
|---|---|---|---|---|---|
| conflict | `Destroy()` a VCN that still has a subnet attached | `409` | step-level, `result.status: failed` | `conflict` | `IncorrectState` |
| not-found | `Destroy()` the same subnet twice | `404` | **envelope-level**, `status: error` | `not-found` | — (not preserved as measured 2026-08-05; fixed since, see note below — not yet re-verified against real OCI) |
| validation | `Execute(CreateVcn)` with `cidrBlock: 999.0.0.0/16` | `400` | step-level, `result.status: failed` | `validation` | `InvalidParameter` |

Two things this pins down:

- The mapping is right on the real path, not just against fixtures, and OCI's
  message survives verbatim (`"...is associated with Subnet that is in use"`,
  `"The requested CIDR 999.0.0.0/16 is invalid: unable to parse."`).
- **A 404 on destroy comes out in a different shape from every other error.**
  `Destroy()` short-circuits it deliberately (`provider.go:598`, and the doc
  comment at `:582-584` says so): the envelope itself becomes `status: error`
  with `class: not-found`. Every other failure stays inside a `status: ok`
  envelope as a failed *step* — worth knowing on its own, a caller parsing
  steps will find none for a 404. As measured 2026-08-05, OCI's own
  `code`/`message` were also dropped on this path while they were kept on all
  the others. **Fixed since** (job `oci-invoke-async-result`): the 404 branch
  now runs the response through `ociError` like every other branch and keeps
  `ProviderCode`, folding OCI's message into the synthesised
  `"resource already gone: <ocid> (...)"` text instead of discarding it. Proven
  at the fixture level (`TestDestroyNotFoundKeepsProviderCode`, negative
  direction shown too) — **not yet re-run against real OCI**; see
  `orchestrator-verification.md` in the job output.

Still unmeasured branches of `ociError`: `401`/`403` (permission), `412`
(precondition), `429` (throttle, the only branch that sets `retryable`), and
`5xx`. Provoking those needs either a deliberately under-privileged principal or
load, neither of which this harness sets up.

## Coverage

| Op | Kind(s) verified | Result | Notes |
|---|---|---|---|
| `describe` | n/a (pure, no OCI call) | **verified** | Returns the module manifest; `required_capabilities.egress_hosts` is `["*.oraclecloud.com"]` — see the realm caveat below. |
| `observe` | `cic:network:vcn`, `cic:network:subnet` | **verified** | Real signed GET against a live tenancy; `state` is confirmed to be OCI's raw response verbatim, `effective_config` the filtered/derived config-surface projection. |
| `validate` | `cic:network:subnet` | **verified** | Fed a real resource's own `effective_config` back as `intent` — `admissible: true`, both `envelope.well-formed` and `schema-conformance` checked. |
| `plan` | `cic:network:subnet` | **verified** | Two cases: `desired == observed` → `noop`; one `mutable` field changed (`displayName`) → `update` with a concrete `UpdateSubnet` (`PUT /subnets/{subnetId}`) provider operation. |
| `poll` | — (Work Request, kind-agnostic) | **verified** | `TestManualRealOCIPoll` run against a live `LaunchInstance` Work Request, 2026-08-04: `work_status: SUCCEEDED`, `terminal: true`. Found and fixed a real bug on this first run — `percent_complete` was always `0` (OCI sends `percentComplete` as a JSON float; the struct field was an `int`, so `encoding/json` silently zeroed it and the decode error was discarded) — see commit `4e577c8`. Fixed and re-verified: `percent_complete: 100` alongside `SUCCEEDED`. Re-verified 2026-08-05 on a Work Request that `Destroy()` itself produced (the `TerminateInstance` one, fed straight from the destroy result's `work_request_id`): `IN_PROGRESS`/`0%`/`terminal: false` first, then `SUCCEEDED`/`100%`/`terminal: true`. That closes the async loop end to end — `Destroy` → `work_request_id` → `Poll` → terminal — rather than polling a Work Request produced by the CLI. |
| `execute` (Create) | `cic:network:vcn`, `cic:network:subnet` | **verified** | `CreateVcn` then `CreateSubnet` (inside that VCN) against an empty personal trial tenancy (commercial realm) — both `http_status: 200`, synchronous, no work request. Each resource independently confirmed via `oci network vcn/subnet get`. |
| `execute` (Update) | `cic:network:vcn` | **verified** | `UpdateVcn` (`displayName` change) on the VCN just created — `http_status: 200`, new `etag`. **Gotcha**: the first re-`Observe` after this appeared to show the *old* value — this was Go's test-result cache silently replaying the previous identical invocation, not a real failure; `oci network vcn get` and a `-count=1` re-run both confirmed the update took effect immediately. Always pass `-count=1`. |
| `execute` (Delete) | `cic:network:vcn`, `cic:network:subnet` | **verified** | `DeleteSubnet` then `DeleteVcn` (order matters — a VCN can't be deleted while a subnet is attached) — both `http_status: 204`, synchronous. Confirmed gone via a 404 on `oci network subnet get` and an empty `oci network vcn list` afterward. Tenancy is empty again. |
| `destroy` | `cic:network:vcn`, `cic:compute:instance` | **verified** | `TestManualRealOCIDestroy` calls `Destroy()` directly — the codepath every prior delete test bypassed by going through `Execute(OCI_EXEC_OPERATION=Delete…)` instead. Both runs 2026-08-05 against the POC trial tenancy (`eu-frankfurt-1`, commercial realm). **Synchronous case** — throwaway VCN (created via `Execute(CreateVcn)`, `200`): operation label `DeleteVcn`, `http_status: 204`, `status: succeeded`; confirmed gone via `404 NotAuthorizedOrNotFound`. **Asynchronous case** — Always Free `VM.Standard.E2.1.Micro` instance: operation label **`TerminateInstance`**, `http_status: 204`, `status: **accepted**` (not `succeeded`), plus a `work_request_id`. Two distinct things are proven here: (a) `resolveOp(kind, roleDelete, binding)` picks the delete op out of the embedded contract by *role*, not by name shape — `TerminateInstance`, not `DeleteInstance`, which is exactly the bug class `oci-lifecycle-role-bridge` fixed; and (b) the async path is distinguished from the sync one at the result level (`accepted` + work request vs `succeeded`). Instance independently confirmed `TERMINATED`. |
| `invoke` | `cic:compute:instance` | **verified, with a defect (fix landed, not yet re-verified against real OCI)** | Run 2026-08-05 against a live instance, moving it into a scratch compartment created for the run: `ChangeInstanceCompartment`, `http_status: 202`, `status: succeeded`. The action itself works — `oci compute instance get` confirmed the instance's `compartment-id` was the new one. **But the result was wrong about being finished.** OCI answered `202 Accepted` and returned an `opc-work-request-id` (measured directly with `oci raw-request` on the same endpoint: `opc-work-request-id: ocid1.coreservicesworkrequest…`). `Invoke()` reported `succeeded` anyway and dropped the id: `operationResult` (`module/provider.go:626`) had no `work_request_id` field, and the status was hardcoded to `succeeded`, downgraded only on `>= 400`. `Execute`/`Destroy` handled this correctly on their own path (*"An async op (202 Accepted, or an opc-work-request-id) is not done — the caller must poll the Work Request. Surface the id, not a false success."*), which `Invoke` failed to do. **Fixed** (job `oci-invoke-async-result`): `operationResult` now carries `work_request_id`, and `Invoke()` reports `accepted` when the header is present, matching `Destroy`'s rule. Proven at the fixture level only (`TestInvokeAsync`, `TestInvokeFailedStatusUnchanged`) — the negative direction was shown too (the new test fails against the pre-fix code). **Not re-run against real OCI yet** — see `orchestrator-verification.md` in the job output for the exact recipe. |
| `cic:compute:instance` schema | n/a (extraction, no OCI call) | **verified** | `make oci.generate` now also emits `module/schemas/core/instance.json`; the extractor resolves `LaunchInstance→create`, `TerminateInstance→delete`, `UpdateInstance→update`, `ChangeInstanceCompartment→action` from the SDK's own HTTP surface (`tools/oci-extract/resolve.go`), matching the P2.5 audit. `describe()`'s `resource_kinds` includes `cic:compute:instance` (measured via `TestManualDescribe`); `required_capabilities.egress_hosts` is unchanged (`["*.oraclecloud.com"]` — a single wildcard declared once in `Describe()`, not derived per resource kind). |

## A concrete finding from `describe`

`Describe()`'s `required_capabilities.egress_hosts` is `["*.oraclecloud.com"]`.
The tenancy this harness was verified against is an **EU Sovereign Cloud**
realm tenancy (OCID realm segment `oc19`), whose real API host is
`iaas.<region>.oraclecloud.eu` — a different suffix. If the relay enforces the
capability manifest literally, this module cannot reach this realm's OCI API
until the manifest (or the enforcement) accounts for non-commercial realms.
This was not previously documented — recorded here as evidence, not yet raised
as an `R#` item in `relay-requirements.md` pending a decision on scope.

## Usage

**Running without a host Go toolchain.** The commands below assume `go test`
runs directly on the host. If Go only exists inside the `builder` container
(`docker compose`), the host mount is the actual obstacle, not `OCI_KEY_PATH`
alone: `docker-compose.yml` does not mount `$HOME/.oci` into `builder`, and
`mk/golang.mk`'s own advice — put the key under a repo-relative gitignored
path — is a trap as shipped, because `.gitignore` has no `*.pem` rule, so a key
dropped under the repo is one `git add -A` away from being tracked. Measured
2026-08-05, this is what actually works — mount the key file directly by path,
bypassing the repo tree entirely:

```bash
docker compose run --rm --no-deps \
  -v "$OCI_KEY_PATH_ON_HOST:/run/oci-key.pem:ro" \
  -e OCI_KEY_PATH=/run/oci-key.pem \
  -e OCI_TENANCY_OCID -e OCI_USER_OCID -e OCI_FINGERPRINT -e OCI_REGION \
  -e OCI_REALM_DOMAIN -e OCI_TEST_KIND -e OCI_TEST_RESOURCE_ID \
  -e REAL_OCI_TEST=1 \
  builder sh -eu -c 'cd /app/module && go test -tags manual_real_oci -count=1 -run <TestName> -v ./'
```

(`OCI_TENANCY_OCID` etc. with no `=value` forwards the host shell's exported
value into the container.) Swap `<TestName>` and the `-e` list per the
per-op commands below. `.gitignore` now has a `*.pem` rule (this job added it)
so a repo-relative key path is no longer a footgun either, but the mount above
avoids the question entirely.

Read-only ops (`Observe`/`Validate`/`Plan`):

```bash
OCI_KEY_PATH=~/.oci/oci_api_key.pem \
OCI_TENANCY_OCID=ocid1.tenancy.oc19..xxx OCI_USER_OCID=ocid1.user.oc19..xxx \
OCI_FINGERPRINT=xx:xx:xx:...             OCI_REGION=eu-frankfurt-2 \
OCI_REALM_DOMAIN=oraclecloud.eu \
OCI_TEST_KIND=cic:network:subnet         OCI_TEST_RESOURCE_ID=ocid1.subnet.oc19...\
REAL_OCI_TEST=1 go test -tags manual_real_oci -count=1 ./module/ -run TestManualRealOCI -v
```

`Execute` (mutating — see the warning at the top of the test function before
running this against anything but an empty/disposable tenancy):

```bash
# ... same OCI_KEY_PATH/OCI_TENANCY_OCID/OCI_USER_OCID/OCI_FINGERPRINT/OCI_REGION/OCI_TEST_KIND as above, plus:
OCI_EXEC_OPERATION=CreateVcn OCI_EXEC_METHOD=POST OCI_EXEC_PATH=/vcns \
OCI_EXEC_CONFIG_JSON='{"compartmentId":"...","displayName":"...","cidrBlock":"10.0.0.0/16"}' \
REAL_OCI_TEST=1 go test -tags manual_real_oci -count=1 -run TestManualRealOCIExecute -v ./module/
```

Omit `OCI_TEST_RESOURCE_ID` for `Create*` (the path has no `{param}` to fill);
set it for `Update*`/`Delete*` (`OCI_EXEC_PATH=/vcns/{vcnId}`, etc.). The
harness has no "list" capability of its own (the module ABI operates on one
resource_id at a time) — after a `Create`, find the new resource's OCID with
the `oci` CLI (e.g. `oci network vcn list --query "...displayName=='...'"`)
to feed into the next `Observe`/`Update`/`Destroy` call.

**Always pass `-count=1`** — see the `execute (Update)` row above.

`Poll` (read-only — GETs a Work Request, never mutates):

```bash
# ... same OCI_KEY_PATH/OCI_TENANCY_OCID/OCI_USER_OCID/OCI_FINGERPRINT/OCI_REGION as above, plus:
OCI_POLL_PATH=/20160918/workRequests/ocid1.workrequest... \
REAL_OCI_TEST=1 go test -tags manual_real_oci -count=1 -run TestManualRealOCIPoll -v ./module/
```

`OCI_POLL_PATH` needs the API version prefix — Poll GETs it verbatim, unlike
`Observe`/`Execute` which prepend `binding.base_path` to a registry path. Get a
live path from an async execute step's `work_request_id`
(`LaunchInstance`/`TerminateInstance` give one; `CreateVcn`/`UpdateVcn` don't —
see the `poll` coverage row above).

`Destroy` (mutating, irreversible — see the warning on
`TestManualRealOCIDestroy` before running this):

```bash
# ... same OCI_KEY_PATH/OCI_TENANCY_OCID/OCI_USER_OCID/OCI_FINGERPRINT/OCI_REGION as above, plus:
OCI_TEST_KIND=cic:compute:instance OCI_TEST_RESOURCE_ID=ocid1.instance... \
REAL_OCI_TEST=1 go test -tags manual_real_oci -count=1 -run TestManualRealOCIDestroy -v ./module/
```

Unlike the `execute (Delete)` row above (which drives deletion through
`Execute(OCI_EXEC_OPERATION=Delete<Resource>)`), this calls `Destroy()`
itself — the codepath that resolves the resource's role="delete" operation
from its own embedded contract (`resolveOp`), rather than trusting a
caller-supplied operation name. This is the codepath that writes the
operation label into the actual ProofTrace step in production.

`Invoke` (mutating — runs a named action operation; needs a target the action
is actually valid for, e.g. a second compartment for
`ChangeInstanceCompartment`):

```bash
# ... same OCI_KEY_PATH/OCI_TENANCY_OCID/OCI_USER_OCID/OCI_FINGERPRINT/OCI_REGION/OCI_TEST_KIND/OCI_TEST_RESOURCE_ID as above, plus:
OCI_INVOKE_OPERATION=ChangeInstanceCompartment \
OCI_INVOKE_CONFIG_JSON='{"compartmentId":"ocid1.compartment...."}' \
REAL_OCI_TEST=1 go test -tags manual_real_oci -count=1 -run TestManualRealOCIInvoke -v ./module/
```

Or via the Makefile wrapper (same env vars, exported before the call):
`make golang.test.manual-real-oci` — read-only ops only, because it runs
`-run "TestManualRealOCI(Observe|Validate|Plan|Poll)$"`. `Destroy`/`Invoke` have
no Makefile target on purpose (see the note in `mk/golang.mk`) — run them
directly with the commands above, deliberately, one at a time.

**Do not rely on a bare `-run TestManualRealOCI` to stay read-only.** Go's
`-run` pattern is an unanchored regexp, so `TestManualRealOCI` *does* match
`TestManualRealOCIExecute`, `TestManualRealOCIDestroy`, and
`TestManualRealOCIInvoke` — this file previously claimed the opposite, and the
claim was wrong. What actually keeps a bare run from mutating anything is each
mutating test's own env guard (`OCI_EXEC_*` for Execute, `OCI_TEST_RESOURCE_ID`
for Destroy, `OCI_INVOKE_*` for Invoke): with those unset the test fails
without issuing a request. If they happen to be set in your shell, a bare
`-run TestManualRealOCI` **will** mutate. Anchor the pattern, as the Makefile
target now does.

The harness never prints or persists the private key material — only its file
path is passed in; the parsed key lives in test-process memory for the
duration of the run.
