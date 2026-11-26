//go:build windows

package main

// The island's own rules, shared with the Mac build, on Windows' simulated
// housing: every display carries an island unless the user picked the
// notch style, and a resting capsule on another screen sits on the row the
// resident shell would take there. The keyboard band waits for the shell
// to clear the taskbar the same way it waits for the Mac's menu bar.

import (
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/platform"
	"rakko/internal/settings"
)

// windowsIslandShelf is a headless shelf on a 1920 × 1080 display whose
// work area starts below a 32pt band, with the island style on.
func windowsIslandShelf() *shelf {
	s := settings.Default()
	s.ShellStyle = settings.ShellStyleIsland
	a := &shelf{
		dispBounds: mygo.Rectangle{Width: 1920, Height: 1080},
		dispWork:   mygo.Rectangle{Y: 32, Width: 1920, Height: 1048},
		notchX:     870, notchY: 0, notchW: 180, notchH: 32, topInset: 32,
		x:            (1920 - sheetW) / 2,
		settings:     s,
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
	}
	a.applyShellStyle()
	return a
}

// A display with the simulated housing gets a resting capsule only in the
// island style, on the row below that housing; a bare display gets one at
// the island's 7pt row.
func TestIdleCapsulesOnWindowsFollowTheStyle(t *testing.T) {
	displays := []islandDisplay{
		{ID: 1, Bounds: mygo.Rectangle{Width: 1920, Height: 1080}, HasNotch: true, NotchH: 32},
		{ID: 2, Bounds: mygo.Rectangle{X: 1920, Width: 2560, Height: 1440}},
	}
	// Island style: both screens draw the island; the simulated housing
	// puts its resting capsule 6pt under the 32pt band.
	got := idleIslandPlacements(displays, 2, settings.ShellStyleIsland, nil)
	if len(got) != 1 || got[0].DisplayID != 1 {
		t.Fatalf("island style placements = %+v, want display 1's", got)
	}
	if f := got[0].Frame; f.Y != 32+islandNotchGap {
		t.Fatalf("simulated-housing capsule row %d, want %d", f.Y, 32+islandNotchGap)
	}
	// Notch style: the simulated housing is the shell, so no capsule.
	if got := idleIslandPlacements(displays, 2, settings.ShellStyleNotch, nil); len(got) != 0 {
		t.Fatalf("notch style placements = %+v, want none", got)
	}
	// Resident on the simulated-housing screen: the bare one keeps the
	// island's own row.
	got = idleIslandPlacements(displays, 1, settings.ShellStyleIsland, nil)
	if len(got) != 1 || got[0].DisplayID != 2 || got[0].Frame.Y != islandTopGap {
		t.Fatalf("resident on 1: placements = %+v", got)
	}
}

// The keyboard band would put the taskbar over the island's top until the
// shell's own top reaches it; the band waits for that.
func TestKeyboardBandWaitsOnWindows(t *testing.T) {
	a := windowsIslandShelf()
	a.focusMode = true
	a.fx.focusK = 0
	if got := a.windowLevel(); got != platform.LayerStatus {
		t.Fatalf("capsule row (top %d) took the keyboard band %v", a.anchorScreenTop(), got)
	}
	a.fx.focusK = 1
	if got := a.windowLevel(); got != platform.LayerSearchKeyboard {
		t.Fatalf("settled search bar (top %d) stayed out of the keyboard band %v",
			a.anchorScreenTop()+a.focusDropNow(), got)
	}
	// The simulated notch style turns the housing on: the shell starts at
	// the display's top edge and takes the keyboard band at once.
	a.settings.ShellStyle = settings.ShellStyleNotch
	a.hasNotch = true
	a.applyShellStyle()
	a.fx.focusK = 0
	if got := a.windowLevel(); got != platform.LayerSearchKeyboard {
		t.Fatalf("notch style level %v, want the keyboard band", got)
	}
}

// A taskbar docked at the top shrinks the work area from the display's top
// edge; the island rests below it instead of covering it, and a place saved
// above the taskbar is pulled back down.
func TestIslandRestsBelowATopTaskbar(t *testing.T) {
	a := windowsIslandShelf()
	a.placeIslandAxis()
	if got, want := a.islandRect().Y, 32+islandTopGap; got != want {
		t.Fatalf("island row %d, want %d below the taskbar", got, want)
	}
	a.settings.IslandPos = map[string]settings.IslandPos{islandSpotKey(a.dispID): {X: 300, Y: 5}}
	a.placeIslandAxis()
	if got, want := a.islandRect().Y, 32+islandTopGap; got != want {
		t.Fatalf("a place saved above the taskbar was kept: row %d, want %d", got, want)
	}
}
