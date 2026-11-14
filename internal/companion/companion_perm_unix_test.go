//go:build !windows

package companion

import (
	"os"
	"testing"
)

// requireOwnerOnlyFile checks the 0600 the state file is created with: on
// Unix the mode is the access control.
func requireOwnerOnlyFile(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if p := fi.Mode().Perm(); p != 0o600 {
		t.Fatalf("file mode %v, want 0600", p)
	}
}
