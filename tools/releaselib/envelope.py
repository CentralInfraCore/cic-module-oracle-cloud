"""ModuleReleaseEnvelope v1 producer (O-1 WP-01, issue #162, ENV-01..03).

The envelope is a detached, signed statement about one release of a module:

    module_id, wasm_sha256 (the raw wasm), executed_wasm_sha256 (the bytes that are compiled), manifest_digest
    (the raw bytes of the whole release manifest), signer {key_id, alg}.

It is signed over the byte-exact CICMODENVv1 domain (fixed field order, ASCII, LF, lowercase hex), and the signature
is stored in a one-line standard-base64 ``.sig`` file. The consumer is the relay's ``admitModuleArtifact``
(CIC_Relay #233); the two are tied together by the shared conformance vectors.

What this module does NOT do:

* It does not compute ``executed_wasm_sha256``. That value is the output of the relay's pinned ``wasm-canon/v1``
  transformation (wabin ``v0.0.0-20230304001439-f6f874872834``, CoreFeaturesV2), which is not reimplemented here; the
  caller passes the executed bytes (``executed_wasm``) produced by that transformation. A wrong value is caught by the
  consumer (``EXECUTED_DIGEST_MISMATCH``), not hidden.
* It does not sign by itself. ``produce_release`` takes a ``sign`` callable. ``vault_ecdsa_signer`` adapts the existing
  ``VaultService.sign`` for the ecdsa-p256-sha256 case; using it against the real Vault is a release action that needs
  the owner's separate approval. Tests use a local test key.
* The manifest is finalised first, the envelope second: it is detached, so there is no self-reference.
"""

import base64
import hashlib
import json
import re
from dataclasses import dataclass
from typing import Callable

from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec, ed25519

from .exceptions import ReleaseError

ENVELOPE_DOMAIN = "CICMODENVv1"
ALG_ECDSA_P256 = "ecdsa-p256-sha256"
ALG_ED25519 = "ed25519"
ALGORITHMS = (ALG_ECDSA_P256, ALG_ED25519)

_DIGEST_RE = re.compile(r"^sha256:[0-9a-f]{64}$")
_MODULE_ID_RE = re.compile(r"^[a-z0-9][a-z0-9._-]*@v?[0-9]+\.[0-9]+\.[0-9]+$")
_FIELDS = (
    "module_id",
    "wasm_sha256",
    "executed_wasm_sha256",
    "manifest_digest",
    "signer_key_id",
    "signer_alg",
)


class EnvelopeError(ReleaseError):
    """A named envelope failure; ``code`` is the consumer's code where one exists."""

    def __init__(self, code, message):
        super().__init__(f"{code}: {message}")
        self.code = code


def sha256_ref(data: bytes) -> str:
    """Return ``sha256:<lowercase hex>`` of data."""
    return "sha256:" + hashlib.sha256(data).hexdigest()


def key_id_of(spki_der: bytes) -> str:
    """The key identity: sha256 of the DER SubjectPublicKeyInfo (ENV-02)."""
    return sha256_ref(spki_der)


def algorithm_of(spki_der: bytes) -> str:
    """The signature algorithm the key type implies; anything outside the allowlist is rejected (ENV-02)."""
    key = serialization.load_der_public_key(spki_der)
    if isinstance(key, ed25519.Ed25519PublicKey):
        return ALG_ED25519
    if isinstance(key, ec.EllipticCurvePublicKey) and isinstance(
        key.curve, ec.SECP256R1
    ):
        return ALG_ECDSA_P256
    raise EnvelopeError(
        "ALG_MISMATCH", "the signer key is neither ecdsa-p256 nor ed25519"
    )


def _check_field(name: str, value: str) -> None:
    if (
        not isinstance(value, str)
        or not value.isascii()
        or any(c in value for c in "\r\n \t")
        or value == ""
    ):
        raise EnvelopeError(
            "ENVELOPE_INVALID",
            f"{name} must be a non-empty ASCII value without whitespace",
        )
    if name == "module_id" and not _MODULE_ID_RE.match(value):
        raise EnvelopeError("ENVELOPE_INVALID", "module_id must be name@semver")
    if name in (
        "wasm_sha256",
        "executed_wasm_sha256",
        "manifest_digest",
        "signer_key_id",
    ) and not _DIGEST_RE.match(value):
        raise EnvelopeError(
            "ENVELOPE_INVALID", f"{name} must be sha256:<64 lowercase hex>"
        )
    if name == "signer_alg" and value not in ALGORITHMS:
        raise EnvelopeError("ENVELOPE_INVALID", f"signer_alg {value!r} is not allowed")


def signed_bytes(fields: dict) -> bytes:
    """The byte-exact CICMODENVv1 signed domain: the domain line, then the six fields in fixed order, LF terminated."""
    if set(fields) != set(_FIELDS):
        raise EnvelopeError(
            "ENVELOPE_INVALID", "the signed domain needs exactly the six fields"
        )
    lines = [ENVELOPE_DOMAIN]
    for name in _FIELDS:
        _check_field(name, fields[name])
        lines.append(f"{name}={fields[name]}")
    return ("\n".join(lines) + "\n").encode("ascii")


def envelope_fields(module_id, raw_wasm, executed_wasm, manifest_raw, spki_der) -> dict:
    """The six signed fields of one release, derived from the bytes themselves (never from a claimed digest)."""
    return {
        "module_id": module_id,
        "wasm_sha256": sha256_ref(raw_wasm),
        "executed_wasm_sha256": sha256_ref(executed_wasm),
        "manifest_digest": sha256_ref(manifest_raw),
        "signer_key_id": key_id_of(spki_der),
        "signer_alg": algorithm_of(spki_der),
    }


def envelope_json(fields: dict) -> bytes:
    """The envelope file: the wire object (module-release-envelope.schema.json), one JSON object and a final LF."""
    doc = {
        "kind": "ModuleReleaseEnvelope",
        "envelope_version": 1,
        "module_id": fields["module_id"],
        "wasm_sha256": fields["wasm_sha256"],
        "executed_wasm_sha256": fields["executed_wasm_sha256"],
        "manifest_digest": fields["manifest_digest"],
        "signer": {"key_id": fields["signer_key_id"], "alg": fields["signer_alg"]},
    }
    return (json.dumps(doc, separators=(",", ":")) + "\n").encode("ascii")


def signature_file(signature: bytes) -> bytes:
    """The ``.sig`` file: the signature as RFC 4648 section 4 standard base64 (padded), one line, one LF (ENV-02)."""
    return base64.b64encode(signature) + b"\n"


@dataclass(frozen=True)
class Release:
    """The files of one produced release envelope."""

    envelope: bytes  # <module>.envelope.json
    signature: bytes  # <module>.sig
    envelope_digest: str  # sha256 of the signed bytes: the identity the relay binds the capability to (ENV-03)


def verify_release(
    envelope, sig_file, spki_der, raw_wasm, executed_wasm, manifest_raw
) -> str:
    """Independently verify a produced release against the bytes (V1, V2, V4 digests and V5 of the contract).

    Returns the envelope digest, raises EnvelopeError(code) otherwise. Used as the producer's self-check, and by
    the tests as the reference consumer (the relay's own check is the real one)."""
    try:
        doc = json.loads(envelope)
    except ValueError as e:
        raise EnvelopeError("ENVELOPE_INVALID", f"not JSON: {e}") from e
    expect = {
        "kind",
        "envelope_version",
        "module_id",
        "wasm_sha256",
        "executed_wasm_sha256",
        "manifest_digest",
        "signer",
    }
    if (
        not isinstance(doc, dict)
        or set(doc) != expect
        or set(doc.get("signer", {})) != {"key_id", "alg"}
    ):
        raise EnvelopeError(
            "ENVELOPE_INVALID", "the envelope is not exactly the contract's shape"
        )
    if doc["envelope_version"] != 1:
        raise EnvelopeError(
            "ENVELOPE_VERSION", f"unknown envelope_version {doc['envelope_version']!r}"
        )
    if doc["kind"] != "ModuleReleaseEnvelope":
        raise EnvelopeError("ENVELOPE_INVALID", "kind must be ModuleReleaseEnvelope")
    fields = {
        "module_id": doc["module_id"],
        "wasm_sha256": doc["wasm_sha256"],
        "executed_wasm_sha256": doc["executed_wasm_sha256"],
        "manifest_digest": doc["manifest_digest"],
        "signer_key_id": doc["signer"]["key_id"],
        "signer_alg": doc["signer"]["alg"],
    }
    signed = signed_bytes(fields)
    if fields["signer_alg"] != algorithm_of(spki_der):
        raise EnvelopeError(
            "ALG_MISMATCH", "signer.alg does not match the signer key type"
        )
    if fields["signer_key_id"] != key_id_of(spki_der):
        raise EnvelopeError("KEY_MISMATCH", "signer.key_id is not the signer key")
    if (
        not sig_file.endswith(b"\n")
        or b"\n" in sig_file[:-1]
        or any(c in sig_file for c in b"\r \t")
    ):
        raise EnvelopeError(
            "SIG_ENCODING", "the signature file must be one base64 line ended by one LF"
        )
    try:
        sig = base64.b64decode(sig_file[:-1], validate=True)
    except ValueError as e:
        raise EnvelopeError(
            "SIG_ENCODING", "the signature is not standard base64"
        ) from e
    if base64.b64encode(sig) != sig_file[:-1]:
        raise EnvelopeError("SIG_ENCODING", "the signature is not canonical base64")
    pub = serialization.load_der_public_key(spki_der)
    try:
        if isinstance(pub, ed25519.Ed25519PublicKey):
            pub.verify(sig, signed)
        elif isinstance(pub, ec.EllipticCurvePublicKey):
            pub.verify(sig, signed, ec.ECDSA(hashes.SHA256()))
        else:
            raise EnvelopeError(
                "ALG_MISMATCH", "the signer key is neither ecdsa-p256 nor ed25519"
            )
    except InvalidSignature as e:
        raise EnvelopeError(
            "SIGNATURE_INVALID", "the signature does not verify over the signed bytes"
        ) from e
    if fields["wasm_sha256"] != sha256_ref(raw_wasm):
        raise EnvelopeError(
            "WASM_DIGEST_MISMATCH", "the raw wasm is not the one the envelope names"
        )
    if fields["executed_wasm_sha256"] != sha256_ref(executed_wasm):
        raise EnvelopeError(
            "EXECUTED_DIGEST_MISMATCH",
            "the executed wasm is not the one the envelope names",
        )
    if fields["manifest_digest"] != sha256_ref(manifest_raw):
        raise EnvelopeError(
            "MANIFEST_DIGEST_MISMATCH", "the manifest is not the one the envelope names"
        )
    return sha256_ref(signed)


def produce_release(
    module_id,
    raw_wasm,
    executed_wasm,
    manifest_raw,
    spki_der,
    sign: Callable[[bytes], bytes],
) -> Release:
    """Produce the envelope and its detached signature for one release.

    ``sign`` receives the signed bytes and returns the raw signature (ecdsa: ASN.1 DER; ed25519: 64 bytes). The result
    is verified against the same bytes before it is returned: a signer that returns the wrong marshaling, or a
    signature by another key, fails here and not in the relay."""
    fields = envelope_fields(module_id, raw_wasm, executed_wasm, manifest_raw, spki_der)
    signed = signed_bytes(fields)
    envelope = envelope_json(fields)
    sig_file = signature_file(sign(signed))
    digest = verify_release(
        envelope, sig_file, spki_der, raw_wasm, executed_wasm, manifest_raw
    )
    return Release(envelope=envelope, signature=sig_file, envelope_digest=digest)


def vault_ecdsa_signer(vault_service, key_name: str) -> Callable[[bytes], bytes]:
    """Adapt the existing ``VaultService.sign`` (a prehashed sha2-256 transit signature) to the ``sign`` callable.

    The ecdsa signature comes back as ``vault:v1:<base64 DER>`` (transit's default ASN.1 marshaling); the producer's
    self-check refuses anything else. Calling this against a real Vault is an owner-approved release action.
    """

    def sign(message: bytes) -> bytes:
        digest_b64 = base64.b64encode(hashlib.sha256(message).digest()).decode("ascii")
        signature = vault_service.sign(digest_b64, key_name)
        prefix = "vault:v1:"
        if not isinstance(signature, str) or not signature.startswith(prefix):
            raise EnvelopeError(
                "SIGNER_ERROR", "the Vault signature has no vault:v1: prefix"
            )
        return base64.b64decode(signature[len(prefix) :], validate=True)

    return sign
