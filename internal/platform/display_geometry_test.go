//go:build darwin

package platform

import "testing"

// notchRectFor is the pure geometry behind NotchRect. These cases replay
// NSScreen values for synthetic multi-display layouts — including displays
// whose frames sit at negative coordinates or below the primary — where a
// wrong sign in the flip would teleport the interactive notch strip.
// Auxiliary top areas are screen-local (bottom-left origin); this Mac's
// 1470×956 built-in reports auxL (0,924,645.5,32) / auxR (824.5,924,645.5,32).
var auxL, auxR = NSRect{Size: NSSize{Width: 645.5, Height: 32}},
	NSRect{Origin: NSPoint{X: 824.5}, Size: NSSize{Width: 645.5, Height: 32}}

func TestNotchRectForPrimaryNotchedPanel(t *testing.T) {
	frame := NSRect{Size: NSSize{Width: 1470, Height: 956}}
	x, y, w, h, has := notchRectFor(frame, NSEdgeInsets{Top: 32}, auxL, auxR, 956)
	if x != 646 || y != 0 || w != 180 || h != 32 || !has {
		t.Fatalf("built-in notch rect = %d,%d %d×%d hasNotch=%v, want 646,0 180×32 true", x, y, w, h, has)
	}
}

func TestNotchRectForNotchedDisplayAsSecondary(t *testing.T) {
	// Clamshell-style layout: the external 2560×1440 panel is the main screen
	// (menu bar, AppKit origin), the notched built-in sits below it so its
	// frame origin is (0,-956) in AppKit space and y=1440 in global top-left
	// space.
	frame := NSRect{Origin: NSPoint{X: 0, Y: -956}, Size: NSSize{Width: 1470, Height: 956}}
	x, y, w, h, has := notchRectFor(frame, NSEdgeInsets{Top: 32}, auxL, auxR, 1440)
	if x != 646 || y != 1440 || w != 180 || h != 32 || !has {
		t.Fatalf("secondary notched rect = %d,%d %d×%d hasNotch=%v, want 646,1440 180×32 true", x, y, w, h, has)
	}
}

func TestNotchRectForNotchlessSecondaryDisplays(t *testing.T) {
	// External displays report no auxiliary areas; the fallback must stay a
	// 180×32 centred box anchored to that display's top edge — negative
	// coordinates on displays above or left of the primary included.
	cases := []struct {
		name     string
		frame    NSRect
		primaryH float64
		want     [4]int
	}{
		{"external primary", NSRect{Size: NSSize{Width: 2560, Height: 1440}}, 1440, [4]int{1190, 0, 180, 32}},
		{"secondary right, tops aligned", NSRect{Origin: NSPoint{X: 1470, Y: -484}, Size: NSSize{Width: 2560, Height: 1440}}, 956, [4]int{2660, 0, 180, 32}},
		{"secondary left, tops aligned", NSRect{Origin: NSPoint{X: -1920, Y: 956 - 1080}, Size: NSSize{Width: 1920, Height: 1080}}, 956, [4]int{-1050, 0, 180, 32}},
		{"secondary directly above", NSRect{Origin: NSPoint{X: 0, Y: 956}, Size: NSSize{Width: 3840, Height: 2160}}, 956, [4]int{1830, -2160, 180, 32}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			x, y, w, h, has := notchRectFor(tt.frame, NSEdgeInsets{}, NSRect{}, NSRect{}, tt.primaryH)
			if has {
				t.Fatal("display without a top safe-area inset must not claim a hardware notch")
			}
			if got := [4]int{x, y, w, h}; got != tt.want {
				t.Fatalf("fallback notch = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNotchRectForNarrowOrMissingAuxAreas(t *testing.T) {
	// A camera housing narrower than 120pt (or auxiliary areas reported by a
	// notch-less panel that lost its insets) must not yield a skewed anchor:
	// the centred fallback wins instead of trusting a degenerate gap.
	frame := NSRect{Size: NSSize{Width: 1470, Height: 956}}
	narrowL := NSRect{Size: NSSize{Width: 700, Height: 32}}
	narrowR := NSRect{Origin: NSPoint{X: 800}, Size: NSSize{Width: 670, Height: 32}}
	x, _, w, h, has := notchRectFor(frame, NSEdgeInsets{Top: 32}, narrowL, narrowR, 956)
	if x != 645 || w != 180 || h != 32 || !has {
		t.Fatalf("narrow gap rect = %d,_ %d×%d hasNotch=%v, want 645,_ 180×32 true", x, w, h, has)
	}
}

// windowFrameFor / windowTopLeftFor own the single top-left→bottom-left flip
// in SetWindowFrame. The conversion must stay exact when y is negative (a
// display above the primary) or x is negative (a display to its left).
func TestWindowFrameForSecondaryDisplays(t *testing.T) {
	cases := []struct {
		name    string
		x, y    int
		screenH float64
		wantOrg NSPoint
	}{
		{"primary top strip", 595, 0, 956, NSPoint{X: 595, Y: 956 - 72}},
		{"external primary", 1190, 0, 1440, NSPoint{X: 1190, Y: 1440 - 72}},
		{"secondary below", 646, 1440, 1440, NSPoint{X: 646, Y: -72}},
		{"secondary above", 1830, -1204, 956, NSPoint{X: 1830, Y: 956 + 1204 - 72}},
		{"secondary left", -1050, 0, 956, NSPoint{X: -1050, Y: 956 - 72}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := windowFrameFor(tt.x, tt.y, 280, 72, tt.screenH)
			if r.Origin != tt.wantOrg || r.Size != (NSSize{Width: 280, Height: 72}) {
				t.Fatalf("frame = %+v, want origin %+v size 280×72", r, tt.wantOrg)
			}
			if p := windowTopLeftFor(tt.x, tt.y, tt.screenH); p.Y-72 != r.Origin.Y || p.X != r.Origin.X {
				t.Fatalf("top-left pin %+v disagrees with frame %+v", p, r)
			}
		})
	}
}
