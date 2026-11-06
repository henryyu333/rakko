//go:build darwin

package mac

import "testing"

func TestClampDragPointProtectsNotchCentre(t *testing.T) {
	region := DragGuardRegion{X: 580, Y: 0, Width: 280, Height: 40}
	for _, tt := range []struct {
		name     string
		in, want CGPoint
	}{
		{"centre at top", CGPoint{720, 0}, CGPoint{720, 40}},
		{"left inside edge", CGPoint{580, 1}, CGPoint{580, 40}},
		{"last inside column", CGPoint{859.5, 0}, CGPoint{859.5, 40}},
		{"safe landing", CGPoint{720, 40}, CGPoint{720, 40}},
		{"below corridor", CGPoint{720, 300}, CGPoint{720, 300}},
		{"outside left", CGPoint{579.5, 0}, CGPoint{579.5, 0}},
		{"outside right", CGPoint{860, 0}, CGPoint{860, 0}},
		{"different screen above", CGPoint{720, -1}, CGPoint{720, -1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampDragPoint(tt.in, region); got != tt.want {
				t.Fatalf("drag point %+v became %+v; want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestClampDragPointOnOffsetDisplay(t *testing.T) {
	region := DragGuardRegion{X: -1000, Y: -1080, Width: 280, Height: 40}
	if got := clampDragPoint(CGPoint{-860, -1080}, region); got != (CGPoint{-860, -1040}) {
		t.Fatalf("secondary-screen centre not protected: %+v", got)
	}
	for _, p := range []CGPoint{{720, 0}, {-860, 0}, {-860, -1081}, {-1100, -1080}} {
		if got := clampDragPoint(p, region); got != p {
			t.Fatalf("point on another screen/region changed: %+v -> %+v", p, got)
		}
	}
}

func TestSetDragGuardRegionNoopWhenDisarmed(t *testing.T) {
	SetDragGuardRegion(DragGuardRegion{X: 580, Y: 0, Width: 280, Height: 40})
}

func TestInvalidDragGuardRegionDoesNotAlterInput(t *testing.T) {
	p := CGPoint{720, 0}
	for _, r := range []DragGuardRegion{{}, {X: 580, Width: 280}, {X: 580, Height: 40}, {Width: -1, Height: 40}} {
		if got := clampDragPoint(p, r); got != p {
			t.Fatalf("invalid region changed input: %+v -> %+v", p, got)
		}
		stop, err := StartDragGuard(r)
		if err == nil || stop != nil {
			t.Fatal("invalid geometry must return a setup failure, not claim protection")
		}
	}
}
