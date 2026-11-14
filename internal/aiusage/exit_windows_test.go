//go:build windows

package aiusage

import (
	"fmt"
	"os/exec"
	"testing"
)

// exitError is a real *exec.ExitError with the given exit code: cmd.exe
// reports the same shape as `security` on macOS does.
func exitError(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("cmd", "/c", fmt.Sprintf("exit %d", code)).Run()
	if err == nil {
		t.Fatalf("exit %d did not fail", code)
	}
	return err
}
