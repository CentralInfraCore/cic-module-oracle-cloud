# Manual verification against a real OCI tenancy

`module/manual_real_oci_test.go` is an opt-in harness that runs the module's
own handler functions (`Describe`/`Observe`/`Validate`/`Plan`) directly against
a real OCI tenancy, using the real OCI API-key signing scheme. It exists
because the module's other tests (`relay_integration_test.go`,
`module_loadtest_test.go`) prove the WASM/ABI/signing-format plumbing against a
**mock** OCI server and a **local** RSA key standing in for Vault — they never
prove the module's request/response logic against **real** OCI. This harness
fills that specific gap, cheaply, without Terraform/Vault/a running relay.

**Status legend:** `verified` — run against real OCI, passed · `not run` —
possible but not yet tried · `blocked` — tried, needs something this harness
doesn't provide.

## What it does NOT cover

- The real `module.wasm` / wazero / ABI-dispatch path (`abi.go`) — this harness
  calls the Go handler functions directly, under the host (`!wasip1`) build.
- The relay's real `cic-flow` host functions (Vault Transit signing, egress
  policy, capability-manifest enforcement) — `testCallHostSign`/
  `testCallHostActuate` substitute a local RSA key and a plain `net/http` call.
- Mutating ops (`execute`, `invoke`, `destroy`) — read-only by design; running
  these for real would provision/change/delete real OCI resources.

Both of the above are real, separate verification work — see
[`relay-requirements.md`](relay-requirements.md) for the relay-side gaps.

## Coverage

| Op | Kind(s) verified | Result | Notes |
|---|---|---|---|
| `describe` | n/a (pure, no OCI call) | **verified** | Returns the module manifest; `required_capabilities.egress_hosts` is `["*.oraclecloud.com"]` — see the realm caveat below. |
| `observe` | `cic:network:vcn`, `cic:network:subnet` | **verified** | Real signed GET against a live tenancy; `state` is confirmed to be OCI's raw response verbatim, `effective_config` the filtered/derived config-surface projection. |
| `validate` | `cic:network:subnet` | **verified** | Fed a real resource's own `effective_config` back as `intent` — `admissible: true`, both `envelope.well-formed` and `schema-conformance` checked. |
| `plan` | `cic:network:subnet` | **verified** | Two cases: `desired == observed` → `noop`; one `mutable` field changed (`displayName`) → `update` with a concrete `UpdateSubnet` (`PUT /subnets/{subnetId}`) provider operation. |
| `poll` | — | **blocked** | Needs a real, still-live OCI Work Request path (from an async mutation's `opc-work-request-id`). None found in this tenancy — `oci work-requests work-request list` and `oci ce work-request list` at the tenancy root both returned empty (this tenancy's infra predates the retention window). Needs either a fresh async mutation to poll immediately after, or a compartment-scoped search that hasn't been tried yet. |
| `execute` | — | **not run** | Would actually mutate real OCI. Do not run without explicit sign-off on which resource, which (non-prod) compartment, and what change. |
| `invoke` | — | **not run** | Same caveat as `execute`. |
| `destroy` | — | **not run** | Same caveat as `execute` — deletes a real resource. |

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

```bash
OCI_KEY_PATH=~/.oci/oci_api_key.pem \
OCI_TENANCY_OCID=ocid1.tenancy.oc19..xxx OCI_USER_OCID=ocid1.user.oc19..xxx \
OCI_FINGERPRINT=xx:xx:xx:...             OCI_REGION=eu-frankfurt-2 \
OCI_TEST_KIND=cic:network:subnet         OCI_TEST_RESOURCE_ID=ocid1.subnet.oc19...\
REAL_OCI_TEST=1 go test -tags manual_real_oci ./module/ -run TestManualRealOCI -v
```

Or via the Makefile wrapper (same env vars, exported before the call):
`make golang.test.manual-real-oci`.

The harness never prints or persists the private key material — only its file
path is passed in; the parsed key lives in test-process memory for the
duration of the run.
