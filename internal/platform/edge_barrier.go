package platform

// EdgeBarrierPushPt is the accumulated upward travel (in points) required
// while stopped at the top-edge anchor junction before releasing the pointer
// onto the upper display.
const EdgeBarrierPushPt = 80

// edgeBarrierResetMargin is how far below the junction the pointer must
// return before a released (or slid-off) barrier arms again.
const edgeBarrierResetMargin = 20

// DragGuardRegion describes the anchor guard zone in logical screen
// coordinates.
//
// X, Y, Width, Height bound the notch/island drop corridor on macOS.
// When BarrierEnabled is true, another display sits directly above the
// horizontal top-edge segment [BarrierX, BarrierX+BarrierWidth) along
// BarrierY, and upward crossings stop at BarrierY until BarrierPushPt
// points of upward travel accumulate.
type DragGuardRegion struct {
	X, Y, Width, Height int

	BarrierEnabled     bool
	BarrierX           int
	BarrierY           int
	BarrierWidth       int
	BarrierPushPt      int
	BarrierResetMargin int
}

// EdgeGuardState is the shared state machine for top-edge protection across
// macOS (CGEventTap / drag pin) and Windows (WH_MOUSE_LL). Zero value is
// ready to use; callers synchronize concurrent access when needed.
type EdgeGuardState struct {
	hasPrev bool
	prevY   float64
	clamped bool
	passed  bool
	accum   float64
}

// Seed initializes the last-known pointer Y without crossing checks, e.g. when
// arming the filter while the pointer already rests on an upper display.
func (s *EdgeGuardState) Seed(y float64) {
	s.hasPrev = true
	s.prevY = y
	s.clamped = false
	s.passed = false
	s.accum = 0
}

// Reset clears active barrier stop/release progress while remembering whether
// the last seen pointer was already above the junction.
func (s *EdgeGuardState) Reset() {
	s.clamped = false
	s.passed = false
	s.accum = 0
}

// Active reports whether the barrier currently holds or has recently released
// an upward crossing near the junction.
func (s *EdgeGuardState) Active() bool {
	return s.clamped || s.passed
}

// Passed reports whether the barrier recently released onto the upper display
// and is waiting for the pointer to return cleanly below the junction.
func (s *EdgeGuardState) Passed() bool {
	return s.passed
}

// Eval evaluates one pointer event at (x, y) against region r and returns the
// adjusted coordinates and whether the pointer should be clamped.
//
// When r.BarrierEnabled is true:
//   - Held non-file drags (leftDown && !fileDrag, e.g. window moves) never clamp
//     or block so moving windows to the upper display stays seamless.
//   - Ordinary moves (!leftDown) and file drags (leftDown && fileDrag) moving
//     from the lower display into [BarrierX, BarrierX+BarrierWidth) above
//     BarrierY stop first at BarrierY, then release after BarrierPushPt of
//     further upward push.
//
// When r.BarrierEnabled is false:
//   - If corridorEnabled && leftDown, legacy notch drop-corridor clamping
//     applies (macOS single-screen / side-by-side Spaces protection).
func (s *EdgeGuardState) Eval(x, y float64, r DragGuardRegion, parked, leftDown, fileDrag, corridorEnabled bool) (nx, ny float64, changed bool) {
	if parked {
		s.Reset()
		s.hasPrev = true
		s.prevY = y
		return x, y, false
	}

	if r.BarrierEnabled {
		if leftDown && !fileDrag {
			s.Reset()
			s.hasPrev = true
			s.prevY = y
			return x, y, false
		}

		pushPt := float64(r.BarrierPushPt)
		if pushPt <= 0 {
			pushPt = EdgeBarrierPushPt
		}
		resetMargin := float64(r.BarrierResetMargin)
		if resetMargin <= 0 {
			resetMargin = edgeBarrierResetMargin
		}

		topY := float64(r.BarrierY)
		if y > topY+resetMargin {
			s.Reset()
			s.hasPrev = true
			s.prevY = y
			return x, y, false
		}
		if s.passed {
			s.hasPrev = true
			s.prevY = y
			return x, y, false
		}

		inSeg := r.BarrierWidth > 0 && x >= float64(r.BarrierX) && x < float64(r.BarrierX+r.BarrierWidth)
		if !s.clamped {
			fromLower := !s.hasPrev && y >= topY || (s.hasPrev && s.prevY >= topY)
			if !inSeg || y >= topY || !fromLower {
				s.hasPrev = true
				s.prevY = y
				return x, y, false
			}
			// First crossing from below into y < topY: stop on the top edge
			// without counting the initial overshoot toward pushPt.
			s.clamped = true
			s.accum = 0
			s.hasPrev = true
			s.prevY = topY
			return x, topY, true
		}

		if !inSeg {
			// Slid horizontally past the anchor segment: release without
			// re-grabbing until the pointer returns to the lower display.
			s.Reset()
			s.hasPrev = true
			s.prevY = y
			return x, y, false
		}

		if y < topY {
			s.accum += topY - y
			if s.accum >= pushPt {
				s.clamped = false
				s.passed = true
				s.accum = 0
				s.hasPrev = true
				s.prevY = y
				return x, y, false
			}
			s.hasPrev = true
			s.prevY = topY
			return x, topY, true
		}

		s.hasPrev = true
		s.prevY = y
		return x, y, false
	}

	s.Reset()
	s.hasPrev = true
	s.prevY = y
	if !corridorEnabled || !leftDown {
		return x, y, false
	}
	return clampCorridorPoint(x, y, r)
}

func clampCorridorPoint(x, y float64, r DragGuardRegion) (nx, ny float64, changed bool) {
	if r.Width <= 0 || r.Height <= 0 || x < float64(r.X) || x >= float64(r.X+r.Width) ||
		y < float64(r.Y) || y >= float64(r.Y+r.Height) {
		return x, y, false
	}
	return x, float64(r.Y + r.Height), true
}
