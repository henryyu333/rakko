package agentnotify

// Another program writing the same agent configuration while Rakko edits it:
// the edit is recomputed on top of the other program's bytes, and when the
// other program keeps winning, nothing is written at all.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// stubBeforeReplace installs fn as the read-modify-write seam for one test.
func stubBeforeReplace(t *testing.T, fn func(path string)) {
	t.Helper()
	beforeReplace = fn
	t.Cleanup(func() { beforeReplace = nil })
}

// TestConnectMergesAWriteFromAnotherProgram: the tool rewrites its own file
// after Rakko has read it; Rakko computes again from those bytes, so the
// user's new setting and Rakko's entry both survive.
func TestConnectMergesAWriteFromAnotherProgram(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	path := ConfigPath(home, ToolGemini)
	writeFile(t, path, "{\n  \"theme\": \"GitHub\"\n}\n")

	wrote := false
	stubBeforeReplace(t, func(string) {
		if wrote {
			return
		}
		wrote = true
		// The tool writes a new preference while Rakko's edit is in flight.
		writeFile(t, path, "{\n  \"theme\": \"GitHub\",\n  \"telemetry\": false\n}\n")
	})

	if err := Connect(home, dataDir, exePath, ToolGemini); err != nil {
		t.Fatalf("connect lost the race instead of recomputing: %v", err)
	}
	if !wrote {
		t.Fatal("the test's write never ran, so no conflict was exercised")
	}
	body := readFile(t, path)
	if !strings.Contains(body, `"telemetry": false`) {
		t.Fatalf("the other program's write was overwritten:\n%s", body)
	}
	if !strings.Contains(body, `"theme": "GitHub"`) {
		t.Fatalf("the user's original setting was lost:\n%s", body)
	}
	if !strings.Contains(body, marker(ToolGemini)) {
		t.Fatalf("Rakko's entry is missing:\n%s", body)
	}
	if got, _ := Status(home, ToolGemini); got != StateInjected {
		t.Fatalf("status = %v, want injected", got)
	}
}

// TestConnectGivesUpWhenAnotherProgramKeepsWriting: every attempt is lost, so
// connect reports an error and the file keeps what the other program wrote —
// with no half-applied Rakko entry and no backup of a file Rakko never got to
// change.
func TestConnectGivesUpWhenAnotherProgramKeepsWriting(t *testing.T) {
	home := testHome(t)
	dataDir := testDataDir(t)
	path := ConfigPath(home, ToolGemini)
	writeFile(t, path, "{\n  \"theme\": \"GitHub\"\n}\n")

	writes := 0
	stubBeforeReplace(t, func(string) {
		writes++
		writeFile(t, path, "{\n  \"attempt\": "+strconv.Itoa(writes)+"\n}\n")
	})

	err := Connect(home, dataDir, exePath, ToolGemini)
	if err == nil {
		t.Fatal("connect reported success although every attempt was overwritten")
	}
	if !strings.Contains(err.Error(), "another program") {
		t.Fatalf("the error does not say what happened: %v", err)
	}
	if writes != conflictTries {
		t.Fatalf("%d attempts, want %d", writes, conflictTries)
	}
	want := "{\n  \"attempt\": " + strconv.Itoa(conflictTries) + "\n}\n"
	if body := readFile(t, path); body != want {
		t.Fatalf("the file no longer holds the other program's bytes:\n%s", body)
	}
	if matches, _ := os.ReadDir(filepath.Dir(path)); len(matches) != 1 {
		// Only the configuration file itself: no temporary files and no
		// backup of an edit that never happened.
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, m.Name())
		}
		t.Fatalf("the failed connect left files behind: %v", names)
	}
}
