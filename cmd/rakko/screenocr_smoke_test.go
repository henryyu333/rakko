//go:build darwin

package main

// Real-window proof for 「框选文字」 (run with RAKKO_BASKET_SMOKE=1):
// the permission card is screenshotted on the live panel, then the whole
// authorized round is driven end-to-end — the interactive capture is
// stubbed to write a fixture shot, the real Vision recognizer reads it,
// and the verdict lands on the real system clipboard. The clipboard is
// snapshotted and restored by snapshotClipboard; nothing touches tcc,
// screencapture's own picker or ~/Library/Application Support/ShelfU.
// Screenshots land in /tmp/shelf-C/.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rakko/internal/mac"
)

const ocrShotDirC = "/tmp/shelf-C"

// shotOCR screenshots the notch window to /tmp/shelf-C/name.png.
func shotOCR(t *testing.T, a *shelf, name string) string {
	t.Helper()
	if a.win == nil {
		t.Fatal("no notch window")
	}
	var num int
	onMain(func() { num = mac.WindowNumber(a.win.NativeHandle()) })
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	if err := os.MkdirAll(ocrShotDirC, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond) // settle the open animation
	shot := filepath.Join(ocrShotDirC, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// TestSmokeScreenOCRSheetAndCopy arms the permission path (preflight
// stubbed off — no real grant is requested or touched), shots the card,
// then drives the authorized flow with a stubbed picker and the real
// recognizer.
func TestSmokeScreenOCRSheetAndCopy(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := dropSmokeShelf(t)
	snapshotClipboard(t)
	var oldWrite clipWriter
	onMain(func() { oldWrite, a.write = a.write, fix2PrivateClipboard{} })
	t.Cleanup(func() { onMain(func() { a.write = oldWrite }) })

	preflight, capture := ocrPreflight, ocrCapture
	t.Cleanup(func() { ocrPreflight, ocrCapture = preflight, capture })

	// 1 · No permission: the card takes the current page's stage.
	ocrPreflight = func() bool { return false }
	var sheet, visible bool
	onMain(func() {
		a.startScreenOCR()
		sheet, visible = a.ocrSheet, a.visible
	})
	if !sheet || !visible {
		t.Fatalf("permission card not up: sheet=%v visible=%v", sheet, visible)
	}
	shotOCR(t, a, "sheet")

	// 「取消」closes it; hide drops the armed card either way.
	onMain(func() {
		a.ocrSheet = false
		a.hide(hideExplicit)
	})

	// 2 · Authorized: the capture stub writes the fixture and the real
	// recognizer + clipboard do the rest.
	ocrPreflight = func() bool { return true }
	dir := t.TempDir()
	fixture := filepath.Join(dir, "region.png")
	if err := os.WriteFile(fixture, ocrTextImage(t), 0o600); err != nil {
		t.Fatal(err)
	}
	ocrCapture = func(path string) error {
		b, err := os.ReadFile(fixture)
		if err != nil {
			return err
		}
		return os.WriteFile(path, b, 0o600)
	}
	onMain(a.startScreenOCR)

	deadline := time.Now().Add(30 * time.Second)
	var busy bool
	for time.Now().Before(deadline) {
		onMain(func() { busy = a.ocrScan })
		if !busy {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if busy {
		t.Fatal("round did not finish")
	}
	var status string
	var panelUp bool
	onMain(func() { status, panelUp = a.status, a.visible })
	if panelUp {
		t.Fatal("panel stayed open for the pick")
	}
	if !strings.Contains(status, "已复制识别出的文字") {
		t.Fatalf("verdict = %q", status)
	}
	shotOCR(t, a, "verdict")
	onMain(func() {
		if a.visible {
			a.hide(hideExplicit)
		}
	})
}
