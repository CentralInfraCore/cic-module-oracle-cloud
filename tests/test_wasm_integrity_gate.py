"""Negative controls for the committed-artifact gate (issue #37).

tools/wasm_integrity_gate.sh is the exact code `make wasm.integrity-verify` runs. A
gate is only worth having if it can fail, so these tests build a minimal checkout in a
temporary directory and check that it fails where it must.

The important case is the one the manifest cannot see: the manifest is consistent with
the files as they are, but the committed module.wasm no longer matches the buildHash
that project.yaml declares for it.
"""

import hashlib
import subprocess
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GATE = REPO / "tools" / "wasm_integrity_gate.sh"

COMMITTED_WASM = b"\x00asm\x01\x00\x00\x00 the committed artifact"


def _sha(data):
    return hashlib.sha256(data).hexdigest()


def _checkout(root, wasm=COMMITTED_WASM, declared=None, project_text=None):
    """A minimal checkout whose MANIFEST.sha256 matches the files as written."""
    (root / "module").mkdir()
    (root / "module" / "module.wasm").write_bytes(wasm)
    if declared is None:
        declared = _sha(COMMITTED_WASM)
    if project_text is None:
        project_text = "metadata:\n  buildHash: %s\n" % declared
    (root / "project.yaml").write_text(project_text)
    manifest = "%s  module/module.wasm\n%s  project.yaml\n" % (
        _sha((root / "module" / "module.wasm").read_bytes()),
        _sha((root / "project.yaml").read_bytes()),
    )
    (root / "MANIFEST.sha256").write_text(manifest)


def _gate(root, *args):
    argv = args or ("module/module.wasm", "project.yaml")
    return subprocess.run(
        ["sh", str(GATE), *argv], cwd=root, capture_output=True, text=True
    )


def _manifest_verifies(root):
    done = subprocess.run(
        ["sha256sum", "-c", "MANIFEST.sha256"], cwd=root, capture_output=True
    )
    return done.returncode == 0


def test_a_consistent_checkout_passes(tmp_path):
    _checkout(tmp_path)
    done = _gate(tmp_path)
    assert done.returncode == 0, done.stderr
    assert "OK: artifact integrity verified" in done.stdout


def test_new_wasm_with_a_consistent_manifest_but_a_stale_build_hash_fails(tmp_path):
    # The negative control the manifest cannot give: the artifact changed, the
    # manifest was regenerated for it, but project.yaml still declares the old hash.
    _checkout(tmp_path, wasm=COMMITTED_WASM + b"\x00", declared=_sha(COMMITTED_WASM))
    assert _manifest_verifies(tmp_path), "the manifest must be consistent here"
    done = _gate(tmp_path)
    assert done.returncode == 1
    assert "does not match" in done.stderr


def test_a_changed_declared_hash_with_a_consistent_manifest_fails(tmp_path):
    _checkout(tmp_path, declared="0" * 64)
    assert _manifest_verifies(tmp_path)
    done = _gate(tmp_path)
    assert done.returncode == 1
    assert "does not match" in done.stderr


def test_a_missing_declared_hash_fails(tmp_path):
    _checkout(tmp_path, project_text="metadata:\n  name: x\n")
    assert _gate(tmp_path).returncode == 1


def test_an_empty_declared_hash_fails(tmp_path):
    _checkout(tmp_path, project_text="metadata:\n  buildHash:\n")
    assert _gate(tmp_path).returncode == 1


def test_two_declared_hashes_fail(tmp_path):
    good = _sha(COMMITTED_WASM)
    text = "metadata:\n  buildHash: %s\nother:\n  buildHash: %s\n" % (good, good)
    _checkout(tmp_path, project_text=text)
    assert _gate(tmp_path).returncode == 1


def test_a_missing_wasm_fails(tmp_path):
    _checkout(tmp_path)
    (tmp_path / "module" / "module.wasm").unlink()
    done = _gate(tmp_path)
    assert done.returncode == 1
    assert "not found" in done.stderr


def test_a_missing_project_yaml_fails(tmp_path):
    _checkout(tmp_path)
    (tmp_path / "project.yaml").unlink()
    assert _gate(tmp_path).returncode == 1


def test_usage_error_is_not_a_pass(tmp_path):
    _checkout(tmp_path)
    assert _gate(tmp_path, "module/module.wasm").returncode == 2


def test_the_gate_changes_nothing(tmp_path):
    # It checks the files as they are; it must never rebuild or rewrite anything.
    _checkout(tmp_path)
    before = {p.name: p.read_bytes() for p in tmp_path.rglob("*") if p.is_file()}
    _gate(tmp_path)
    after = {p.name: p.read_bytes() for p in tmp_path.rglob("*") if p.is_file()}
    assert before == after
