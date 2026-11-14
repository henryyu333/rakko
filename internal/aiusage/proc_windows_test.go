//go:build windows

package aiusage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestStartInGroupKillsTheWholeTree: cancelling a run ends the child and
// the process the child started, not just the one this package holds.
func TestStartInGroupKillsTheWholeTree(t *testing.T) {
	switch os.Getenv("RAKKO_TREE_HELPER") {
	case "":
		// The test itself.
	case "child":
		grand := exec.Command(os.Args[0], "-test.run=^TestStartInGroupKillsTheWholeTree$")
		grand.Env = append(os.Environ(), "RAKKO_TREE_HELPER=grandchild")
		if err := grand.Start(); err != nil {
			os.Exit(2)
		}
		_ = os.WriteFile(os.Getenv("RAKKO_TREE_RESULT"),
			[]byte(fmt.Sprintf("%d %d\n", os.Getpid(), grand.Process.Pid)), 0o600)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "grandchild":
		time.Sleep(time.Minute)
		os.Exit(0)
	}

	result := filepath.Join(t.TempDir(), "pids")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStartInGroupKillsTheWholeTree$")
	cmd.Env = append(os.Environ(), "RAKKO_TREE_HELPER=child", "RAKKO_TREE_RESULT="+result)
	group, err := startInGroup(cmd)
	if err != nil {
		t.Fatalf("starting the helper tree: %v", err)
	}
	defer group.release()

	var data []byte
	for range 100 {
		if data, _ = os.ReadFile(result); len(data) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fields := strings.Fields(string(data))
	if len(fields) != 2 {
		t.Fatalf("the helper tree reported %q", data)
	}
	pids := make([]uint32, 0, 2)
	for _, f := range fields {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("helper pid %q: %v", f, err)
		}
		pids = append(pids, uint32(pid))
	}

	group.kill()
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the killed run did not end")
	}
	for _, pid := range pids {
		deadline := time.Now().Add(5 * time.Second)
		for processAlive(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("pid %d survived the kill", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// processAlive reports whether pid is a live process.
func processAlive(pid uint32) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// TestRunClaudeLoginConPTY runs a real process under the pseudo console the
// sign-in uses: a zero exit is success, a non-zero one is a failed sign-in.
func TestRunClaudeLoginConPTY(t *testing.T) {
	dir := t.TempDir()
	if err := runClaudeLoginTTY(context.Background(), []string{"cmd.exe", "/c", "exit", "0"}, dir, os.Environ()); err != nil {
		t.Fatalf("a successful run under ConPTY: %v", err)
	}
	if err := runClaudeLoginTTY(context.Background(), []string{"cmd.exe", "/c", "exit", "3"}, dir, os.Environ()); err == nil {
		t.Fatal("exit code 3 reported success")
	}
}

// TestRunClaudeLoginConPTYCancel: a sign-in that runs too long is ended with
// its process, and the wait comes back as a cancelled context.
func TestRunClaudeLoginConPTYCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := runClaudeLoginTTY(ctx, []string{"ping.exe", "-n", "60", "127.0.0.1"}, t.TempDir(), os.Environ())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("cancelling took %v", elapsed)
	}
}

// TestHelperWritesMarker is the child of the binding tests: writing the
// marker is proof it was allowed to run.
func TestHelperWritesMarker(t *testing.T) {
	marker := os.Getenv("RAKKO_MARKER")
	if marker == "" {
		t.Skip("only runs as a child process")
	}
	if err := os.WriteFile(marker, []byte("ran"), 0o600); err != nil {
		os.Exit(3)
	}
}

// TestStartInGroupBindsBeforeTheProcessRuns: a process that cannot be put
// in its job is ended and reported, and it never ran — the old order let it
// run first and ignored the failure.
func TestStartInGroupBindsBeforeTheProcessRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("RAKKO_MARKER", marker)
	old := assignToJob
	assignToJob = func(windows.Handle, uint32) error { return errors.New("the job refused the process") }
	t.Cleanup(func() { assignToJob = old })

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperWritesMarker$")
	group, err := startInGroup(cmd)
	if err == nil {
		group.release()
		t.Fatal("a run that could not be bound reported success")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the process ran before its job held it")
	}
	pid := uint32(cmd.Process.Pid)
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d left running after a failed bind", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunClaudeLoginBindsBeforeTheProcessRuns: the sign-in's entry point
// behaves the same — a failed bind is an error and the process never runs.
func TestRunClaudeLoginBindsBeforeTheProcessRuns(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	t.Setenv("RAKKO_MARKER", marker)
	old := assignToJob
	assignToJob = func(windows.Handle, uint32) error { return errors.New("the job refused the process") }
	t.Cleanup(func() { assignToJob = old })

	err := runClaudeLoginTTY(context.Background(),
		[]string{os.Args[0], "-test.run=^TestHelperWritesMarker$"}, t.TempDir(), os.Environ())
	if err == nil {
		t.Fatal("a sign-in that could not be bound reported success")
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the sign-in ran before its job held it")
	}
}

// TestCancelRightAfterStartLeavesNoProcess: a run cancelled the moment it
// started leaves neither the child nor the grandchild it starts as its
// first act. The child is resumed only after the job holds it, so the
// grandchild is in the job whatever the timing.
func TestCancelRightAfterStartLeavesNoProcess(t *testing.T) {
	result := filepath.Join(t.TempDir(), "pids")
	cmd := exec.Command(os.Args[0], "-test.run=^TestStartInGroupKillsTheWholeTree$")
	cmd.Env = append(os.Environ(), "RAKKO_TREE_HELPER=child", "RAKKO_TREE_RESULT="+result)
	group, err := startInGroup(cmd)
	if err != nil {
		t.Fatalf("starting the helper tree: %v", err)
	}
	group.kill()
	defer group.release()

	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the killed run did not end")
	}
	data, _ := os.ReadFile(result)
	for _, f := range strings.Fields(string(data)) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("helper pid %q: %v", f, err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for processAlive(uint32(pid)) {
			if time.Now().After(deadline) {
				t.Fatalf("pid %d survived a cancel right after start", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
