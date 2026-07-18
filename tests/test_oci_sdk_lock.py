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
    for key in (
        "name",
        "version",
        "vcs",
        "module_hash",
        "gomod_hash",
        "extracted_schema_hash",
    ):
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


def test_extracted_schema_hash_matches_committed_schema():
    """P2.4 breaking-change gate (integrity half): the pinned
    extracted_schema_hash must equal sha256 of the committed generated schema
    (module/schemas/vcn.json). This catches a schema change that was not
    re-pinned/reviewed — including one from an SDK bump via `make oci.generate`.
    The semantic breaking-vs-compatible classification is `oci-extract -diff`.
    """
    import hashlib

    schema = REPO / "module" / "schemas" / "vcn.json"
    digest = hashlib.sha256(schema.read_bytes()).hexdigest()
    assert _dep()["extracted_schema_hash"] == f"sha256:{digest}", (
        "extracted_schema_hash is stale — module/schemas/vcn.json changed. "
        "Review the diff (oci-extract -diff), then re-pin the hash in oci-sdk.lock.yaml."
    )
