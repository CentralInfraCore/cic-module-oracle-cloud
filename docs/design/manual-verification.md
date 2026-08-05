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
- `invoke` — not yet run for real (see coverage table). Running it for real
  needs a second compartment to move a resource into/out of (e.g.
  `ChangeInstanceCompartment`) — the trial tenancy this harness otherwise uses
  has none (root compartment only).
- `destroy` — `TestManualRealOCIDestroy` now exists (calls `Destroy()` itself,
  not `Execute()` — see the coverage row) but has not yet been run for real.
- `execute` is exercised (Create/Update/Delete), but only ever run against an
  empty, personal/non-production trial tenancy, and the resources were torn
  down again afterward — never run against the paynance production tenancy
  used for the read-only ops above, and no resource from this harness is left
  running in either tenancy.

Both of the above are real, separate verification work — see
[`relay-requirements.md`](relay-requirements.md) for the relay-side gaps.

## Coverage

| Op | Kind(s) verified | Result | Notes |
|---|---|---|---|
| `describe` | n/a (pure, no OCI call) | **verified** | Returns the module manifest; `required_capabilities.egress_hosts` is `["*.oraclecloud.com"]` — see the realm caveat below. |
| `observe` | `cic:network:vcn`, `cic:network:subnet` | **verified** | Real signed GET against a live tenancy; `state` is confirmed to be OCI's raw response verbatim, `effective_config` the filtered/derived config-surface projection. |
| `validate` | `cic:network:subnet` | **verified** | Fed a real resource's own `effective_config` back as `intent` — `admissible: true`, both `envelope.well-formed` and `schema-conformance` checked. |
| `plan` | `cic:network:subnet` | **verified** | Two cases: `desired == observed` → `noop`; one `mutable` field changed (`displayName`) → `update` with a concrete `UpdateSubnet` (`PUT /subnets/{subnetId}`) provider operation. |
| `poll` | — (Work Request, kind-agnostic) | **verified** | `TestManualRealOCIPoll` run against a live `LaunchInstance` Work Request, 2026-08-04: `work_status: SUCCEEDED`, `terminal: true`. Found and fixed a real bug on this first run — `percent_complete` was always `0` (OCI sends `percentComplete` as a JSON float; the struct field was an `int`, so `encoding/json` silently zeroed it and the decode error was discarded) — see commit `4e577c8`. Fixed and re-verified: `percent_complete: 100` alongside `SUCCEEDED`. |
| `execute` (Create) | `cic:network:vcn`, `cic:network:subnet` | **verified** | `CreateVcn` then `CreateSubnet` (inside that VCN) against an empty personal trial tenancy (commercial realm) — both `http_status: 200`, synchronous, no work request. Each resource independently confirmed via `oci network vcn/subnet get`. |
| `execute` (Update) | `cic:network:vcn` | **verified** | `UpdateVcn` (`displayName` change) on the VCN just created — `http_status: 200`, new `etag`. **Gotcha**: the first re-`Observe` after this appeared to show the *old* value — this was Go's test-result cache silently replaying the previous identical invocation, not a real failure; `oci network vcn get` and a `-count=1` re-run both confirmed the update took effect immediately. Always pass `-count=1`. |
| `execute` (Delete) | `cic:network:vcn`, `cic:network:subnet` | **verified** | `DeleteSubnet` then `DeleteVcn` (order matters — a VCN can't be deleted while a subnet is attached) — both `http_status: 204`, synchronous. Confirmed gone via a 404 on `oci network subnet get` and an empty `oci network vcn list` afterward. Tenancy is empty again. |
| `destroy` | — | **not run** | `TestManualRealOCIDestroy` now exists (`module/manual_real_oci_test.go`) and calls `Destroy()` directly — the codepath every prior delete test bypassed by going through `Execute(OCI_EXEC_OPERATION=Delete…)` instead. Unit/fixture-level only so far; env-guard confirmed to skip cleanly with no network call when `REAL_OCI_TEST` is unset. Not yet run against real OCI — see `output/orchestrator-verification.md` (cic-factory job `oci-instance-lifecycle-coverage`) for the exact run recipe. |
| `invoke` | — | **not run** | `TestManualRealOCIInvoke` now exists. Only tried on resource kinds (`Vcn`, `Subnet`) that have no `action-managed` fields wired to a real action in prior test sessions; `cic:compute:instance` adds one (`ChangeInstanceCompartment`), but running it needs a second compartment the trial tenancy does not have. Env-guard confirmed to skip cleanly with no network call. |
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
