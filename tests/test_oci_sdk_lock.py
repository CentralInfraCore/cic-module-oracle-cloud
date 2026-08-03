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
        "extracted_schema_hashes",
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


SCHEMAS_DIR = REPO / "module" / "schemas"


def _service_dirs():
    """Every service subdirectory under module/schemas/ that has committed
    generated schemas — the per-service integrity-hash unit (sweep task C)."""
    return sorted(
        d for d in SCHEMAS_DIR.iterdir() if d.is_dir() and any(d.glob("*.json"))
    )


def _service_schema_hash(service_dir):
    """sha256 over one service's committed generated schemas, stable across
    files: each file's own sha256 in sorted-filename order, joined and hashed
    again. Scoped to a single service directory (module/schemas/<service>/) so
    a change to one service's schemas cannot be masked, or falsely blamed,
    inside a repo-wide combined hash."""
    import hashlib

    h = hashlib.sha256()
    for f in sorted(service_dir.glob("*.json")):
        h.update(f"{f.name}:{hashlib.sha256(f.read_bytes()).hexdigest()}\n".encode())
    return h.hexdigest()


def test_extracted_schema_hashes_cover_every_committed_service():
    """Every module/schemas/<service>/ directory with committed schemas must
    have a pinned entry, and vice versa — a service directory added without
    updating oci-sdk.lock.yaml (or a stale map entry for a service whose
    schemas were removed) is itself a gate failure, not just a hash mismatch
    inside one entry."""
    pinned = set(_dep()["extracted_schema_hashes"])
    committed = {d.name for d in _service_dirs()}
    assert pinned == committed, (
        f"extracted_schema_hashes keys {sorted(pinned)} do not match the "
        f"committed service directories {sorted(committed)} under module/schemas/."
    )


def test_extracted_schema_hashes_match_committed_schemas():
    """P2.4 breaking-change gate (integrity half), per service (sweep task C):
    each service's pinned hash in extracted_schema_hashes must equal the
    combined hash of that service's committed generated schemas
    (module/schemas/<service>/*.json). This catches a schema change that was
    not re-pinned/reviewed — including one from an SDK bump via
    `make oci.generate` — and, because the hash is per service, points a
    reviewer straight at which service changed instead of "something in the
    whole repo did". The semantic breaking-vs-compatible classification is
    `oci-extract -diff`, run per resource within the affected service.
    """
    pinned = _dep()["extracted_schema_hashes"]
    for service_dir in _service_dirs():
        service = service_dir.name
        assert pinned[service] == f"sha256:{_service_schema_hash(service_dir)}", (
            f"extracted_schema_hashes[{service}] is stale — "
            f"module/schemas/{service}/*.json changed. Review the diff "
            f"(oci-extract -diff), then re-pin that service's hash in oci-sdk.lock.yaml."
        )
