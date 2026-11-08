"""The order of the CI steps is part of what they prove (issue #37).

`make wasm.build` rebuilds module/module.wasm and rewrites project.yaml's buildHash in
the CI workspace. A check of the committed artifact that runs after it only compares
files that were just created together. These tests keep the committed-artifact gate
ahead of the build, exactly once, and keep the fresh-guest step in the workflow.
"""

from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent
WORKFLOW = REPO / ".github" / "workflows" / "ci.yml"


def _steps():
    data = yaml.safe_load(WORKFLOW.read_text())
    return data["jobs"]["lint_and_test"]["steps"]


def _positions(command):
    return [i for i, s in enumerate(_steps()) if command in (s.get("run") or "")]


def test_the_committed_artifact_is_checked_exactly_once_and_before_the_build():
    gate = _positions("make wasm.integrity-verify")
    build = _positions("make wasm.build")
    assert len(gate) == 1, "the integrity check must appear exactly once"
    assert len(build) == 1
    assert gate[0] < build[0], "the committed artifact must be checked before the build"


def test_no_integrity_check_runs_after_the_build():
    build = _positions("make wasm.build")[0]
    assert all(i < build for i in _positions("make wasm.integrity-verify"))


def test_the_manifest_is_checked_before_the_build_too():
    manifest = _positions("make manifest-verify")
    build = _positions("make wasm.build")
    assert len(manifest) == 1
    assert manifest[0] < build[0]


def test_the_fresh_guest_step_exists_exactly_once():
    assert len(_positions("make wasm.test-fresh")) == 1


def test_the_fresh_guest_is_built_before_the_in_place_build_mutates_files():
    fresh = _positions("make wasm.test-fresh")[0]
    build = _positions("make wasm.build")[0]
    assert fresh < build, "the scratch build must see the original checkout"
