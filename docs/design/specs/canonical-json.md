# cic-canonical-json/1

The one definition this module uses for "these two JSON texts are the same value".
It decides whether a plan has any work to do (`plan`), and is the form the digests of a
plan are computed over. Go implements it (`module/canonical_json.go`); the
**normative expectations are the language-neutral vectors** in
`module/testdata/canonical_json_vectors.json`, so another implementation can be
checked against the same file.

Status: **implemented**. `plan` decides with it, and the plan, target and execution
digests are computed over it (`plan-digest.md`).

## Why not the standard library

`encoding/json` reads every number as a `float64`, so two different integers above
2^53 compare equal (and `0.1` equals `0.10000000000000001`); it keeps only the last of
a repeated object key; and it turns an invalid UTF-8 byte or a lone surrogate escape
into U+FFFD, so distinct texts can collapse into one. `plan` used that comparison to
call two different values a `noop`.

## The form

The input is one strict JSON text (RFC 8259). The output is deterministic:

- **Whitespace** is dropped (only space, tab, CR and LF are whitespace on input).
- **Objects**: members sorted by the **decoded key's UTF-8 bytes**; the keys are
  compared after escapes are decoded (`"a"` is `"a"`). A **repeated key is an
  error**, never merged.
- **Arrays** keep their order.
- **Numbers** are exact, with no floating point: the value is written as
  `<digits>e<exponent>`, where `<digits>` are the significant digits (no leading and
  no trailing zeros) and the exponent is the power of ten that scales them, with a
  `-` in front for a negative value. `1`, `1.0`, `1e0` and `10e-1` are all `1e0`;
  `100` and `1e2` are `1e2`; `0.5` is `5e-1`; zero in any spelling (`-0`, `0.0`,
  `0e5`) is `0`. `9007199254740993` stays `9007199254740993e0` and differs from
  `9007199254740992e0`.
- **Strings** are decoded (all escapes, surrogate pairs combined) and written with
  only the quote, the backslash and the characters below U+0020 escaped, the latter
  as `\u00xx` in lower case. Every other character is written as it is, with no HTML
  escaping. A lone surrogate escape is an error.
- Literals `true`, `false`, `null` as written.

## What is refused (an error, never a repair)

Not valid UTF-8 (a byte order mark included); a value followed by anything but
whitespace; a leading zero, a `+`, a bare `.5` or `1.`, `NaN` or `Infinity`; trailing
commas, comments, single quotes, unquoted keys; a raw control character in a string;
a bad or short escape; a lone or mismatched surrogate escape; a repeated object key;
empty or whitespace-only input.

## Bounds

An input beyond a bound is refused, not truncated:

| Bound | Limit |
|---|---|
| input text | 1 MiB (2^20 bytes) |
| nesting of arrays and objects | 64 |
| digits in a number's integer and fraction parts | 1000 |
| digits in a number's written exponent | 6 |
| exponent after normalization | -10000 to 10000 |

An error names the byte offset and the reason, never the offending text.

## Where it applies today

`plan` refuses `desired.data` and `observed.data` that are not canonicalizable
(`validation`, `field_path: desired.data` / `observed.data`), and compares values
(whole payloads and field by field) by their canonical form. `validate` and the other
operations are unchanged.

## What this is not

Not JCS (RFC 8785): that requires ES6 floating-point number formatting, which is
exactly the loss this form exists to avoid. Not a signature or an approval: two texts
with the same canonical form are the same JSON value, and nothing more.
