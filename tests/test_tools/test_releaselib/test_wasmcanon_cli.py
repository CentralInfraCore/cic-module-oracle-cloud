"""The producer's access to the relay's wasm-canon/v1 command (O-1 INT-04): a fake command stands in for the binary."""

import pathlib
import stat

import pytest

from tools.releaselib import wasmcanon_cli as wc
from tools.releaselib.envelope import EnvelopeError

RAW = b"\x00asm\x01\x00\x00\x00\x00\x0a\x08cic.test\xaa"
EXEC = b"\x00asm\x01\x00\x00\x00"


def fake(
    tmp_path,
    version,
    exec_script="cat >/dev/null; printf '\\000asm\\001\\000\\000\\000'",
    exec_rc=0,
):
    p = tmp_path / "wasmcanon"
    p.write_text(
        "#!/bin/sh\n"
        f'if [ "$1" = version ]; then echo "{version}"; exit 0; fi\n'
        f'if [ "$1" = exec ]; then {exec_script}; exit {exec_rc}; fi\n'
        "exit 2\n"
    )
    p.chmod(p.stat().st_mode | stat.S_IEXEC)
    return str(p)


def test_returns_what_the_pinned_command_writes(tmp_path):
    assert wc.executed_wasm(RAW, fake(tmp_path, wc.EXPECTED_VERSION)) == EXEC


def test_refuses_a_command_that_is_not_the_pinned_transformation(tmp_path):
    for version in (
        "wasm-canon/v1 wabin=v0.0.0-other",
        "wasm-canon/v2 wabin=" + wc.PINNED_WABIN,
        "",
    ):
        with pytest.raises(EnvelopeError) as exc:
            wc.executed_wasm(RAW, fake(tmp_path, version))
        assert exc.value.code == "WASMCANON_ERROR"
        assert "not the pinned transformation" in str(exc.value)


def test_a_refused_module_is_a_named_error(tmp_path):
    cli = fake(
        tmp_path,
        wc.EXPECTED_VERSION,
        exec_script="cat >/dev/null; echo 'failed to decode' >&2",
        exec_rc=1,
    )
    with pytest.raises(EnvelopeError) as exc:
        wc.executed_wasm(RAW, cli)
    assert exc.value.code == "WASMCANON_ERROR" and "failed to decode" in str(exc.value)


def test_a_missing_command_is_a_named_error(tmp_path):
    with pytest.raises(EnvelopeError) as exc:
        wc.executed_wasm(RAW, str(tmp_path / "absent"))
    assert exc.value.code == "WASMCANON_ERROR"


def test_the_producer_has_no_second_implementation():
    src = pathlib.Path(wc.__file__).read_text()
    assert "CustomSections" not in src and "custom section" not in src.split('"""')[2]
