"""Controls for the fresh-guest step (issue #37).

Two scripts are involved. tools/wasm_assert_tests_passed.sh decides, from a
`go test -v` log, whether the required real-WASM tests really PASSED (go test exits 0
on a skip, so the exit code alone is not evidence). tools/wasm_fresh_guest_test.sh builds
the guest to a scratch path; it must refuse any output path that could overwrite a
tracked file. Neither of these checks needs TinyGo.
"""

import subprocess
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
ASSERT = REPO / "tools" / "wasm_assert_tests_passed.sh"
FRESH = REPO / "tools" / "wasm_fresh_guest_test.sh"
MK = REPO / "mk" / "wasm.mk"

REQUIRED = (
    "TestHostLoadExecuteCreateCarriesOCID",
    "TestHostLoadExecuteCreateWithoutIDFails",
    "TestHostLoadExecuteCreateRefusesDuplicateID",
    "TestHostLoadExecuteUpdateHasNoResourceID",
)


def _log(tmp_path, lines):
    path = tmp_path / "go-test.log"
    path.write_text("\n".join(lines) + "\n")
    return path


def _assert(log, names):
    return subprocess.run(
        ["sh", str(ASSERT), str(log), *names], capture_output=True, text=True
    )


def _all(status):
    return ["--- %s: %s (0.10s)" % (status, name) for name in REQUIRED]


def test_all_required_tests_passing_is_accepted(tmp_path):
    done = _assert(
        _log(tmp_path, _all("PASS") + ["PASS", "ok  module  0.4s"]), REQUIRED
    )
    assert done.returncode == 0, done.stderr


def test_a_skipped_required_test_is_refused(tmp_path):
    lines = _all("PASS")
    lines[1] = "--- SKIP: %s (0.00s)" % REQUIRED[1]
    done = _assert(_log(tmp_path, lines + ["ok  module  0.4s"]), REQUIRED)
    assert done.returncode == 1
    assert "SKIP" in done.stderr and REQUIRED[1] in done.stderr


def test_all_four_skipped_is_refused_even_though_go_test_would_exit_zero(tmp_path):
    done = _assert(_log(tmp_path, _all("SKIP") + ["ok  module  0.0s"]), REQUIRED)
    assert done.returncode == 1


def test_a_failed_required_test_is_refused(tmp_path):
    lines = _all("PASS")
    lines[2] = "--- FAIL: %s (0.00s)" % REQUIRED[2]
    assert _assert(_log(tmp_path, lines), REQUIRED).returncode == 1


def test_a_required_test_that_never_ran_is_refused(tmp_path):
    done = _assert(_log(tmp_path, _all("PASS")[:3]), REQUIRED)
    assert done.returncode == 1
    assert "MISSING" in done.stderr and REQUIRED[3] in done.stderr


def test_an_indented_subtest_line_does_not_satisfy_its_parent(tmp_path):
    lines = ["    --- PASS: %s (0.00s)" % name for name in REQUIRED]
    assert _assert(_log(tmp_path, lines), REQUIRED).returncode == 1


def test_a_longer_test_name_does_not_satisfy_a_prefix(tmp_path):
    lines = ["--- PASS: TestHostLoadExecuteCreateCarriesOCIDExtra (0.00s)"]
    done = _assert(_log(tmp_path, lines), REQUIRED[:1])
    assert done.returncode == 1


def test_usage_errors_are_not_a_pass(tmp_path):
    assert _assert(tmp_path / "missing.log", REQUIRED).returncode == 2
    assert _assert(_log(tmp_path, _all("PASS")), []).returncode == 2


def _fresh(out, root):
    env = {"WASM_FRESH_OUT": out, "APP_ROOT": str(root), "PATH": "/usr/bin:/bin"}
    return subprocess.run(
        ["sh", str(FRESH), "TestX"], env=env, capture_output=True, text=True
    )


def test_a_scratch_path_inside_the_repository_is_refused(tmp_path):
    for out in (
        str(tmp_path / "module" / "module.wasm"),
        str(tmp_path / "anything.wasm"),
        str(tmp_path),
    ):
        done = _fresh(out, tmp_path)
        assert done.returncode == 2, (out, done.stderr)
        assert "inside the repository" in done.stderr


def test_a_relative_scratch_path_is_refused(tmp_path):
    done = _fresh("module/module.wasm", tmp_path)
    assert done.returncode == 2
    assert "absolute path" in done.stderr


def test_no_required_test_name_is_a_usage_error(tmp_path):
    env = {"APP_ROOT": str(tmp_path), "PATH": "/usr/bin:/bin"}
    done = subprocess.run(["sh", str(FRESH)], env=env, capture_output=True, text=True)
    assert done.returncode == 2


def test_the_make_default_scratch_path_is_outside_the_repository():
    text = MK.read_text()
    line = next(x for x in text.splitlines() if x.startswith("WASM_FRESH_OUT"))
    value = line.split(":=", 1)[1].strip()
    assert value.startswith("/tmp/")
    assert "module/module.wasm" not in value


def test_the_make_target_requires_exactly_the_four_real_wasm_tests():
    text = MK.read_text()
    start = text.index("WASM_FRESH_REQUIRED_TESTS :=")
    block = text[start : text.index("\n\n", start)]
    names = [tok for tok in block.replace("\\", " ").split() if tok.startswith("Test")]
    assert tuple(names) == REQUIRED
