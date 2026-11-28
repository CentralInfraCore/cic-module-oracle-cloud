"""Access to the relay's wasm-canon/v1 transformation for the release producer (O-1 INT-04).

The producer does NOT implement the transformation. ``executed_wasm_sha256`` is the digest of the bytes the relay
compiles, and those are derived from the raw wasm by the relay's pinned ``pkg/wasmcanon`` (wabin decode, drop custom
sections, encode). A second implementation here could drift from it, so the producer asks the same code through the thin
``wasmcanon`` command (CIC_Relay ``cmd/wasmcanon``): raw wasm on stdin, executed wasm on stdout.

The command is pinned two ways: its ``version`` output must name the transform and the pinned wabin, and the caller
supplies the path of a binary built from the pinned Relay commit (no lookup on PATH, no download).
"""

import subprocess

from .envelope import EnvelopeError

TRANSFORM_ID = "wasm-canon/v1"
PINNED_WABIN = "v0.0.0-20230304001439-f6f874872834"
EXPECTED_VERSION = f"{TRANSFORM_ID} wabin={PINNED_WABIN}"
_TIMEOUT_S = 60


def _run(cli: str, command: str, data: bytes = b""):
    try:
        return subprocess.run(
            [cli, command],
            input=data,
            capture_output=True,
            timeout=_TIMEOUT_S,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as exc:
        raise EnvelopeError(
            "WASMCANON_ERROR", f"cannot run the wasmcanon command: {exc}"
        )


def check_pinned(cli: str) -> None:
    """Refuse a command that is not the pinned transformation."""
    done = _run(cli, "version")
    got = done.stdout.decode("utf-8", "replace").strip()
    if done.returncode != 0 or got != EXPECTED_VERSION:
        raise EnvelopeError(
            "WASMCANON_ERROR",
            f"the wasmcanon command is not the pinned transformation (got {got!r}, want {EXPECTED_VERSION!r})",
        )


def executed_wasm(raw_wasm: bytes, cli: str) -> bytes:
    """The executed representation of ``raw_wasm``, from the pinned relay transformation."""
    check_pinned(cli)
    done = _run(cli, "exec", raw_wasm)
    if done.returncode != 0:
        msg = done.stderr.decode("utf-8", "replace").strip()
        raise EnvelopeError("WASMCANON_ERROR", f"the module is refused: {msg}")
    return done.stdout
