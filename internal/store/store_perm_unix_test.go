//go:build !windows

package store

import "testing"

// requireOwnerOnlyFile checks the 0600 a data file is created with: on
// Unix the mode is the access control, so it must not be readable by
// anyone else.
func requireOwnerOnlyFile(t *testing.T, path string) {
	t.Helper()
	if p := filePerm(t, path); p != 0o600 {
		t.Fatalf("%s perm = %o, want 600", path, p)
	}
}

// requireOwnerOnlyDir checks the 0700 a data directory is created with.
func requireOwnerOnlyDir(t *testing.T, path string) {
	t.Helper()
	if p := filePerm(t, path); p != 0o700 {
		t.Fatalf("%s perm = %o, want 700", path, p)
	}
}
