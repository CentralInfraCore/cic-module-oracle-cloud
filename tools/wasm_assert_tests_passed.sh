#!/bin/sh
# Assert that the named Go tests really PASSED in a `go test -v` log.
#
# `go test` exits 0 when a test is skipped, so the exit code alone cannot show that
# a test ran. A test counts as passed only if the log has a top-level
# "--- PASS: <name> (" line for it. A skipped, failed or missing test fails the
# check; indented sub-test lines never count for their parent.
#
# Usage: wasm_assert_tests_passed.sh <go-test-v-log> <TestName>...
# Exit:  0 all named tests passed, 1 at least one did not, 2 usage error.
set -eu

if [ "$#" -lt 2 ]; then
    echo "usage: $0 <go-test-v-log> <TestName>..." >&2
    exit 2
fi
LOG=$1
shift
if [ ! -f "$LOG" ]; then
    echo "FATAL: log $LOG not found" >&2
    exit 2
fi

status=0
for name in "$@"; do
    if grep -qE "^--- PASS: ${name} \(" "$LOG"; then
        echo "  PASS     $name"
    elif grep -qE "^--- SKIP: ${name} \(" "$LOG"; then
        echo "  SKIP     $name   (a skipped test is not evidence)" >&2
        status=1
    elif grep -qE "^--- FAIL: ${name} \(" "$LOG"; then
        echo "  FAIL     $name" >&2
        status=1
    else
        echo "  MISSING  $name   (it did not run at all)" >&2
        status=1
    fi
done

if [ "$status" -ne 0 ]; then
    echo "FATAL: not every required test passed" >&2
fi
exit "$status"
