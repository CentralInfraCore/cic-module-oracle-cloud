"""Controls for the fresh-guest step (issue #37).

Two scripts are involved. tools/wasm_assert_tests_passed.sh decides, from a
`go test -v` log, whether the required real-WASM tests really PASSED (go test exits 0
on a skip, so the exit code alone is not evidence). tools/wasm_fresh_guest_test.sh builds
the guest in a private scratch directory that it creates itself, so that no caller-chosen
path (a symlink, a ".." component) can lead it into the repository. Neither of these
checks needs TinyGo: the scratch tests run the script with a PATH that has none, so it
stops at the build, after the guards.
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


SENTINEL = "TRACKED-ARTIFACT-SENTINEL"


def _repo(tmp_path):
    """A fake repository root with a tracked module/module.wasm that must survive."""
    app = tmp_path / "app"
    (app / "module").mkdir(parents=True)
    (app / "module" / "module.wasm").write_text(SENTINEL)
    return app


def _base(tmp_path):
    base = tmp_path / "base"
    base.mkdir()
    return base


def _run(app, **env):
    full = {"APP_ROOT": str(app), "PATH": "/usr/bin:/bin"}
    full.update({k: str(v) for k, v in env.items()})
    return subprocess.run(
        ["sh", str(FRESH), "TestX"], env=full, capture_output=True, text=True
    )


def _sentinel_intact(app):
    path = app / "module" / "module.wasm"
    return path.is_file() and path.read_text() == SENTINEL


def _leftovers(directory):
    return sorted(p.name for p in directory.iterdir() if "cic-fresh-guest" in p.name)


def test_a_symlinked_output_path_cannot_delete_a_tracked_file(tmp_path):
    # The attack from the review of PR #38: a directory outside the repository is a
    # symlink into it. An output path chosen by the caller used to reach the tracked
    # file through it; the script must no longer take an output path at all.
    app = _repo(tmp_path)
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "link").symlink_to(app / "module")
    done = _run(
        app,
        WASM_FRESH_OUT=outside / "link" / "module.wasm",
        WASM_FRESH_TMP=_base(tmp_path),
    )
    assert _sentinel_intact(app), done.stderr
    assert done.returncode != 0


def test_dotdot_in_an_output_path_cannot_delete_a_tracked_file(tmp_path):
    app = _repo(tmp_path)
    outside = tmp_path / "outside"
    outside.mkdir()
    sneaky = str(outside) + "/../app/module/module.wasm"
    done = _run(app, WASM_FRESH_OUT=sneaky, WASM_FRESH_TMP=_base(tmp_path))
    assert _sentinel_intact(app), done.stderr
    assert done.returncode != 0


def test_a_scratch_base_that_is_a_symlink_into_the_repository_is_refused(tmp_path):
    app = _repo(tmp_path)
    outside = tmp_path / "outside"
    outside.mkdir()
    (outside / "link").symlink_to(app / "module")
    done = _run(app, WASM_FRESH_TMP=outside / "link")
    assert done.returncode == 2, done.stderr
    assert "inside the repository" in done.stderr
    assert _sentinel_intact(app)
    assert _leftovers(app / "module") == [], "nothing may be left inside the repository"


def test_a_scratch_base_inside_the_repository_is_refused(tmp_path):
    app = _repo(tmp_path)
    done = _run(app, WASM_FRESH_TMP=app / "module")
    assert done.returncode == 2, done.stderr
    assert "inside the repository" in done.stderr
    assert _sentinel_intact(app)
    assert _leftovers(app / "module") == []


def test_a_relative_or_missing_scratch_base_is_refused(tmp_path):
    app = _repo(tmp_path)
    relative = _run(app, WASM_FRESH_TMP="scratch")
    assert relative.returncode == 2
    assert "absolute" in relative.stderr
    missing = _run(app, WASM_FRESH_TMP=tmp_path / "does-not-exist")
    assert missing.returncode == 2
    assert _sentinel_intact(app)


def test_the_private_scratch_directory_is_removed_even_when_the_build_fails(tmp_path):
    app = _repo(tmp_path)
    base = _base(tmp_path)
    done = _run(app, WASM_FRESH_TMP=base)
    assert done.returncode != 0  # no TinyGo on this PATH: it stops at the build
    assert _leftovers(base) == [], "the scratch directory must not outlive the run"
    assert _sentinel_intact(app)


def test_no_required_test_name_is_a_usage_error(tmp_path):
    env = {"APP_ROOT": str(_repo(tmp_path)), "PATH": "/usr/bin:/bin"}
    done = subprocess.run(["sh", str(FRESH)], env=env, capture_output=True, text=True)
    assert done.returncode == 2


def test_the_make_target_does_not_let_the_caller_choose_an_output_path():
    text = MK.read_text()
    assert "WASM_FRESH_OUT" not in text


def test_the_make_target_requires_exactly_the_four_real_wasm_tests():
    text = MK.read_text()
    start = text.index("WASM_FRESH_REQUIRED_TESTS :=")
    block = text[start : text.index("\n\n", start)]
    names = [tok for tok in block.replace("\\", " ").split() if tok.startswith("Test")]
    assert tuple(names) == REQUIRED
