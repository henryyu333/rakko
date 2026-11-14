//go:build windows

package platform

// Process identity. A process id is not an identity on Windows: the system
// reuses the ids of exited processes, so anything answered from an id must be
// asked for again rather than remembered.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProcessExeIsNotMemoizedByPID checks that a process's executable is read
// again instead of being remembered by its id. The old memo answered the
// executable of the exited process for whatever process took the id next, so
// the frontmost app and the running-app list could name the wrong program.
func TestProcessExeIsNotMemoizedByPID(t *testing.T) {
	ping := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	cmd := exec.Command(ping, "-n", "20", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start a child process: %v", err)
	}
	pid := cmd.Process.Pid
	childPath := processExe(pid)
	if childPath == "" {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatal("processExe of a live child process is empty")
	}
	if !strings.EqualFold(filepath.Base(childPath), "PING.EXE") {
		cmd.Process.Kill()
		cmd.Wait()
		t.Fatalf("processExe(%d) = %q, want the child's PING.EXE", pid, childPath)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill the child: %v", err)
	}
	cmd.Wait()

	// The id is free again, so a memo would keep answering PING.EXE. A
	// reused id would name whatever process took it, which is a different
	// image.
	if got := processExe(pid); got == childPath {
		t.Fatalf("processExe(%d) still answers %q after the process exited", pid, childPath)
	}
}
