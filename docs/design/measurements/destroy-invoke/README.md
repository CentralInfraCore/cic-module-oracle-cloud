# Destroy and Invoke: measurement of the current behaviour

What this is: the scratch probes and their output behind the tables in
[`../../specs/destroy-invoke-binding.md`](../../specs/destroy-invoke-binding.md), section 1. They
observe what `Destroy` and `Invoke` are given and what they sign and send. They are kept here
unchanged so that the tables can be checked, not taken on trust.

## Environment of the recorded run

| | |
|---|---|
| Module commit measured | `eda4aebbe65bdfcf8e1d5a1a37db9df3d943013a` (`devel`), taken with `git archive` into an empty directory, so no working-tree residue |
| Production code vs the earlier run | the same run was first made on `f48246e`; `module/provider.go`, `canonical_json.go`, `plan_binding.go` and `abi.go` are identical in `eda4aeb` (empty `git diff`), and the output of the two runs is identical apart from timings |
| Go | go1.25.0 linux/amd64 (the repo's builder image) |
| TinyGo | 0.39.0 (not used by the probes) |
| Builder image | `cic-module-oracle-cloud-builder:latest`, id `sha256:b5a3b64b391e6d4a1254b41dfa59f78025a603cfccdaacf902c408967047debf` |
| Host | Linux 7.0.0-43-generic |
| Provider | none. The host seam is the module's own test mock (`cicFlowMock` in `module/cicflow_mock_test.go`): `sign` returns a fixed signature, `actuate` answers with a configured status and body. No OCI endpoint, credential or network is involved |

"sign" and "actuate" in the tables are the numbers of calls that reached that mock seam.

## Files

- `probe_test.go.txt` - `TestZZDestroy`, `TestZZInvoke`, `TestZZRenderBodyVsInvoke`: the
  dimension changes, the operation roles, the replay counts and the `renderBody` comparison.
- `raw_probe_test.go.txt` - `TestZZInvokeRawBodies`: the body fidelity of `Invoke`. The request
  TEXT is written out byte by byte (a backslash is `"\x5c"`) and handed to `Invoke` as it is, because
  a request built with `json.Marshal` rewrites the body text (it compacts whitespace and escapes
  `<`, `>`, `&`) and an editor or tool can rewrite an escape sequence typed into a source file;
  either would make the body measurement meaningless.
- `output.txt` - the output of the recorded run, verbatim (`DI` lines from the first file,
  `RAW` lines from the second).

The files end in `.txt` so that they are not compiled into the module.

## Rerun

From a clean checkout of the commit above, in a scratch copy (not in the repository):

```
cp probe_test.go.txt     <scratch>/module/zz_di_probe_test.go
cp raw_probe_test.go.txt <scratch>/module/zz_di_raw_test.go
cd <scratch>/module
go test -count=1 -run 'TestZZ' -v .
```

The two files belong together (the second uses `ok200` from the first). They use helpers of the
module's test package (`cicFlowMock`, `decodeResult`, `execBinding`, `destroyRequest`,
`invokeRequest`, `renderBody`), which exist at the commit above.

## Limits of this measurement

It measures the module against a mock host seam. It does not measure what the Relay does with the
calls that reach it, what OCI answers, or whether OCI accepts any request body; the real
provider was not involved.
