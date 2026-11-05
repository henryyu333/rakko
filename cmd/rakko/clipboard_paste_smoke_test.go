//go:build darwin

package main

// Real-window acceptance for the clipboard paste flow (Issue #4): a controlled
// AppKit receiver (testdata/paste_receiver) stands in for the app the user came
// from. The production flow writes the real pasteboard, collapses the panel,
// hands the keyboard back to the receiver's pid and sends ⌘V to it; the test
// then reads what the receiver's editing control actually received. Run with
// RAKKO_BASKET_SMOKE=1 through the native gate; the user's clipboard is
// snapshotted and restored.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"rakko/internal/mac"
)

func TestSmokePasteIntoReceiver(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native paste acceptance")
	}
	if !mac.AccessibilityTrusted() {
		t.Skip("this process is not trusted for Accessibility: the real ⌘V chain cannot be exercised here")
	}
	a := redesignShelf(t)
	snapshotClipboard(t)
	bin := buildPasteReceiver(t)

	// Text: a filter narrows four rows to three, and ⌘3 pastes the third row
	// the list shows — not the selected first one, not the fourth (filtered
	// out) one.
	t.Run("text", func(t *testing.T) {
		pid, state := launchPasteReceiver(t, bin, "text")
		if !mac.ActivateApp(pid) {
			t.Fatalf("activating the receiver %d", pid)
		}
		if got := waitFrontmost(t, pid); got != pid {
			t.Fatalf("frontmost pid %d, want the receiver %d", got, pid)
		}
		var shown []string
		onMain(func() {
			_ = a.store.Clear()
			for _, s := range []string{"验收一", "验收二", "验收三", "无关记录"} {
				_ = a.store.AddClipText(s, "com.example.Receiver")
			}
			a.openClipboardSearch()
			a.search = "验收"
			for _, it := range a.visibleItems() {
				shown = append(shown, it.Text)
			}
			if items := a.visibleItems(); len(items) > 0 {
				a.selection.only(items[0].ID) // ⌘3 必须忽略它
			}
		})
		if want := []string{"验收三", "验收二", "验收一"}; !slices.Equal(shown, want) {
			t.Fatalf("the filtered order is %q, want %q", shown, want)
		}
		var remembered int
		onMain(func() { remembered = a.prevAppPID })
		if remembered != pid {
			t.Fatalf("the picker remembered pid %d, want the receiver %d", remembered, pid)
		}
		// macOS rate-limits activation: the real user takes a moment to type;
		// a synthetic open-then-collapse within a few milliseconds can be
		// refused, so let the panel settle like a person would.
		time.Sleep(400 * time.Millisecond)

		onMain(func() { a.pasteClipIndex(3) })

		want := shown[2]
		got := waitReceiverField(t, state, "text", func(v string) bool { return v == want })
		if got != want {
			t.Fatalf("the receiver holds %q, want the third displayed item %q", got, want)
		}
		t.Logf("text target received: %q", got)
		finishPasteChecks(t, a, pid, state)
	})

	// Image: the same flow with a PNG row, pasted into a view that takes Paste.
	t.Run("image", func(t *testing.T) {
		pid, state := launchPasteReceiver(t, bin, "image")
		if !mac.ActivateApp(pid) {
			t.Fatalf("activating the receiver %d", pid)
		}
		if got := waitFrontmost(t, pid); got != pid {
			t.Fatalf("frontmost pid %d, want the receiver %d", got, pid)
		}
		onMain(func() {
			_ = a.store.Clear()
			_ = a.store.AddClipImage(testPNGBytes(t), "com.example.Receiver")
			a.openClipboardSearch()
			if items := a.visibleItems(); len(items) > 0 {
				a.selection.only(items[0].ID)
			}
		})
		var remembered int
		onMain(func() { remembered = a.prevAppPID })
		if remembered != pid {
			t.Fatalf("the picker remembered pid %d, want the receiver %d", remembered, pid)
		}

		onMain(func() { a.pasteClipIndex(1) })

		paste := waitReceiverField(t, state, "paste", func(v string) bool {
			return strings.Contains(v, "public.png") && !strings.Contains(v, "png=0")
		})
		t.Logf("image target received: %s", paste)
		finishPasteChecks(t, a, pid, state)
	})
}

// finishPasteChecks asserts what a successful paste leaves behind: the target
// app holds the keyboard and the panel is closed.
func finishPasteChecks(t *testing.T, a *shelf, pid int, state string) {
	t.Helper()
	if front := waitFrontmost(t, pid); front != pid {
		t.Fatalf("after the paste the frontmost pid is %d, want the receiver %d", front, pid)
	}
	var visible bool
	onMain(func() { visible = a.visible })
	if visible {
		t.Fatal("the panel stayed open after a successful paste")
	}
	t.Logf("receiver state: %s", readReceiverState(state))
}

// buildPasteReceiver compiles the AppKit fixture.
func buildPasteReceiver(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("testdata", "paste_receiver", "main.swift"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "receiver")
	out, err := exec.Command("/usr/bin/swiftc", "-O", src, "-o", bin).CombinedOutput()
	if err != nil {
		if _, lookErr := exec.LookPath("swiftc"); lookErr != nil {
			t.Skipf("no Swift toolchain to build the receiver: %v", lookErr)
		}
		t.Fatalf("building the receiver: %v: %s", err, out)
	}
	return bin
}

// launchPasteReceiver starts the fixture in mode and returns its pid and its
// state file. The process is killed when the test ends.
func launchPasteReceiver(t *testing.T, bin, mode string) (int, string) {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command(bin, dir, mode)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the receiver: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	pidFile := filepath.Join(dir, "receiver.pid")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil {
			if pid, perr := parsePid(string(data)); perr == nil {
				return pid, filepath.Join(dir, "receiver.txt")
			}
		}
		if cmd.ProcessState != nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the receiver did not start: %s", stderr.String())
	return 0, ""
}

func parsePid(s string) (int, error) {
	var pid int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &pid); err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, fmt.Errorf("pid %d", pid)
	}
	return pid, nil
}

// readReceiverState returns the receiver's last dump, "" before the first.
func readReceiverState(state string) string {
	data, err := os.ReadFile(state)
	if err != nil {
		return ""
	}
	return string(data)
}

// receiverField returns one key=value line of the receiver's dump.
func receiverField(state, key string) string {
	for line := range strings.SplitSeq(readReceiverState(state), "\n") {
		if v, ok := strings.CutPrefix(line, key+"="); ok {
			return v
		}
	}
	return ""
}

// waitReceiverField waits until the field satisfies ok and returns its value.
func waitReceiverField(t *testing.T, state, key string, ok func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		last = receiverField(state, key)
		if ok(last) {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the receiver's %s stayed %q; state:\n%s", key, last, readReceiverState(state))
	return last
}

// waitFrontmost waits for pid to hold the keyboard, or returns another pid.
func waitFrontmost(t *testing.T, pid int) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var front int
		onMain(func() { front = mac.FrontmostPID() })
		if front == pid {
			return front
		}
		time.Sleep(50 * time.Millisecond)
	}
	var front int
	onMain(func() { front = mac.FrontmostPID() })
	return front
}
