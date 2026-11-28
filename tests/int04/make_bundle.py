"""INT-04: produce releases with the REAL producer and the REAL wasmcanon command, and write them as a bundle the relay's
admission (verifyEnvelope) is run against. Usage: make_bundle.py <wasmcanon-binary> <raw.wasm> <manifest.yaml> <out.json>.

Positive cases are what produce_release returns. Negative cases are made from a produced release by one named mutation
each. Expected codes are the relay's admission codes."""

import base64
import json
import pathlib
import sys

from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import ec, ed25519

from tools.releaselib import envelope as env
from tools.releaselib.wasmcanon_cli import executed_wasm

MODULE_ID = "oracle-cloud@0.1.0"


def spki(pub):
    return pub.public_bytes(
        serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo
    )


def b64(b):
    return base64.b64encode(b).decode()


def main(cli, raw_path, manifest_path, out):
    raw = pathlib.Path(raw_path).read_bytes()
    manifest = pathlib.Path(manifest_path).read_bytes()
    executed = executed_wasm(raw, cli)
    assert (
        executed != raw
    ), "the module has no custom section: the executed representation would not differ"
    ed = ed25519.Ed25519PrivateKey.from_private_bytes(bytes(range(32)))
    ec_key = ec.generate_private_key(ec.SECP256R1())
    signers = {
        "ed25519": (spki(ed.public_key()), lambda m: ed.sign(m)),
        "ecdsa": (
            spki(ec_key.public_key()),
            lambda m: ec_key.sign(m, ec.ECDSA(hashes.SHA256())),
        ),
    }
    cases = []

    def add(cid, rel, spki_der, expect, raw_b=raw, exec_b=executed, man_b=manifest):
        cases.append(
            {
                "id": cid,
                "expect": expect,
                "envelope": (
                    json.loads(rel["envelope"])
                    if isinstance(rel["envelope"], (bytes, str))
                    else rel["envelope"]
                ),
                "signature_file": rel["signature"].decode(),
                "signer_spki_b64": b64(spki_der),
                "raw_b64": b64(raw_b),
                "executed_b64": b64(exec_b),
                "manifest_b64": b64(man_b),
            }
        )

    for name, (spki_der, sign) in signers.items():
        r = env.produce_release(MODULE_ID, raw, executed, manifest, spki_der, sign)
        rel = {"envelope": r.envelope, "signature": r.signature}
        add(f"{name}-produced", rel, spki_der, None)
        add(
            f"{name}-raw-modified",
            rel,
            spki_der,
            "WASM_DIGEST_MISMATCH",
            raw_b=raw + b"\x00",
        )
        add(
            f"{name}-executed-is-the-raw-wasm",
            rel,
            spki_der,
            "EXECUTED_DIGEST_MISMATCH",
            exec_b=raw,
        )
        add(
            f"{name}-manifest-modified",
            rel,
            spki_der,
            "MANIFEST_DIGEST_MISMATCH",
            man_b=manifest + b"#",
        )
        tampered = json.loads(r.envelope)
        tampered["module_id"] = "other@0.1.0"
        add(
            f"{name}-envelope-field-tampered",
            {"envelope": json.dumps(tampered).encode(), "signature": r.signature},
            spki_der,
            "SIGNATURE_INVALID",
        )
        sig = bytearray(base64.b64decode(r.signature.strip()))
        sig[-1] ^= 1
        add(
            f"{name}-signature-bit-flipped",
            {"envelope": r.envelope, "signature": base64.b64encode(bytes(sig)) + b"\n"},
            spki_der,
            "SIGNATURE_INVALID",
        )
    pathlib.Path(out).write_text(
        json.dumps({"module_id": MODULE_ID, "cases": cases}, indent=1)
    )
    print(
        f"{len(cases)} cases: {sum(1 for c in cases if c['expect'] is None)} accepted, "
        f"{sum(1 for c in cases if c['expect'] is not None)} refused; executed {len(raw)}->{len(executed)} bytes"
    )


if __name__ == "__main__":
    main(*sys.argv[1:5])
