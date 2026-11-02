//go:build darwin

package main

// Real-window proof for 批量改名: five temporary entries (a
// folder and a Chinese name among them) are renamed by all three modes,
// each undone, and disk and tray paths are compared entry by entry. The
// test shoots the real 重新命名 window (a conflict row included) and the
// native item menu and the completion pill with 撤销, and checks the
// keyboard goes back to the app the window was opened from. Everything lives under /tmp/shelf-rename and
// the smoke data dir; user files and the real data directory are never
// touched. Run with RAKKO_BASKET_SMOKE=1.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/store"
)

const renameShotDir = "/tmp/shelf-rename"

// renameShot screenshots the native window w to /tmp/shelf-rename/name.png.
func renameShot(t *testing.T, w *mygo.Window, name string) string {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", name)
	}
	time.Sleep(400 * time.Millisecond) // let the drawn frame settle
	if err := os.MkdirAll(renameShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	shot := filepath.Join(renameShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s -> %s", name, shot)
	return shot
}

// postRightClick posts a real secondary click at a screen point through
// the HID tap, as clickAt does for the primary button.
func postRightClick(t *testing.T, x, y float64) {
	t.Helper()
	requireOwnMouseTarget(t, x, y)
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreGraphics: %v", err)
	}
	var (
		create func(source uintptr, typ uint32, p mac.CGPoint, button uint32) uintptr
		post   func(tap uint32, ev uintptr)
		setF   func(ev uintptr, field uint32, value int64)
		rel    func(cf uintptr)
	)
	purego.RegisterLibFunc(&create, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	purego.RegisterLibFunc(&setF, lib, "CGEventSetIntegerValueField")
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatalf("CoreFoundation: %v", err)
	}
	purego.RegisterLibFunc(&rel, libCF, "CFRelease")
	const (
		rightDown = 3
		rightUp   = 4
		hidTap    = 0
	)
	for _, typ := range []uint32{rightDown, rightUp} {
		ev := create(0, typ, mac.CGPoint{X: x, Y: y}, 1)
		if ev == 0 {
			t.Fatal("CGEventCreateMouseEvent returned nil")
		}
		setF(ev, 1 /* kCGMouseEventClickState */, 1)
		post(hidTap, ev)
		rel(ev)
		time.Sleep(60 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)
}

// postEscape presses a real Escape, closing a native menu.
func postEscape(t *testing.T) {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	var (
		createKey func(source uintptr, key uint16, down bool) uintptr
		post      func(tap uint32, ev uintptr)
		rel       func(cf uintptr)
	)
	purego.RegisterLibFunc(&createKey, lib, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&post, lib, "CGEventPost")
	libCF, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	purego.RegisterLibFunc(&rel, libCF, "CFRelease")
	for _, down := range []bool{true, false} {
		ev := createKey(0, 53 /* Escape */, down)
		if ev == 0 {
			t.Fatal("CGEventCreateKeyboardEvent returned nil")
		}
		post(0, ev)
		rel(ev)
	}
	time.Sleep(300 * time.Millisecond)
}

// diskNames lists a directory.
func diskNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range ents {
		out = append(out, e.Name())
	}
	return out
}

// trayNames lists the store's file names by item path, in tray order.
func trayNames(a *shelf) []string {
	var out []string
	for _, it := range a.store.Items {
		out = append(out, filepath.Base(it.Path))
	}
	return out
}

// sameSet compares two name lists regardless of order.
func sameSet(t *testing.T, got, want []string, what string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", what, got, want)
	}
	seen := map[string]int{}
	for _, n := range got {
		seen[n]++
	}
	for _, n := range want {
		seen[n]--
	}
	for _, c := range seen {
		if c != 0 {
			t.Fatalf("%s = %v, want %v", what, got, want)
		}
	}
}

// TestRenameSmoke runs the real window through the three modes and one
// undo each, with a folder and a Chinese name in the batch.
func TestRenameSmoke(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native rename smoke test")
	}
	a := smokeShelf
	// Esc/Space must not bind real global keys from the test process.
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })

	dir := t.TempDir()
	names := []string{"照片 1.jpg", "照片 2.jpg", "说明.txt", "资料 夹", "报告.pdf"}
	for _, n := range names {
		p := filepath.Join(dir, n)
		if n == "资料 夹" {
			if err := os.Mkdir(p, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(p, "里面.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	onMain(func() {
		for _, it := range a.store.Items {
			if it.Pinned {
				_ = a.store.SetPinned(it.ID, false)
			}
		}
		_ = a.store.Clear()
		var paths []string
		for i := len(names) - 1; i >= 0; i-- { // first name ends frontmost
			paths = append(paths, filepath.Join(dir, names[i]))
		}
		if err := a.store.AddFiles(paths); err != nil {
			t.Fatal(err)
		}
	})
	if got := trayNames(a); len(got) != 5 {
		t.Fatalf("tray names = %v", got)
	}
	if a.win == nil {
		onMain(func() { a.newNotchWindow() })
	}
	expandShelf(t, a)
	time.Sleep(700 * time.Millisecond)

	// Real context menu: right-click the first tile. The tile grid starts
	// 24pt in and ~74pt down from the panel's top-left (DESIGN.md layout),
	// and cells are ~99×100 with the first center at half that.
	frame := a.win.Bounds()
	tileX := float64(frame.X + 24 + 50)
	tileY := float64(frame.Y + 74 + 50)
	postRightClick(t, tileX, tileY)
	if err := os.MkdirAll(renameShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	menuShot := filepath.Join(renameShotDir, "menu.png")
	if out, err := exec.Command("screencapture", "-x", menuShot).CombinedOutput(); err != nil {
		t.Fatalf("full-screen shot failed (%v): %s", err, out)
	}
	if fi, err := os.Stat(menuShot); err != nil || fi.Size() == 0 {
		t.Fatalf("menu screenshot missing: %v", err)
	}
	t.Logf("menu shot -> %s (tile at %.0f,%.0f)", menuShot, tileX, tileY)
	postEscape(t)

	// The rename window, opened by the same function the menu item calls.
	before := mac.FrontmostPID()
	var items []store.Item
	onMain(func() {
		items = a.visibleItems()
		a.openRename(items)
	})
	if a.rn == nil || a.rn.win == nil {
		t.Fatal("rename window was not created")
	}
	renameShot(t, a.rn.win, "form")

	// A conflict renders in red: an existing 旅行 1.jpg blocks the replace.
	if err := os.WriteFile(filepath.Join(dir, "旅行 1.jpg"), []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	onMain(func() {
		r := a.rn
		r.mode, r.find, r.replace = int(modeReplace), "照片", "旅行"
		r.win.Invalidate()
	})
	time.Sleep(400 * time.Millisecond)
	renameShot(t, a.rn.win, "conflict")
	if a.rn.plan().conflicts == 0 {
		t.Fatalf("want a conflict, plan=%+v", a.rn.plan().rows)
	}
	if err := os.Remove(filepath.Join(dir, "旅行 1.jpg")); err != nil {
		t.Fatal(err)
	}

	// 1) 添加文字：prefix everything (folder included), then undo.
	onMain(func() {
		r := a.rn
		r.mode, r.addText, r.addSeg = int(modeAdd), "新-", 0
		r.win.Invalidate()
	})
	time.Sleep(200 * time.Millisecond)
	onMain(func() { a.rn.apply() })
	if a.rn.done == nil {
		t.Fatalf("add apply failed: %s", a.rn.err)
	}
	want := []string{"新-照片 1.jpg", "新-照片 2.jpg", "新-说明.txt", "新-资料 夹", "新-报告.pdf"}
	sameSet(t, diskNames(t, dir), want, "add: disk")
	sameSet(t, trayNames(a), want, "add: tray")
	renameShot(t, a.rn.win, "done")

	// Closing must leave the 撤销 pill in the panel: shot it, then undo
	// from there (the panel pill outlives the window).
	if a.rn.undoUntil.Before(time.Now().Add(9 * time.Second)) {
		t.Fatalf("pill deadline too soon: %v", a.rn.undoUntil)
	}
	onMain(func() { a.rn.win.Close() })
	time.Sleep(500 * time.Millisecond)
	if a.rn.win != nil {
		t.Fatal("closing did not release the window")
	}
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.show(false)
		a.progress, a.animationFrom = 1, 1
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(700 * time.Millisecond)
	renameShot(t, a.win, "toast")
	onMain(func() { a.rn.undo() })
	sameSet(t, diskNames(t, dir), names, "add undo: disk")
	sameSet(t, trayNames(a), names, "add undo: tray")

	// 2) 名称加序号：custom name + two-digit numbers, then undo. The
	// window is opened again, which must keep working after a close.
	var folderItems []store.Item
	onMain(func() {
		for _, it := range a.visibleItems() {
			folderItems = append(folderItems, it)
		}
		a.openRename(folderItems)
	})
	if a.rn.win == nil {
		t.Fatal("re-opening after a close did not create the window")
	}
	onMain(func() {
		r := a.rn
		r.mode, r.fmtName, r.fmtStartText, r.digitsSeg, r.posSeg = int(modeFormat), "批次", "1", 1, 1
		r.win.Invalidate()
	})
	time.Sleep(200 * time.Millisecond)
	onMain(func() { a.rn.apply() })
	if a.rn.done == nil {
		t.Fatalf("format apply failed: %s", a.rn.err)
	}
	// The exact names are the plan's own rows, in tray order.
	var wantFormat []string
	onMain(func() {
		for _, row := range a.rn.plan().rows {
			wantFormat = append(wantFormat, row.newName)
		}
	})
	sameSet(t, diskNames(t, dir), wantFormat, "format: disk")
	sameSet(t, trayNames(a), wantFormat, "format: tray")
	for _, n := range wantFormat {
		if !strings.HasPrefix(n, "批次 0") {
			t.Fatalf("format name %q lacks the two-digit name", n)
		}
	}
	onMain(func() { a.rn.undo() })
	sameSet(t, diskNames(t, dir), names, "format undo: disk")
	sameSet(t, trayNames(a), names, "format undo: tray")

	// 3) 替换文字：photos only, then undo.
	onMain(func() {
		r := a.rn
		r.mode, r.find, r.replace = int(modeReplace), "照片", "图片"
		r.win.Invalidate()
	})
	time.Sleep(200 * time.Millisecond)
	onMain(func() { a.rn.apply() })
	if a.rn.done == nil {
		t.Fatalf("replace apply failed: %s", a.rn.err)
	}
	want = []string{"图片 1.jpg", "图片 2.jpg", "说明.txt", "资料 夹", "报告.pdf"}
	sameSet(t, diskNames(t, dir), want, "replace: disk")
	sameSet(t, trayNames(a), want, "replace: tray")
	onMain(func() { a.rn.undo() })
	sameSet(t, diskNames(t, dir), names, "replace undo: disk")
	sameSet(t, trayNames(a), names, "replace undo: tray")

	// Closing hands the keyboard back to the app the window came from.
	if before != 0 && before != mac.OwnPID() {
		onMain(func() { a.rn.win.Close() })
		back := false
		for range 40 {
			if mac.FrontmostPID() == before {
				back = true
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if !back {
			t.Fatalf("frontmost app after close = %d, want %d", mac.FrontmostPID(), before)
		}
	} else {
		t.Logf("skipping focus handback: frontmost before open was self (%d)", before)
	}
}
