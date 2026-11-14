//go:build windows

package platform

// Tests for the menu watch's geometry: where a system menu opened over the
// panel is moved. The monitor layout is handed in, so a monitor above the
// primary one (a work area with a negative top) is checked on a single-
// monitor machine.

import "testing"

// monitorAt builds the screenMonitor a test needs: bounds and work area in
// one rectangle each.
func monitorAt(boundsLeft, boundsTop, boundsRight, boundsBottom, workLeft, workTop, workRight, workBottom int32) screenMonitor {
	return screenMonitor{
		bounds: winRect{Left: boundsLeft, Top: boundsTop, Right: boundsRight, Bottom: boundsBottom},
		work:   winRect{Left: workLeft, Top: workTop, Right: workRight, Bottom: workBottom},
	}
}

func TestBesidePanelKeepsMenusInsideTheUpperMonitor(t *testing.T) {
	primary := monitorAt(0, 0, 1920, 1080, 0, 0, 1920, 1040)
	upper := monitorAt(0, -1080, 1920, 0, 0, -1080, 1920, 0)
	monitors := []screenMonitor{primary, upper}
	panel := winRect{Left: 630, Top: -1080, Right: 1291, Bottom: -380}

	// A menu opened at the very top of the monitor above the primary one
	// keeps the work area's 8pt band, not a band measured from zero.
	menu := winRect{Left: 800, Top: -1080, Right: 1022, Bottom: -1050}
	x, y := besidePanel(menu, panel, monitors)
	if y != -1072 {
		t.Fatalf("menu at the upper monitor's top: y = %d, want -1072", y)
	}
	if x != panel.Right+8 {
		t.Fatalf("menu beside the panel: x = %d, want %d", x, panel.Right+8)
	}

	// A menu at the bottom of that monitor is lifted above its work area's
	// bottom edge.
	menu = winRect{Left: 800, Top: -30, Right: 1022, Bottom: 30}
	if _, y = besidePanel(menu, panel, monitors); y != 0-8-60 {
		t.Fatalf("menu at the upper monitor's bottom: y = %d, want -68", y)
	}

	// Opened four points below that monitor's top edge, the eight-point
	// band pushes it back to -1072: the band belongs to the monitor's work
	// area, not to the origin of the desktop.
	menu = winRect{Left: 800, Top: -1076, Right: 1022, Bottom: -1046}
	if _, y = besidePanel(menu, panel, monitors); y != -1072 {
		t.Fatalf("menu opened near the upper monitor's top: y = %d, want -1072", y)
	}
}

func TestBesidePanelLeavesAnUnplacedMenuAlone(t *testing.T) {
	monitors := []screenMonitor{monitorAt(0, 0, 1920, 1080, 0, 0, 1920, 1040)}
	panel := winRect{Left: 630, Top: 0, Right: 1291, Bottom: 700}
	// The menu claims a point no monitor holds: nothing is known about its
	// work area, so it keeps the height the system gave it.
	menu := winRect{Left: 2500, Top: 4, Right: 2722, Bottom: 34}
	x, y := besidePanel(menu, panel, monitors)
	if x != menu.Left || y != menu.Top {
		t.Fatalf("unplaced menu moved to %d,%d, want %d,%d", x, y, menu.Left, menu.Top)
	}
}

func TestBesidePanelFallsBackToTheLeftSide(t *testing.T) {
	monitors := []screenMonitor{monitorAt(0, 0, 1920, 1080, 0, 0, 1920, 1040)}
	// A panel against the right edge leaves no room for a 222pt menu.
	panel := winRect{Left: 1600, Top: 0, Right: 1880, Bottom: 700}
	menu := winRect{Left: 1650, Top: 200, Right: 1872, Bottom: 447}
	x, y := besidePanel(menu, panel, monitors)
	if want := panel.Left - 8 - menu.width(); x != want {
		t.Fatalf("menu on the left side: x = %d, want %d", x, want)
	}
	if y != menu.Top {
		t.Fatalf("menu y changed to %d, want %d", y, menu.Top)
	}
	// The desktop work area covers the panel's right side too, so the menu
	// stays put when neither side has room.
	panel = winRect{Left: 8, Top: 0, Right: 1912, Bottom: 700}
	x, _ = besidePanel(menu, panel, monitors)
	if x != menu.Left {
		t.Fatalf("menu with no room either side: x = %d, want %d", x, menu.Left)
	}
}
