//go:build darwin

package main

// The ⌃⌥V flow on a real window: openClipboardSearch activates the app so
// the notch window becomes the key window and the search field takes the
// keyboard; hiding hands the keyboard back to the app the user came from.
// Headless, RAKKO_BASKET_SMOKE=1 builds the real app loop (see
// basket_test.go); RAKKO_CLIP_HOLD=1 keeps the panel up so an external
// `screencapture -l <id>` can shoot the live window.

import (
	"os"
	"strconv"
	"testing"
	"time"

	"rakko/internal/mac"
)

func TestSearchOpenFocusesWindow(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := smokeShelf
	if a.win == nil {
		onMain(func() { a.newNotchWindow() })
	}
	if a.win == nil {
		t.Fatal("no notch window")
	}
	prev := 0
	onMain(func() { prev = mac.FrontmostPID() })
	t.Logf("frontmost before open: pid=%d", prev)
	if prev == mac.OwnPID() {
		t.Skip("ShelfU is already frontmost; run the test from another app's session")
	}
	// Seed a couple of captures so the clipboard tab renders rows.
	onMain(func() {
		_ = a.store.AddClipText("seeded from Terminal", "com.apple.Terminal")
		_ = a.store.AddClipText("second capture", "com.apple.Safari")
		_ = a.store.SetPinned(a.store.Items[0].ID, true)
	})
	onMain(func() { a.openClipboardSearch() })
	// Activation is delivered through the WindowServer; poll briefly. The
	// harness has no monitoring ticker, so the open animation is stepped
	// by hand — without it progress stays 0 and the shell keeps the pill.
	key := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		onMain(func() {
			a.animate(time.Now())
			key = mac.IsKeyWindow(a.win.NativeHandle())
		})
		if key {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("keyWindow=%v isKeyWindow=%v visible=%v focusMode=%v tab=%s prevPID=%d",
		key, a.win.IsFocused(), a.win.IsVisible(), a.focusMode, a.tab, a.prevAppPID)
	if !a.focusMode || a.tab != pageClipboard || a.search != "" {
		t.Fatalf("open-for-search state wrong: focusMode=%v tab=%s search=%q", a.focusMode, a.tab, a.search)
	}
	if a.prevAppPID != prev {
		t.Fatalf("previous app not remembered: %d, want %d", a.prevAppPID, prev)
	}
	if !key {
		t.Fatal("the notch window did not become the key window; typing would go nowhere")
	}

	// IME evidence: the window must sit below kCGDockWindowLevel while the
	// search field has the keyboard, and the rect the candidate panel
	// anchors to must be a real caret inside the window — if either is
	// wrong, candidates are drawn behind the opaque panel or off-screen.
	level := 0
	onMain(func() { level = mac.WindowLevel(a.win.NativeHandle()) })
	if level != mac.SearchKeyboardLevel {
		t.Fatalf("focus-mode window level = %d, want %d (below the IM band at 20)", level, mac.SearchKeyboardLevel)
	}
	t.Logf("firstResponder=%s windowLevel=%d (IM candidate band=20, menubar=24, status=25)",
		mac.FirstResponderClass(a.win.NativeHandle()), level)
	var frame, caret mac.NSRect
	var haveCaret bool
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		onMain(func() {
			frame = mac.WindowFrame(a.win.NativeHandle())
			caret, haveCaret = mac.IMECompositionRect(a.win.NativeHandle(), 0, 0)
		})
		if haveCaret {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !haveCaret {
		t.Fatal("firstRectForCharacterRange returned no rect; the candidate panel has nowhere to anchor")
	}
	t.Logf("windowFrame=%+v caretScreenRect=%+v (AppKit bottom-left coords)", frame, caret)
	if caret.Origin.Y < frame.Origin.Y || caret.Origin.Y+caret.Size.Height > frame.Origin.Y+frame.Size.Height ||
		caret.Origin.X < frame.Origin.X || caret.Origin.X > frame.Origin.X+frame.Size.Width {
		t.Fatalf("caret rect %+v not inside window frame %+v — candidates anchor outside the window", caret, frame)
	}
	if caret.Origin.Y+caret.Size.Height > frame.Origin.Y+frame.Size.Height-33 {
		t.Logf("note: caret sits in the strip the menubar covers while the window is below level 24")
	}

	if num := mac.WindowNumber(a.win.NativeHandle()); num != 0 {
		t.Logf("shelf CGWindowID=%d — screencapture -l %d shoots the panel", num, num)
		// Leave the number where a shell watcher can pick it up while the
		// hold below keeps the panel on screen.
		_ = os.WriteFile("/tmp/shelf-cgwindowid.txt", []byte(strconv.Itoa(num)), 0o600)
	}
	if os.Getenv("RAKKO_CLIP_HOLD") != "" {
		time.Sleep(8 * time.Second)
	}
	// Hide hands the keyboard back to where the user came from. The view
	// state (focus mode) survives the 120 ms content fade, then clears.
	onMain(func() { a.hide(hideExplicit) })
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if !a.focusMode {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if a.focusMode || a.prevAppPID != 0 {
		t.Fatalf("hide kept keyboard state: focusMode=%v prevAppPID=%d", a.focusMode, a.prevAppPID)
	}
	restored := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		onMain(func() {
			restored = !mac.AppActive() && mac.FrontmostPID() == prev
		})
		if restored {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !restored {
		t.Fatalf("the previous app %d was not re-activated on hide", prev)
	}
	restoredLevel := 0
	onMain(func() { restoredLevel = mac.WindowLevel(a.win.NativeHandle()) })
	if restoredLevel != mac.StatusWindowLevel {
		t.Fatalf("window level after hide = %d, want %d — mouse flows keep the status level", restoredLevel, mac.StatusWindowLevel)
	}
	t.Logf("focus restored to pid=%d", prev)
}
