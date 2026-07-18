"""P0.2 — capability manifest schema.

Validates that project.yaml's capability_manifest block conforms to
capability-manifest.schema.yaml, that the schema is wired into
project.schema.yaml via $ref, and that the schema actually rejects
malformed manifests (a schema that only ever passes proves nothing).
"""

from pathlib import Path

import pytest
from jsonschema import ValidationError, validate

from tools.infra import load_and_resolve_schema, load_yaml

REPO = Path(__file__).resolve().parent.parent
MANIFEST_SCHEMA = REPO / "capability-manifest.schema.yaml"
PROJECT_SCHEMA = REPO / "project.schema.yaml"
PROJECT_YAML = REPO / "project.yaml"


@pytest.fixture(scope="module")
def manifest_schema():
    return load_and_resolve_schema(MANIFEST_SCHEMA)


@pytest.fixture(scope="module")
def project_manifest():
    return load_yaml(PROJECT_YAML)["capability_manifest"]


# --- the real declaration validates ---


def test_project_manifest_conforms(manifest_schema, project_manifest):
    validate(instance=project_manifest, schema=manifest_schema)


def test_wired_into_project_schema_via_ref():
    """The full project.yaml validates against project.schema.yaml with the
    capability-manifest $ref resolved — proving the wiring, not just the file."""
    schema = load_and_resolve_schema(PROJECT_SCHEMA)
    validate(instance=load_yaml(PROJECT_YAML), schema=schema)


# --- the schema rejects malformed manifests ---


def _valid():
    return {
        "version": "v0.1.0",
        "egress": [{"host": "*.oraclecloud.com", "methods": ["GET", "POST"]}],
        "secrets": [{"handle": "oci/*", "ops": ["sign"]}],
    }


def test_baseline_is_valid(manifest_schema):
    validate(instance=_valid(), schema=manifest_schema)


@pytest.mark.parametrize(
    "mutate",
    [
        lambda m: m.pop("version"),
        lambda m: m.pop("egress"),
        lambda m: m.pop("secrets"),
        lambda m: m.update(version="0.1.0"),  # missing the v prefix
        lambda m: m["egress"][0].update(methods=["FETCH"]),  # not an HTTP method
        lambda m: m["egress"][0].pop("host"),
        lambda m: m["secrets"][0].update(ops=["exfiltrate"]),  # not sign/read
        lambda m: m["secrets"][0].update(ops=[]),  # ops must be non-empty
        lambda m: m.update(unexpected="x"),  # additionalProperties: false
        lambda m: m.update(limits={"wall_clock": "forever"}),  # bad duration
    ],
    ids=[
        "no-version",
        "no-egress",
        "no-secrets",
        "version-no-v",
        "bad-egress-method",
        "egress-no-host",
        "bad-secret-op",
        "empty-secret-ops",
        "extra-property",
        "bad-limit-duration",
    ],
)
def test_schema_rejects(manifest_schema, mutate):
    bad = _valid()
    mutate(bad)
    with pytest.raises(ValidationError):
        validate(instance=bad, schema=manifest_schema)
