"""Tests for the ModuleReleaseEnvelope producer (O-1 WP-01).

The shared conformance vectors (testdata/envelope-vectors.wasm-canon-v1.json) are a byte-identical copy of the set
the relay's consumer (CIC_Relay #233) runs through its own verifier; the keys in them are public test constants and
sign nothing real. No test here talks to a real Vault or signs with a real key.
"""

import base64
import hashlib
import json
import pathlib

import pytest
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec, ed25519, rsa, utils

from tools.releaselib import envelope as env
from tools.releaselib.exceptions import ReleaseError

VECTORS = (
    pathlib.Path(__file__).parent / "testdata" / "envelope-vectors.wasm-canon-v1.json"
)
ED_SEED = bytes(range(32))  # the vectors' public test constant

RAW = b"\x00asm\x01\x00\x00\x00\x00\x0a\x08cic.test\xaa"
EXECUTED = b"\x00asm\x01\x00\x00\x00"
MANIFEST = b"name: m\nversion: 0.1.0\n"
MODULE_ID = "m@0.1.0"


def spki(pub):
    return pub.public_bytes(
        serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo
    )


def load_vectors():
    return json.loads(VECTORS.read_text())


def b64(s):
    return base64.b64decode(s)


@pytest.fixture(scope="module")
def ecdsa_key():
    return ec.generate_private_key(ec.SECP256R1())


def ecdsa_sign(key):
    return lambda msg: key.sign(msg, ec.ECDSA(hashes.SHA256()))


def produce(key, sign=None, **over):
    args = {
        "module_id": MODULE_ID,
        "raw_wasm": RAW,
        "executed_wasm": EXECUTED,
        "manifest_raw": MANIFEST,
    }
    args.update(over)
    return env.produce_release(
        args["module_id"],
        args["raw_wasm"],
        args["executed_wasm"],
        args["manifest_raw"],
        spki(key.public_key()),
        sign or ecdsa_sign(key),
    )


def test_vector_file_is_the_shared_set():
    d = load_vectors()
    assert len(d["cases"]) == 26
    assert (
        hashlib.sha256(VECTORS.read_bytes()).hexdigest().startswith("d8a2e058aecb5ed4")
    )


def test_ed25519_output_is_byte_identical_to_the_shared_vector():
    """Ed25519 is deterministic: the producer must reproduce the vector's envelope and .sig byte for byte, which is
    the cross-implementation check against the file the relay's consumer verifies."""
    case = next(c for c in load_vectors()["cases"] if c["id"] == "ed25519-valid")
    key = ed25519.Ed25519PrivateKey.from_private_bytes(ED_SEED)
    a = case["artifacts"]
    rel = env.produce_release(
        case["envelope"]["module_id"],
        b64(a["wasm_raw_b64"]),
        b64(a["wasm_executed_b64"]),
        b64(a["manifest_raw_b64"]),
        spki(key.public_key()),
        key.sign,
    )
    assert json.loads(rel.envelope) == case["envelope"]
    assert rel.signature.decode() == case["signature_file"]
    assert spki(key.public_key()) == b64(case["signer_spki_b64"])


@pytest.mark.parametrize("case", load_vectors()["cases"], ids=lambda c: c["id"])
def test_every_shared_vector_gets_its_expected_code_from_the_reference_verifier(case):
    a = case["artifacts"]
    args = (
        case["envelope"] if False else json.dumps(case["envelope"]).encode(),
        case["signature_file"].encode(),
        b64(case["signer_spki_b64"]),
        b64(a["wasm_raw_b64"]),
        b64(a["wasm_executed_b64"]),
        b64(a["manifest_raw_b64"]),
    )
    if case["expect"] is None:
        assert env.verify_release(*args).startswith("sha256:")
        return
    with pytest.raises(env.EnvelopeError) as e:
        env.verify_release(*args)
    assert e.value.code == case["expect"]


def test_ecdsa_release_verifies_and_carries_the_envelope_identity(ecdsa_key):
    rel = produce(ecdsa_key)
    fields = env.envelope_fields(
        MODULE_ID, RAW, EXECUTED, MANIFEST, spki(ecdsa_key.public_key())
    )
    assert rel.envelope_digest == env.sha256_ref(env.signed_bytes(fields))
    assert rel.signature.endswith(b"\n") and rel.signature.count(b"\n") == 1
    doc = json.loads(rel.envelope)
    assert (
        doc["wasm_sha256"] != doc["executed_wasm_sha256"]
    ), "the raw and the executed identity are separate claims"
    assert doc["signer"] == {
        "key_id": env.key_id_of(spki(ecdsa_key.public_key())),
        "alg": "ecdsa-p256-sha256",
    }


def test_the_signed_domain_is_byte_exact():
    fields = {
        "module_id": MODULE_ID,
        "wasm_sha256": "sha256:" + "ab" * 32,
        "executed_wasm_sha256": "sha256:" + "cd" * 32,
        "manifest_digest": "sha256:" + "ef" * 32,
        "signer_key_id": "sha256:" + "12" * 32,
        "signer_alg": "ed25519",
    }
    expect = (
        "CICMODENVv1\nmodule_id=m@0.1.0\nwasm_sha256=sha256:"
        + "ab" * 32
        + "\nexecuted_wasm_sha256=sha256:"
        + "cd" * 32
        + "\nmanifest_digest=sha256:"
        + "ef" * 32
        + "\nsigner_key_id=sha256:"
        + "12" * 32
        + "\nsigner_alg=ed25519\n"
    ).encode()
    assert env.signed_bytes(fields) == expect
    assert (
        env.signed_bytes(dict(reversed(list(fields.items())))) == expect
    ), "the order of the dict does not matter"


@pytest.mark.parametrize(
    "name,value",
    [
        ("module_id", "m@0.1.0\nx"),
        ("module_id", "no-version"),
        ("wasm_sha256", "sha256:" + "AB" * 32),
        ("wasm_sha256", "ab" * 32),
        ("manifest_digest", "sha256:" + "ab" * 31),
        ("signer_alg", "rsa-pkcs1-sha1"),
        ("signer_key_id", ""),
        ("module_id", "m@0.1.0 "),
        ("executed_wasm_sha256", "sha256:" + "é" * 64),
    ],
)
def test_signed_domain_refuses_malformed_fields(name, value):
    fields = {
        "module_id": MODULE_ID,
        "wasm_sha256": "sha256:" + "ab" * 32,
        "executed_wasm_sha256": "sha256:" + "cd" * 32,
        "manifest_digest": "sha256:" + "ef" * 32,
        "signer_key_id": "sha256:" + "12" * 32,
        "signer_alg": "ed25519",
    }
    fields[name] = value
    with pytest.raises(env.EnvelopeError) as e:
        env.signed_bytes(fields)
    assert e.value.code == "ENVELOPE_INVALID"


def test_signed_domain_needs_exactly_the_six_fields():
    with pytest.raises(env.EnvelopeError):
        env.signed_bytes({"module_id": MODULE_ID})


def test_manifest_edited_after_signing_is_detected(ecdsa_key):
    """ENV-01: the manifest is bound by its raw bytes; one changed byte after signing no longer matches."""
    rel = produce(ecdsa_key)
    with pytest.raises(env.EnvelopeError) as e:
        env.verify_release(
            rel.envelope,
            rel.signature,
            spki(ecdsa_key.public_key()),
            RAW,
            EXECUTED,
            MANIFEST + b"#",
        )
    assert e.value.code == "MANIFEST_DIGEST_MISMATCH"


def test_a_different_executed_representation_is_detected(ecdsa_key):
    rel = produce(ecdsa_key)
    with pytest.raises(env.EnvelopeError) as e:
        env.verify_release(
            rel.envelope,
            rel.signature,
            spki(ecdsa_key.public_key()),
            RAW,
            RAW,
            MANIFEST,
        )
    assert e.value.code == "EXECUTED_DIGEST_MISMATCH"


def test_a_modified_raw_wasm_is_detected(ecdsa_key):
    rel = produce(ecdsa_key)
    with pytest.raises(env.EnvelopeError) as e:
        env.verify_release(
            rel.envelope,
            rel.signature,
            spki(ecdsa_key.public_key()),
            RAW + b"\x00",
            EXECUTED,
            MANIFEST,
        )
    assert e.value.code == "WASM_DIGEST_MISMATCH"


def test_signature_by_another_key_is_refused_at_production(ecdsa_key):
    other = ec.generate_private_key(ec.SECP256R1())
    with pytest.raises(env.EnvelopeError) as e:
        produce(ecdsa_key, sign=ecdsa_sign(other))
    assert e.value.code == "SIGNATURE_INVALID"


def test_wrong_ecdsa_marshaling_is_refused_at_production(ecdsa_key):
    """A signer that returns r||s (IEEE P1363) instead of ASN.1 DER must fail here, not in the relay."""

    def p1363(msg):
        r, s = utils.decode_dss_signature(
            ecdsa_key.sign(msg, ec.ECDSA(hashes.SHA256()))
        )
        return r.to_bytes(32, "big") + s.to_bytes(32, "big")

    with pytest.raises(env.EnvelopeError) as e:
        produce(ecdsa_key, sign=p1363)
    assert e.value.code == "SIGNATURE_INVALID"


@pytest.mark.parametrize(
    "make",
    [
        lambda: ec.generate_private_key(ec.SECP384R1()),
        lambda: rsa.generate_private_key(public_exponent=65537, key_size=2048),
    ],
    ids=["p384", "rsa"],
)
def test_keys_outside_the_allowlist_are_refused(make):
    key = make()
    with pytest.raises(env.EnvelopeError) as e:
        env.algorithm_of(spki(key.public_key()))
    assert e.value.code == "ALG_MISMATCH"


def test_signature_file_form():
    assert env.signature_file(b"\x01\x02\x03") == b"AQID\n"


def test_envelope_json_is_one_object_with_a_final_lf(ecdsa_key):
    rel = produce(ecdsa_key)
    assert rel.envelope.endswith(b"\n") and rel.envelope.count(b"\n") == 1
    assert list(json.loads(rel.envelope)) == [
        "kind",
        "envelope_version",
        "module_id",
        "wasm_sha256",
        "executed_wasm_sha256",
        "manifest_digest",
        "signer",
    ]


class FakeVault:
    """Stands in for VaultService: a prehashed sha2-256 transit signature, as `vault:v1:<base64 DER>`."""

    def __init__(self, key, prefix="vault:v1:", p1363=False):
        self.key, self.prefix, self.p1363, self.calls = key, prefix, p1363, []

    def sign(self, digest_b64, key_name):
        self.calls.append((digest_b64, key_name))
        digest = base64.b64decode(digest_b64)
        der = self.key.sign(digest, ec.ECDSA(utils.Prehashed(hashes.SHA256())))
        if self.p1363:
            r, s = utils.decode_dss_signature(der)
            der = r.to_bytes(32, "big") + s.to_bytes(32, "big")
        return self.prefix + base64.b64encode(der).decode()


def test_vault_adapter_sends_the_prehashed_digest_of_the_signed_bytes(ecdsa_key):
    vault = FakeVault(ecdsa_key)
    rel = produce(ecdsa_key, sign=env.vault_ecdsa_signer(vault, "some-key"))
    fields = env.envelope_fields(
        MODULE_ID, RAW, EXECUTED, MANIFEST, spki(ecdsa_key.public_key())
    )
    expect_digest = base64.b64encode(
        hashlib.sha256(env.signed_bytes(fields)).digest()
    ).decode()
    assert vault.calls == [(expect_digest, "some-key")]
    assert rel.envelope_digest.startswith("sha256:")


def test_vault_adapter_refuses_a_signature_without_the_vault_prefix(ecdsa_key):
    with pytest.raises(env.EnvelopeError) as e:
        produce(
            ecdsa_key, sign=env.vault_ecdsa_signer(FakeVault(ecdsa_key, prefix=""), "k")
        )
    assert e.value.code == "SIGNER_ERROR"


def test_vault_adapter_with_p1363_marshaling_is_caught_by_the_self_check(ecdsa_key):
    with pytest.raises(env.EnvelopeError) as e:
        produce(
            ecdsa_key,
            sign=env.vault_ecdsa_signer(FakeVault(ecdsa_key, p1363=True), "k"),
        )
    assert e.value.code == "SIGNATURE_INVALID"


def test_envelope_error_is_a_release_error():
    assert issubclass(env.EnvelopeError, ReleaseError)


def test_verify_refuses_unknown_shape_and_version(ecdsa_key):
    rel = produce(ecdsa_key)
    good = json.loads(rel.envelope)
    key = spki(ecdsa_key.public_key())
    for mutate, code in [
        (lambda d: d.update(extra=1), "ENVELOPE_INVALID"),
        (lambda d: d.update(envelope_version=2), "ENVELOPE_VERSION"),
        (lambda d: d.update(kind="Other"), "ENVELOPE_INVALID"),
        (lambda d: d["signer"].update(x=1), "ENVELOPE_INVALID"),
    ]:
        d = json.loads(json.dumps(good))
        mutate(d)
        with pytest.raises(env.EnvelopeError) as e:
            env.verify_release(
                json.dumps(d).encode(), rel.signature, key, RAW, EXECUTED, MANIFEST
            )
        assert e.value.code == code
    with pytest.raises(env.EnvelopeError) as e:
        env.verify_release(b"not json", rel.signature, key, RAW, EXECUTED, MANIFEST)
    assert e.value.code == "ENVELOPE_INVALID"
