//go:build darwin

package main

// Real-window screenshots of the tools page for design comparison, run
// only under RAKKO_BASKET_SMOKE=1 (the shared TestMain hosts the app
// loop). Shots land in /tmp/shelf-A/<name>.png; the instance works in
// the smoke data dir and never touches ~/Library/Application
// Support/ShelfU.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rakko/internal/layout"
	"rakko/internal/mac"
)

const toolsShotDir = "/tmp/shelf-A"

// shotToolsPage puts the panel on the tools tab under RAKKO_ env pins
// and screenshots the notch window.
func shotToolsPage(t *testing.T, a *shelf, name string, setup func()) {
	t.Helper()
	onMain(func() {
		// showPage raises the panel and lands on the tools page; setting
		// only the tab left these shots on the search bar.
		a.showPage(pageTools)
		if setup != nil {
			setup()
		}
		a.win.Invalidate()
	})
	time.Sleep(700 * time.Millisecond)
	onMain(a.syncWindowFrame)
	if err := os.MkdirAll(toolsShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	num := mac.WindowNumber(a.win.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	shot := filepath.Join(toolsShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
}

func TestToolsPageScreenshots(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := dropSmokeShelf(t)
	p := a.toolsPage()
	if p == nil {
		t.Fatal("tools page missing")
	}
	fix := t.TempDir()
	// A couple of tray files so the 最近文件 card and selection have
	// content.
	img := writeSmokePNG(t, fix, "卡片测试.png", 320, 240)
	if err := a.store.AddFiles([]string{img}); err != nil {
		t.Fatal(err)
	}
	// 1. The normal grid.
	shotToolsPage(t, a, "tools-grid", nil)
	// 2. Edit mode: wobbling cards, ⊖ badges, span handle, + slot.
	shotToolsPage(t, a, "tools-edit", func() {
		p.editing, p.editAt = true, time.Now()
	})
	// 3. The catalogue picker.
	shotToolsPage(t, a, "tools-picker", func() {
		p.picking = true
	})
	onMain(func() { p.picking, p.editing = false, true })
	// 4. Files hovering the tools tab: accepting cards glow, others dim,
	// the drop zone stays off (design §1).
	t.Setenv("RAKKO_DEBUG_DROP_FILES", strings.Join([]string{img}, string(filepath.ListSeparator)))
	defer t.Setenv("RAKKO_DEBUG_DROP_FILES", "")
	shotToolsPage(t, a, "tools-files-hover", func() { p.editing = false; a.dragOpenTools = true })
	// A pin card in layout.json shows as a removed slot after reload —
	// reset for a clean exit.
	onMain(func() {
		a.mutateTools(func(l *layout.Layout) {})
		a.hide(hideExplicit)
	})
}
