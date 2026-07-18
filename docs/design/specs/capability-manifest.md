# Spec — capability manifest

**Piece:** P0.2, P1.3 · **Status:** spec

The security model for third-party modules. A module **declares** the narrow set
of capabilities it needs; the host **enforces** exactly that set. Nothing the
module does can exceed its declaration, and the declaration is reviewable before
the module is trusted.

## Why declaration, not trust

The sandbox already prevents ambient I/O. The manifest goes further: it makes a
module's reach **explicit and bounded**. An OCI module that declares egress to
`*.oraclecloud.com` and access to `oci/*` secrets can reach nothing else — not
another cloud, not another tenant's secret, not an internal endpoint. A reviewer
approves the *declaration*, not the code.

## Shape

Declared in `project.yaml` (part of the signed release) and mirrored in the
module manifest returned by `describe`:

```yaml
capability_manifest:
  version: v0.1.0

  # Outbound HTTP the host will permit, enforced by http-executor's EgressPolicy.
  egress:
    - host: "*.oraclecloud.com"
      methods: [GET, POST, PUT, DELETE, PATCH]
      # optional: paths, ports; default 443
    - host: "objectstorage.*.oraclecloud.com"
      methods: [GET, PUT]

  # Secret handles the module may sign/read through — never the secret itself.
  secrets:
    - handle: "oci/*"
      ops: [sign]           # sign only; the private key stays in Vault
    # ops: [read] would allow reading a KV value through the handle

  # Host functions the module imports (must match abi.imports).
  host_functions:
    - http.request
    - vault.sign

  # Optional resource limits (host-enforced; defaults apply if omitted).
  limits:
    max_request_bytes: 1048576
    max_response_bytes: 8388608
    wall_clock: 60s
```

## Enforcement (P1.3, host side)

At module load the host:

1. Reads `capability_manifest` from the verified, signed release.
2. Cross-checks it against `abi.imports` — a module importing `vault.sign` but
   not declaring a `secrets` entry with `sign` is **rejected at load**, not at
   call time.
3. Configures the per-module `http-executor` `EgressPolicy` from `egress` and
   the `vault-adapter` scope from `secrets`.
4. On every host call, enforces the scope: a `http.request` to an undeclared
   host, or a `vault.sign` on an undeclared handle, fails with
   `provider-error{class: permission}` and is recorded in the ProofTrace as a
   denied attempt.

The module cannot widen its own scope: the manifest is part of the signed
artifact, and the host derives the enforced policy solely from it.

## Secret handles

The module never receives a secret value. It receives a **handle**:

```yaml
credential:
  secret_handle: "cic-secret://oci/prod-tenancy"
```

`vault.sign(handle, bytes)` asks the host to sign `bytes` with the key behind the
handle; the signature returns, the key does not. For OCI this is exactly what
request signing needs — the module builds the canonical signing string, the host
signs it via `vault-adapter` transit, the module assembles the `Authorization`
header. See [host-functions.md](host-functions.md).

## Review flow (P5.2, informative)

A third-party submission is approved on three independent axes:

1. **Signature** — the artifact is signed; provenance is provable.
2. **Manifest** — a human/policy reviews the declared `egress` + `secrets`. This
   is the small, auditable surface: not the code, the reach.
3. **Behaviour** — the ProofTrace of a test run shows the module stayed within
   its declaration.

A module whose manifest is too broad (`egress: "*"`) is rejected at review,
before it ever runs.
