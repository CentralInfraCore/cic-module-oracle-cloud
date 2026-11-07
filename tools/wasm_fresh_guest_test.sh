#!/bin/sh
# Build the guest to a SCRATCH path and run the module tests against that binary.
#
# The guest is built fresh from the current sources, outside the repository, so it
# never overwrites the tracked module/module.wasm and never rewrites
# project.yaml. The tests then load it through CIC_MODULE_WASM_PATH, which makes
# the real-WASM Call tests execute instead of skipping. A required test that does
# not PASS fails the run: `go test` alone exits 0 on a skip.
#
# Runs inside the builder container. Environment:
#   APP_ROOT        repository root (default /app)
#   WASM_FRESH_OUT  scratch output path (default /tmp/cic-fresh-guest/module.wasm);
#                   refused if it is inside APP_ROOT or is a relative path
#   WASM_TARGET     TinyGo target (default wasip1)
#
# Usage: wasm_fresh_guest_test.sh <required TestName>...
# Exit:  0 built and all required tests passed, 1 failure, 2 usage / refused path.
set -eu

APP_ROOT=${APP_ROOT:-/app}
OUT=${WASM_FRESH_OUT:-/tmp/cic-fresh-guest/module.wasm}
TARGET=${WASM_TARGET:-wasip1}

if [ "$#" -lt 1 ]; then
    echo "usage: $0 <required TestName>..." >&2
    exit 2
fi

case "$OUT" in
    /*) ;;
    *)
        echo "FATAL: WASM_FRESH_OUT must be an absolute path outside the repository, got '$OUT'" >&2
        exit 2
        ;;
esac
case "$OUT" in
    "$APP_ROOT"|"$APP_ROOT"/*)
        echo "FATAL: WASM_FRESH_OUT '$OUT' is inside the repository ($APP_ROOT); it would overwrite tracked files" >&2
        exit 2
        ;;
esac

mkdir -p "$(dirname "$OUT")"
rm -f "$OUT"

(cd "$APP_ROOT/module" && tinygo build -o "$OUT" -target "$TARGET" -scheduler=none .)
echo "fresh guest built: $(sha256sum "$OUT" | cut -d' ' -f1)  (one build artifact; the whole-file hash is not reproducible)"

LOG=$(mktemp)
RC=0
(cd "$APP_ROOT/module" && CIC_MODULE_WASM_PATH="$OUT" GOFLAGS=-mod=mod go test -count=1 -v .) >"$LOG" 2>&1 || RC=$?
cat "$LOG"

if ! sh "$APP_ROOT/tools/wasm_assert_tests_passed.sh" "$LOG" "$@"; then
    rm -f "$LOG"
    exit 1
fi
rm -f "$LOG"
if [ "$RC" -ne 0 ]; then
    echo "FATAL: go test exited with $RC against the fresh guest" >&2
    exit 1
fi
echo "OK: the fresh guest was built to $OUT and every required test passed"
