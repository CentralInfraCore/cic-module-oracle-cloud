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
- `invoke` — not yet run for real (see coverage table).
- `execute`/`destroy` are now exercised (Create/Update/Delete), but only ever
  run against an empty, personal/non-production trial tenancy, and the
  resources were torn down again afterward — never run against the paynance
  production tenancy used for the read-only ops above, and no resource from
  this harness is left running in either tenancy.

Both of the above are real, separate verification work — see
[`relay-requirements.md`](relay-requirements.md) for the relay-side gaps.

## Coverage

| Op | Kind(s) verified | Result | Notes |
|---|---|---|---|
| `describe` | n/a (pure, no OCI call) | **verified** | Returns the module manifest; `required_capabilities.egress_hosts` is `["*.oraclecloud.com"]` — see the realm caveat below. |
| `observe` | `cic:network:vcn`, `cic:network:subnet` | **verified** | Real signed GET against a live tenancy; `state` is confirmed to be OCI's raw response verbatim, `effective_config` the filtered/derived config-surface projection. |
| `validate` | `cic:network:subnet` | **verified** | Fed a real resource's own `effective_config` back as `intent` — `admissible: true`, both `envelope.well-formed` and `schema-conformance` checked. |
| `plan` | `cic:network:subnet` | **verified** | Two cases: `desired == observed` → `noop`; one `mutable` field changed (`displayName`) → `update` with a concrete `UpdateSubnet` (`PUT /subnets/{subnetId}`) provider operation. |
| `poll` | — | **not run** | `TestManualRealOCIPoll` now exists (env: `OCI_POLL_PATH`, see Usage below) but has not been run for real. Still needs a real, still-live OCI Work Request path (from an async mutation's `opc-work-request-id`). None found in the paynance tenancy (`oci work-requests work-request list` / `oci ce work-request list` at the tenancy root both empty — infra predates the retention window). `CreateVcn`/`UpdateVcn` in the trial tenancy both turned out to be **synchronous** (no `opc-work-request-id`); `LaunchInstance`/`TerminateInstance` do give a real Work Request (measured, `work_request_id` present) — an instance create/terminate is the concrete way to get a live path to poll. |
| `execute` (Create) | `cic:network:vcn`, `cic:network:subnet` | **verified** | `CreateVcn` then `CreateSubnet` (inside that VCN) against an empty personal trial tenancy (commercial realm) — both `http_status: 200`, synchronous, no work request. Each resource independently confirmed via `oci network vcn/subnet get`. |
| `execute` (Update) | `cic:network:vcn` | **verified** | `UpdateVcn` (`displayName` change) on the VCN just created — `http_status: 200`, new `etag`. **Gotcha**: the first re-`Observe` after this appeared to show the *old* value — this was Go's test-result cache silently replaying the previous identical invocation, not a real failure; `oci network vcn get` and a `-count=1` re-run both confirmed the update took effect immediately. Always pass `-count=1`. |
| `execute` (Delete) | `cic:network:vcn`, `cic:network:subnet` | **verified** | `DeleteSubnet` then `DeleteVcn` (order matters — a VCN can't be deleted while a subnet is attached) — both `http_status: 204`, synchronous. Confirmed gone via a 404 on `oci network subnet get` and an empty `oci network vcn list` afterward. Tenancy is empty again. |
| `invoke` | — | **not run** | Only tried on resource kinds (`Vcn`, `Subnet`) that have no `action-managed` fields wired to a real action in this test session. |

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

Or via the Makefile wrapper (same env vars, exported before the call):
`make golang.test.manual-real-oci` — read-only ops only, because it runs
`-run "TestManualRealOCI(Observe|Validate|Plan|Poll)$"`.

**Do not rely on a bare `-run TestManualRealOCI` to stay read-only.** Go's
`-run` pattern is an unanchored regexp, so `TestManualRealOCI` *does* match
`TestManualRealOCIExecute` — this file previously claimed the opposite, and the
claim was wrong. What actually keeps a bare run from mutating anything is the
`OCI_EXEC_*` env guard inside the test: with those unset the Execute test fails
without issuing a request. If they happen to be set in your shell, a bare
`-run TestManualRealOCI` **will** mutate. Anchor the pattern, as the Makefile
target now does.

The harness never prints or persists the private key material — only its file
path is passed in; the parsed key lives in test-process memory for the
duration of the run.
