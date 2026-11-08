# Target data and path building: measurement of the current behaviour

The probe and its output behind section 1 of
[`../../specs/target-origin-and-path-safety.md`](../../specs/target-origin-and-path-safety.md). It observes
what the module builds from a caller-given `host` and `base_path`, and what the CIC-Relay's
egress rule, as read in its source, makes of that. Kept unchanged so the tables can be checked.

## Environment of the recorded run

| | |
|---|---|
| Module commit | `2ac19ecade7131514a3103f20ab0d38cccf41521` (`devel`), taken with `git archive` into an empty directory, no working-tree residue |
| Go | go1.25.0 linux/amd64 (the repo's builder image `cic-module-oracle-cloud-builder:latest`, id `sha256:b5a3b64b391e6d4a1254b41dfa59f78025a603cfccdaacf902c408967047debf`) |
| Host | Linux 7.0.0-43-generic |
| Provider, Relay | neither is involved. The host seam is the module's own test mock (`cicFlowMock`): `sign` returns a fixed signature, `actuate` answers 200 |
| The Relay's rule | CIC-Relay at commit `55a899f`, **read, not run**: `cmd/relay/cic_flow_manifest.go` (`egressPolicy.allows` :73, `hostGlobMatch` :89, `portAllowed` :114, `pathAllowed` :126) and `cmd/relay/cic_flow_host_funcs.go` (`actuate`: `url.Parse` :153, the egress check on `u.Hostname()`, the port and `u.Path` :161). The probe contains a **transcription** of those rules (`relayHostGlob`, `relayPort`, `relayView`) and applies it to the URL string the module sent. It is not the Relay's code and the Relay's real behaviour was not measured |
| The module's manifest | `project.yaml` `capability_manifest.egress`: one rule, host `*.oraclecloud.com`, methods GET, POST, PUT, DELETE, PATCH, no ports (so 443 only), no paths (so any path) |

## Files

- `probe_test.go.txt` - `TestZZD5Host`, `TestZZD5BasePath`, `TestZZD5SamePathEverywhere`. A backslash in
  a probe input is written `"\x5c"`, so no tool can rewrite it.
- `output.txt` - the output of the recorded run, verbatim. For each case: the URL the module handed to
  `actuate`; the `(request-target)` and `host` lines of the string the module asked to have signed
  (`ociCanonical`); and what the transcribed Relay rule makes of the URL (`hostname`, `port`, `path`,
  `rawquery`, `fragment`, `user`, `egress`).

The files end in `.txt` so that they are not compiled into the module.

## Rerun

In a scratch copy of the commit above, not in the repository:

```
cp probe_test.go.txt <scratch>/module/zz_d5_probe_test.go
cd <scratch>/module
go test -count=1 -run 'TestZZD5' -v .
```

It uses helpers of the module's test package (`cicFlowMock`, `decodeResult`, `observeRequest`,
`destroyRequest`, `invokeRequest`, `pollRequest`, `m4aForOp`, `ociCanonical`, `opPath`).

## Limits

The module against a mock, and a transcription of a rule. Not measured: the real Relay, what OCI
does with any of these URLs, what a provider or a proxy does with dot segments or an encoded dot
segment, and what the Go HTTP client sends for a URL that carries a fragment.
