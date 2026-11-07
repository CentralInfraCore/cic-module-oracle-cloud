//go:build !wasip1

package main

// guest_binary_gate_test.go pins the skip-versus-fail rule of guestBinaryBytes.
//
// The WASM Call tests are evidence only if they really ran against a binary
// built from the current sources. So the one thing that must never happen is a
// skip caused by a binary the caller EXPLICITLY asked for but that cannot be
// read: that would be a green run that executed nothing. A skip stays allowed
// in the two intended cases (the variable is unset and the committed binary is
// not built; or the variable is unset and the individual test needs a fresh
// guest, see requireFreshGuest).

import (
	"os"
	"path/filepath"
	"testing"
)

// inDir runs the rest of a test with dir as the working directory, so a relative
// "module.wasm" resolves inside it.
func inDir(t *testing.T, dir string) {
	t.Helper()
	t.Chdir(dir)
}

func TestGuestBinaryBytesExplicitPathIsNeverSkipped(t *testing.T) {
	dir := t.TempDir()
	cases := []struct{ name, path string }{
		{"a path that does not exist", filepath.Join(dir, "typo.wasm")},
		{"a directory instead of a file", dir},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(wasmPathEnv, tc.path)
			b, skip, err := guestBinaryBytes()
			if err == nil {
				t.Errorf("err = nil, want an error: an explicit but unreadable binary must fail the test")
			}
			if skip != "" {
				t.Errorf("skip = %q, want no skip for an explicit path", skip)
			}
			if b != nil {
				t.Errorf("returned %d bytes for an unreadable path", len(b))
			}
		})
	}
}

func TestGuestBinaryBytesExplicitReadablePathIsLoaded(t *testing.T) {
	f := filepath.Join(t.TempDir(), "fresh.wasm")
	if err := os.WriteFile(f, []byte("\x00asm-fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(wasmPathEnv, f)
	b, skip, err := guestBinaryBytes()
	if err != nil || skip != "" || string(b) != "\x00asm-fake" {
		t.Fatalf("got (%q, %q, %v), want the file's bytes with no skip and no error", b, skip, err)
	}
}

func TestGuestBinaryBytesUnsetAndNotBuiltSkips(t *testing.T) {
	inDir(t, t.TempDir()) // no module.wasm here
	t.Setenv(wasmPathEnv, "")
	b, skip, err := guestBinaryBytes()
	if err != nil || skip == "" || b != nil {
		t.Fatalf("got (%d bytes, skip %q, err %v), want a skip and no error", len(b), skip, err)
	}
}

func TestGuestBinaryBytesUnsetButUnreadableIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, wasmPath), 0o700); err != nil { // "module.wasm" exists but is a directory
		t.Fatal(err)
	}
	inDir(t, dir)
	t.Setenv(wasmPathEnv, "")
	_, skip, err := guestBinaryBytes()
	if err == nil || skip != "" {
		t.Fatalf("got skip %q, err %v, want an error: only 'does not exist' may skip", skip, err)
	}
}

func TestGuestBinaryBytesUnsetUsesTheCommittedBinary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, wasmPath), []byte("committed"), 0o600); err != nil {
		t.Fatal(err)
	}
	inDir(t, dir)
	t.Setenv(wasmPathEnv, "")
	b, skip, err := guestBinaryBytes()
	if err != nil || skip != "" || string(b) != "committed" {
		t.Fatalf("got (%q, %q, %v), want the committed binary's bytes", b, skip, err)
	}
}
