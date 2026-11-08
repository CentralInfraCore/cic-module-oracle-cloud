# Target data: where it comes from, and how a path is built (D5)

Status: **Part A** (section 1) is a measurement of the module at `2ac19ec`. **Part B** (sections 3
and 4) is a proposal (**concept**): nothing in it is implemented in the module or the Relay.

D5 is split in two on purpose. Part A records what the module does today with the target values
a caller gives it, and what follows. Part B is the contract the architecture asks for, and it is
**not** an input validator: it asks why a caller gives these values at all.

**Premise of Part B.** The `host`, `base_path`, `key_id`, `revision` and the resource's OCID are not
part of the domain intent. They belong to the realization (deployment) context, the provider
contract and the observed state, and the CIC derives them from authenticated schemas and context.
A provider module is not meant to be the source of truth for them. This premise is the project
owner's statement of the CIC model; it is not recorded elsewhere in this repository (searched:
`docs/`, `theads/`), so this document is its first written form here and it is open to correction.

Why it matters: `target_digest` (`plan-digest.md`, `destroy-invoke-binding.md`) binds the five
values to a plan or an action. It does not show that they are the right ones. The M4a gate proves a
plan and a target belong together; Part B asks why this plan and this target follow from the
authenticated state and the approved intent.

## 1. Part A: measured current behaviour

### 1.1 Method

Scratch probe in a copy made with `git archive` of `2ac19ec`; the host seam is the module's own
test mock; no provider and no Relay. Sources, output and exact environment:
[`../measurements/target-and-path/README.md`](../measurements/target-and-path/README.md). The
Relay's egress rule was **read** in CIC-Relay at `55a899f` (cited there) and **transcribed** into
the probe; the Relay was not run, so "egress allowed or denied" below is that transcription's
verdict, not a measurement of the Relay.

### 1.2 What the code does (read)

- Four entry points (`observe`, `execute`, `destroy`, `invoke`) build the request path with one
  function, `opPath` = `binding.base_path` + the registry path with `{param}` replaced by
  `binding.resource_id`. `poll` has its own strict builder (`/` and 8 digits).
- The request URL is the string `"https://" + host + path`. The string handed to `sign` is built
  from the same `host` and `path` as two separate raw strings (`(request-target)` and `host` lines).
- The module checks that `host` and `key_id` are present, and the resource id's syntax. It checks
  nothing about the structure of `host` or `base_path`.
- The module's embedded registry carries, per operation, only `method`, `path`, `path_params`,
  `role` and `input` (inspected in `module/schemas/core/*.json`). It carries **no API version, no
  base path and no host**; the schema extractor (`tools/oci-extract`) does not record one. The
  version `/20160918` appears only in callers' bindings, tests and the manual harness.
- The module's capability manifest (`project.yaml`) declares one egress rule: host
  `*.oraclecloud.com`, five methods, no ports (so 443 only), no paths (so any path).
- The Relay's `actuate` (read) parses the URL string with `url.Parse`, applies the egress rule to
  `u.Hostname()`, the port and `u.Path`, and sends the request to that URL. Its path rule is a prefix
  match on `u.Path`, and an empty list allows any path.

### 1.3 Observations

**H1. `host` is used as the caller wrote it** (module: `observe`, GET, the same `base_path` and
`resource_id`; the right column is the transcribed Relay rule applied to the URL the module sent).

| `host` given | The URL's parsed host, port, path | Egress by the transcribed rule |
|---|---|---|
| `iaas.eu-frankfurt-1.oraclecloud.com` | same host, 443 | allowed |
| `...oraclecloud.com:8443` | port 8443 | denied (443 only) |
| `iaas.eu-frankfurt-1.oraclecloud.com@evil.example.com` | host `evil.example.com`, userinfo = the OCI name | denied |
| `evil.example.com#.oraclecloud.com` | host `evil.example.com`; the intended path became the fragment | denied |
| `evil.example.com?x=.oraclecloud.com` | host `evil.example.com`; the intended path became the query | denied |
| `evil.example.com/x.oraclecloud.com` | host `evil.example.com`; the rest became the path | denied |
| a backslash after `evil.example.com` | not a valid URL | refused |
| `IAAS.EU-FRANKFURT-1.ORACLECLOUD.COM` | upper case kept | allowed |
| `iaas.eu-frankfurt-1.oraclecloud.com.` (trailing dot) | host keeps the dot | denied |
| `anything.oraclecloud.com`, `objectstorage.us-ashburn-1.oraclecloud.com` | as given | **allowed** |
| `evil-oraclecloud.com` | as given | denied |
| `http://iaas.eu-frankfurt-1.oraclecloud.com` | host `http`, path starts `//iaas...` | denied |
| a host containing a space | not a valid URL | refused |
| `[::1]` | host `::1` | denied |

In every row the module asked `sign` to sign a string whose `host:` line is the raw value, even when
the URL's real host is another one (rows 3 to 6): the signed host line and the URL's host differ.
What stops those requests, in the transcribed rule, is the Relay's check of the parsed host, not
the module.

**H2. `base_path` is put in front of the path as the caller wrote it** (`host` normal).

| `base_path` given | URL sent | The URL parsed as the Relay parses it | Egress |
|---|---|---|---|
| `/20160918` | `/20160918/vcns/<id>` | path as intended | allowed |
| empty | `/vcns/<id>` (no version at all) | path as sent | allowed |
| `20160918` (no leading slash) | `https://iaas...com20160918/vcns/<id>` | host becomes `iaas...com20160918` | denied |
| `/20160918/` | `/20160918//vcns/<id>` | double slash kept | allowed |
| `/20160918/../other` | sent as written | path `/20160918/../other/vcns/<id>` (dot segment not resolved by the module) | allowed |
| `//evil.example.com` | `//evil.example.com/vcns/<id>` | path with a leading double slash, host unchanged | allowed |
| `/x?inject=1` | `/x?inject=1/vcns/<id>` | path `/x`, query `inject=1/vcns/<id>`: **the resource part is no longer a path** | allowed |
| `/x#frag` | `/x#frag/vcns/<id>` | path `/x`, the rest is a fragment | allowed |
| `/%2e%2e/other` | sent as written | `u.Path` is `/../other/vcns/<id>` (decoded); the module signed the encoded form | allowed |
| `/identity/20160918` | as written | another service's prefix | allowed |
| `/20160918 x` (a space) | as written | path with a space | allowed |
| a newline inside | URL has a newline | not a valid URL | refused |

For the newline case the module has, before `actuate`, asked `sign` to sign a string in which the
injected text is a line of its own (`(request-target): get /20160918` followed by a line `X: y/...`):
the sign call precedes the send in `actuateSigned`.

**H3. One builder, one surface.** For the same hostile binding (`base_path` `/x/../y?q=1`) `observe`,
`destroy`, `invoke` and `execute` produce the same URL string up to the operation's own tail;
`poll` by `work_request_id` sends nothing (it refuses). So every `base_path` property above holds
for four entry points, and a fix at `opPath` reaches all four.

### 1.4 Not measured

The real Relay, what OCI or a proxy does with dot segments, an encoded dot segment, a query where a
path was meant or a double slash, and what the Go HTTP client sends for a URL that carries a
fragment. Whether the signed string and the sent request-target agree in each row was not measured.

## 2. What the observations show

| Property | From |
|---|---|
| **Q1.** The module accepts the five target values from the caller without establishing where they come from, and nothing in the registry or manifest could tell it what they should be: no API version, base path or host is recorded. | 1.2, H1, H2 |
| **Q2.** The only structural control on a request's destination is the Relay's egress rule on the parsed host and port. Its host rule is a wildcard over all of `*.oraclecloud.com` (any OCI service and region, with the same key) and it has no path rule. | 1.2, H1, H2 |
| **Q3.** What the module signs and what leaves can differ in structure when a value carries URL syntax (a host that parses as another host, a `base_path` that turns the resource path into a query or fragment). | H1, H2 |
| **Q4.** Text that is not a URL path can reach the string that is signed (a newline adds a line to the signed string); the signature is requested before anything refuses it. | H2 |
| **Q5.** The same builder serves four entry points; poll is already strict. | H3 |

## 3. Part B: where the target data should come from (concept)

The values are not free parameters of a provider call. Each has an origin and an authenticator in the
CIC model; where the module can derive a value itself from what it already has, it should, and where
it cannot, it should receive it from an authenticated source and not as a parameter in the request.

| Field | Origin (premise) | Can the module derive it? | Authenticator |
|---|---|---|---|
| `host` | the realization environment the provider is assigned to (region, realm) | no: it needs the realization context | the Relay and the realization layer; the manifest allowlist is an upper bound, not the value |
| `base_path` | the provider's API contract (the service's API version) | **yes, if the contract records it**: today it does not (Q1). The schema extractor reads the SDK that sets it; recording it per service in the registry is the missing piece, **not verified here** that the SDK exposes it in a form the extractor can read | the registry digest (`contract_digest`, already in `plan_digest`) would then cover it |
| `key_id` | the Relay/CIC execution identity's binding to a tenant and principal | no | the Relay (R6, R7): the module knows a name only |
| `resource_id` | the checked binding of the CIC logical object to the provider resource | partly: syntax and type today; the binding itself comes from the state model (`observe` returns it in `provider_metadata`) | the state model and the observation |
| `revision` | the observation's provider state (an etag) | no | the observation, with its source and time |

**What the architecture asks of the contract.**

1. The provider module is not the source of truth for target data. A target that the caller supplied
   freely does not stand in for a derivation from schemas and context.
2. `target_digest` binds the **derived and checked** binding to the plan. It binds values; it is not
   the derivation. What binds the derivation is what it was derived from: the schemas' digests
   (already in `plan_digest`), the contract digest (already in `plan_digest`), and a digest of the
   realization context and of the observation the revision came from (**missing**).
3. A target arrives from an authenticated source (the execution context the Relay provides, R6),
   not as a free field of the request, so that the module can be given something it did not have to
   trust blindly. Until that exists the module's `target` input is exactly what it was in M4a: a
   caller-supplied value that the gate proves consistent.
4. The Relay enforces the authority to execute the approved consequence (R7, R8); the returned state
   and the ProofTrace prove separately what was observed and what happened (R9).

M4a, the canonical JSON, the three digests, the derive-again gate and the replay limit are building
blocks for this. They are not the realization proof: that proof is "why this plan and this target
follow from the authenticated system state and the approved intent", which M4a does not show.

## 4. Safe execution of a derived target (path building)

These are the details of executing a derived target safely, not the architecture's starting point,
and they do not replace section 3. They follow from H1 to H3 and Q3 to Q5 and are defense in depth
at the one place that builds the path:

- one builder for the path and one for the host, shared by the four entry points (H3);
- a structural check of `host` (a lower-case DNS name; no userinfo, port, scheme, `#`, `?`, `/`,
  backslash, whitespace or control character; no trailing dot) and of `base_path` (the form
  `poll` already requires: `/` and 8 digits, or derived from the registry once it is recorded);
- nothing that is not a path segment reaches the string that is signed, and the check runs before
  `sign` is asked (Q4);
- the Relay's egress rule narrowed from a wildcard host and an empty path list to what a deployment
  actually uses is the Relay's decision (R7 and the manifest), not this repository's.

## 5. What can be proved where

| Statement | Who can prove it |
|---|---|
| The path the module builds is a path under the API prefix, and no value carries URL syntax | the module (section 4) |
| The registry records the API version and a plan's digest covers it | the module, once the extractor records it |
| That `host` is the right region and realm, `key_id` the right tenant, `revision` current | not the module and not the digest: the realization context, the Relay and the observation (section 3) |
| That the signed request is the one that leaves | the Relay's host boundary (R7) |
| That anyone approved it, once, with what outcome | the Relay (R7, R8, R9) |

## 6. Open decisions

- **D5-1.** Record the service API version per service in the registry (extractor change), so `base_path`
  is derived and no longer given. Needs a look at how the SDK exposes it, not done here.
- **D5-2.** Where the realization context and its digest live, and what carries them into an
  execution (R6): the largest open design question; this document only names it.
- **D5-3.** Whether the module keeps a caller-supplied `target` field at all once an authenticated
  source exists, or reads it only from that source.
- **D5-4.** The structural checks of section 4: refuse (proposed) or normalize.
- **D5-5.** The `poll` alias's behaviour with an empty `base_path` (it takes the version from the
  caller today).

## What this does not claim

Nothing in Part B or section 4 is implemented. The measurements are of the module against a mock,
with the Relay's rule read and transcribed, not run; they do not show that any of the listed requests
would succeed against OCI, and they do not show that the Relay behaves as transcribed.
