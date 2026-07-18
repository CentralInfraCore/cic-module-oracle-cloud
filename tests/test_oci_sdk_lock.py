"""P2.1 — the pinned OCI Go SDK lock.

Structural checks on oci-sdk.lock.yaml: the fields the P2.2 extractor will read
are present and well-formed, and the pin is internally consistent (the version
matches the VCS ref). This does not reach the network; the recorded hashes are
verified against sum.golang.org when the lock is created/bumped, not on every CI
run.
"""

import re
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent
LOCK = REPO / "oci-sdk.lock.yaml"


def _dep():
    return yaml.safe_load(LOCK.read_text())["provider_dependency"]


def test_required_fields_present():
    dep = _dep()
    for key in ("name", "version", "vcs", "module_hash", "gomod_hash", "extracted_schema_hash"):
        assert key in dep, f"missing {key}"
    for key in ("url", "ref", "commit"):
        assert key in dep["vcs"], f"missing vcs.{key}"


def test_version_is_a_pinned_v65():
    assert re.fullmatch(r"v65\.\d+\.\d+", _dep()["version"])


def test_commit_is_a_full_sha():
    assert re.fullmatch(r"[0-9a-f]{40}", _dep()["vcs"]["commit"])


def test_version_matches_vcs_ref():
    dep = _dep()
    assert dep["vcs"]["ref"] == f"refs/tags/{dep['version']}"


def test_module_hashes_are_gosum_h1():
    dep = _dep()
    for h in (dep["module_hash"], dep["gomod_hash"]):
        assert re.fullmatch(r"h1:[A-Za-z0-9+/]+=*", h), f"not an h1 hash: {h}"


def test_extracted_schema_hash_awaits_extractor():
    # Filled by P2.2; null until the extractor exists.
    assert _dep()["extracted_schema_hash"] is None
