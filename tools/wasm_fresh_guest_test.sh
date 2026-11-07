#!/bin/sh
# Build the guest in a PRIVATE SCRATCH DIRECTORY and run the module tests against it.
#
# The guest is built fresh from the current sources, outside the repository, so it
# never overwrites the tracked module/module.wasm and never rewrites project.yaml.
# The tests then load it through CIC_MODULE_WASM_PATH, which makes the real-WASM Call
# tests execute instead of skipping. A required test that does not PASS fails the
# run: `go test` alone exits 0 on a skip.
#
# The caller does not choose the output path. A path chosen by the caller can be a
# symlink, or contain "..", that leads into the repository, and a textual prefix check
# cannot see that. Instead the script creates its own directory with mktemp -d (a new
# directory cannot already contain a symlink), refuses it if its PHYSICAL location is
# inside the repository, and removes it when the script exits.
#
# Runs inside the builder container. Environment:
#   APP_ROOT        repository root (default /app)
#   WASM_FRESH_TMP  absolute, existing base directory for the scratch directory
#                   (default /tmp); refused if the directory created in it lies,
#                   physically, inside APP_ROOT
#   WASM_TARGET     TinyGo target (default wasip1)
#
# Usage: wasm_fresh_guest_test.sh <required TestName>...
# Exit:  0 built and all required tests passed, 1 failure, 2 usage / refused location.
set -eu

APP_ROOT=${APP_ROOT:-/app}
BASE=${WASM_FRESH_TMP:-/tmp}
TARGET=${WASM_TARGET:-wasip1}

if [ "$#" -lt 1 ]; then
    echo "usage: $0 <required TestName>..." >&2
    exit 2
fi

case "$BASE" in
    /*) ;;
    *)
        echo "FATAL: WASM_FRESH_TMP must be an absolute path, got '$BASE'" >&2
        exit 2
        ;;
esac
if [ ! -d "$BASE" ]; then
    echo "FATAL: WASM_FRESH_TMP '$BASE' is not an existing directory" >&2
    exit 2
fi
if [ ! -d "$APP_ROOT" ]; then
    echo "FATAL: APP_ROOT '$APP_ROOT' is not a directory" >&2
    exit 2
fi

APP_PHYS=$(cd "$APP_ROOT" && pwd -P)
SCRATCH=$(mktemp -d "$BASE/cic-fresh-guest.XXXXXX")
SCRATCH_PHYS=$(cd "$SCRATCH" && pwd -P)

case "$SCRATCH_PHYS" in
    "$APP_PHYS"|"$APP_PHYS"/*)
        # Our own, just-created, empty directory: rmdir cannot remove anything else.
        rmdir "$SCRATCH"
        echo "FATAL: the scratch directory would be inside the repository ($APP_PHYS), physically: $SCRATCH_PHYS" >&2
        exit 2
        ;;
esac

trap 'rm -rf "$SCRATCH_PHYS"' EXIT
OUT="$SCRATCH_PHYS/module.wasm"
LOG="$SCRATCH_PHYS/go-test.log"

(cd "$APP_PHYS/module" && tinygo build -o "$OUT" -target "$TARGET" -scheduler=none .)
echo "fresh guest built: $(sha256sum "$OUT" | cut -d' ' -f1)  (one build artifact; the whole-file hash is not reproducible)"

RC=0
(cd "$APP_PHYS/module" && CIC_MODULE_WASM_PATH="$OUT" GOFLAGS=-mod=mod go test -count=1 -v .) >"$LOG" 2>&1 || RC=$?
cat "$LOG"

sh "$APP_PHYS/tools/wasm_assert_tests_passed.sh" "$LOG" "$@" || exit 1
if [ "$RC" -ne 0 ]; then
    echo "FATAL: go test exited with $RC against the fresh guest" >&2
    exit 1
fi
echo "OK: the fresh guest was built in a private scratch directory (removed afterwards) and every required test passed"
