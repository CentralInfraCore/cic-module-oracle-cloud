#!/bin/sh
# Verify that the COMMITTED WASM artifact matches the hash the repository declares
# for it: sha256(<module.wasm>) == project.yaml metadata.buildHash.
#
# This checks the files exactly as they are in the checkout. It must run BEFORE any
# step that rebuilds module/module.wasm or rewrites buildHash (`make wasm.build`);
# after such a step it would only compare files that were just created together
# (issue #37). It proves that the committed files agree with each other (artifact
# consistency). It does not verify a signature and does not require the build to be
# reproducible.
#
# Usage: wasm_integrity_gate.sh <module.wasm> <project.yaml>
# Exit:  0 consistent, 1 inconsistent or unreadable, 2 usage error.
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: $0 <module.wasm> <project.yaml>" >&2
    exit 2
fi
WASM=$1
PROJECT=$2

if [ ! -f "$WASM" ]; then
    echo "FATAL: $WASM not found" >&2
    exit 1
fi
if [ ! -f "$PROJECT" ]; then
    echo "FATAL: $PROJECT not found" >&2
    exit 1
fi

ART_HASH=$(sha256sum "$WASM" | cut -d' ' -f1)
EXPECTED_HASH=$(grep -E '^[[:space:]]*buildHash:' "$PROJECT" | sed -E 's/^[[:space:]]*buildHash:[[:space:]]*//' || true)

echo "artifact sha256:        $ART_HASH"
echo "project.yaml buildHash: $EXPECTED_HASH"

if [ "$ART_HASH" != "$EXPECTED_HASH" ]; then
    echo "FATAL: committed $WASM does not match $PROJECT metadata.buildHash" >&2
    echo "  artifact: $ART_HASH" >&2
    echo "  declared: $EXPECTED_HASH" >&2
    echo "The committed artifact and its declared hash disagree (an empty or repeated declaration also fails)." >&2
    echo "Rebuild and re-sign the artifact through the release flow, then commit both." >&2
    exit 1
fi
echo "OK: artifact integrity verified"
