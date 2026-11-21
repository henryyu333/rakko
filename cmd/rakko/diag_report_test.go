//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The diagnostics signal answers before the shelf exists: a launch that
// hangs still produces a report, and two presses never overwrite each
// other.
func TestDiagnosticsReportBeforeTheShelfExists(t *testing.T) {
	installDiagSignal()
	dir := diagLogDir()
	before := map[string]bool{}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			before[e.Name()] = true
		}
	}
	fresh := func() []string {
		var out []string
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !before[e.Name()] {
				out = append(out, e.Name())
			}
		}
		return out
	}
	for i := range 2 {
		if err := syscall.Kill(os.Getpid(), syscall.SIGUSR1); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(3 * time.Second)
		for len(fresh()) <= i && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if len(fresh()) <= i {
			t.Fatalf("press %d wrote no report", i+1)
		}
	}
	names := fresh()
	t.Cleanup(func() {
		for _, n := range names {
			os.Remove(filepath.Join(dir, n))
		}
	})
	if len(names) != 2 {
		t.Fatalf("two presses wrote %d reports: %v", len(names), names)
	}
	data, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "Rakko Diagnostics") || !strings.Contains(text, "goroutine") {
		t.Fatalf("report has no stacks:\n%s", text[:min(len(text), 400)])
	}
	if !strings.Contains(names[0], ".") {
		t.Fatalf("report name has no milliseconds: %s", names[0])
	}
}
