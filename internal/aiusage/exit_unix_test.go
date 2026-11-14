//go:build !windows

package aiusage

import (
	"fmt"
	"os/exec"
	"testing"
)

// exitError is a real *exec.ExitError with the given exit code, which is
// how `security` reports a missing item (44) or anything else.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("/bin/sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("exit %d did not fail", code)
	}
	return err
}
