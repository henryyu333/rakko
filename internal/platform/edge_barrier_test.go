package platform

import "testing"

func testBarrierRegion() DragGuardRegion {
	return DragGuardRegion{
		X: 620, Y: 0, Width: 230, Height: 37,
		BarrierEnabled: true,
		BarrierX:       645,
		BarrierY:       0,
		BarrierWidth:   180,
		BarrierPushPt:  80,
	}
}

func TestEdgeBarrierStopsThenReleasesAfter80pt(t *testing.T) {
	r := testBarrierRegion()
	var s EdgeGuardState

	// Approach from below at three speeds: initial overshoot stops at BarrierY
	// and never leaks into push accumulation.
	for _, firstY := range []float64{-20, -60, -150} {
		s = EdgeGuardState{}
		if nx, ny, ch := s.Eval(735, 100, r, false, false, false, true); ch || nx != 735 || ny != 100 {
			t.Fatalf("below edge modified point: (%v,%v) ch=%v", nx, ny, ch)
		}
		nx, ny, ch := s.Eval(735, firstY, r, false, false, false, true)
		if !ch || nx != 735 || ny != 0 {
			t.Fatalf("first crossing y=%v gave (%v,%v) ch=%v; want clamp to (735,0)", firstY, nx, ny, ch)
		}
		// Three 20pt pushes (60pt total) keep clamping; fourth reaches 80pt and releases.
		for i := range 3 {
			nx, ny, ch = s.Eval(735, -20, r, false, false, false, true)
			if !ch || ny != 0 {
				t.Fatalf("step %d released early at 60pt or less: (%v,%v) ch=%v", i+1, nx, ny, ch)
			}
		}
		nx, ny, ch = s.Eval(735, -20, r, false, false, false, true)
		if ch || nx != 735 || ny != -20 {
			t.Fatalf("fourth 20pt push (80pt total) stayed clamped: (%v,%v) ch=%v", nx, ny, ch)
		}
		// Moving further up on the upper display stays unblocked.
		if nx, ny, ch = s.Eval(735, -150, r, false, false, false, true); ch || ny != -150 {
			t.Fatalf("upper display re-clamped after release: (%v,%v) ch=%v", nx, ny, ch)
		}
	}
}

func TestEdgeBarrierReArmsAfterReturningBelowMargin(t *testing.T) {
	r := testBarrierRegion()
	var s EdgeGuardState
	s.Eval(735, 60, r, false, false, false, true)
	if _, ny, ch := s.Eval(735, -10, r, false, false, false, true); !ch || ny != 0 {
		t.Fatalf("initial stop failed: ny=%v ch=%v", ny, ch)
	}
	if _, _, ch := s.Eval(735, -80, r, false, false, false, true); ch {
		t.Fatal("80pt push did not release")
	}
	// Slight dip back near BarrierY within reset margin does not re-arm prematurely.
	if _, _, ch := s.Eval(735, 10, r, false, false, false, true); ch {
		t.Fatal("pointer within reset margin triggered unexpected clamp")
	}
	// Returning cleanly below BarrierY+20 re-arms the barrier.
	s.Eval(735, 30, r, false, false, false, true)
	if _, ny, ch := s.Eval(735, -25, r, false, false, false, true); !ch || ny != 0 {
		t.Fatalf("re-armed barrier did not stop at top edge: ny=%v ch=%v", ny, ch)
	}
}

func TestEdgeBarrierLeavesSidesAndUpperMovesUntouched(t *testing.T) {
	r := testBarrierRegion()
	var s EdgeGuardState

	// Outside anchor horizontal span [645, 825): never blocks.
	for _, x := range []float64{644, 825, 100, 1400} {
		s = EdgeGuardState{}
		s.Eval(x, 60, r, false, false, false, true)
		if _, _, ch := s.Eval(x, -40, r, false, false, false, true); ch {
			t.Fatalf("x=%v outside anchor was blocked", x)
		}
	}

	// Sliding horizontally onto the anchor while already high on the upper
	// display must never yank the pointer down onto BarrierY.
	s = EdgeGuardState{}
	s.Eval(300, 60, r, false, false, false, true)
	s.Eval(300, -250, r, false, false, false, true)
	if nx, ny, ch := s.Eval(735, -250, r, false, false, false, true); ch || nx != 735 || ny != -250 {
		t.Fatalf("upper-display horizontal entry was yanked: (%v,%v) ch=%v", nx, ny, ch)
	}

	// Sliding horizontally off the notch while clamped releases cleanly.
	s = EdgeGuardState{}
	s.Eval(735, 50, r, false, false, false, true)
	if _, _, ch := s.Eval(735, -10, r, false, false, false, true); !ch {
		t.Fatal("did not clamp at top")
	}
	if nx, ny, ch := s.Eval(640, -10, r, false, false, false, true); ch || nx != 640 || ny != -10 {
		t.Fatalf("sliding off anchor side stayed blocked: (%v,%v) ch=%v", nx, ny, ch)
	}
	// Moving back onto anchor while still on upper display does not re-grab.
	if _, _, ch := s.Eval(660, -10, r, false, false, false, true); ch {
		t.Fatal("sliding back onto anchor above BarrierY re-grabbed pointer")
	}
}

func TestEdgeBarrierDistinguishesFileDragFromWindowDrag(t *testing.T) {
	r := testBarrierRegion()
	var s EdgeGuardState

	// Non-file held drag (window drag) passes straight across AND is never
	// clamped by the legacy notch corridor while an upper display sits above.
	s.Eval(735, 60, r, false, true, false, true)
	if _, _, ch := s.Eval(735, 10, r, false, true, false, true); ch {
		t.Fatal("window drag inside notch corridor was clamped with upper display present")
	}
	if _, _, ch := s.Eval(735, -30, r, false, true, false, true); ch {
		t.Fatal("window drag across upper display junction was blocked")
	}

	// File drag IS blocked at BarrierY and releases after 80pt push.
	s = EdgeGuardState{}
	s.Eval(735, 60, r, false, true, true, true)
	if _, ny, ch := s.Eval(735, -15, r, false, true, true, true); !ch || ny != 0 {
		t.Fatalf("file drag did not stop at junction: ny=%v ch=%v", ny, ch)
	}
	if _, _, ch := s.Eval(735, -50, r, false, true, true, true); !ch {
		t.Fatal("file drag released at 50pt (<80pt)")
	}
	if nx, ny, ch := s.Eval(735, -30, r, false, true, true, true); ch || nx != 735 || ny != -30 {
		t.Fatalf("file drag did not release at 80pt: (%v,%v) ch=%v", nx, ny, ch)
	}
}

func TestEdgeBarrierRespectsCorridorAndParkedModes(t *testing.T) {
	// Without an upper display: corridorEnabled preserves Mac drop-corridor
	// clamping for drags, while disabling it (Windows) leaves cursor untouched.
	noBarrier := DragGuardRegion{X: 580, Y: 0, Width: 280, Height: 40}
	var s EdgeGuardState
	if nx, ny, ch := s.Eval(720, 10, noBarrier, false, true, false, true); !ch || nx != 720 || ny != 40 {
		t.Fatalf("mac single-display drag corridor broke: (%v,%v) ch=%v", nx, ny, ch)
	}
	if _, _, ch := s.Eval(720, 10, noBarrier, false, false, false, true); ch {
		t.Fatal("hover was blocked without an upper display")
	}
	if _, _, ch := s.Eval(720, 10, noBarrier, false, true, false, false); ch {
		t.Fatal("windows no-barrier drag altered cursor")
	}

	// Parked state resets active barrier and passes everything through.
	r := testBarrierRegion()
	s.Eval(735, 60, r, false, false, false, true)
	s.Eval(735, -10, r, false, false, false, true)
	if !s.Active() {
		t.Fatal("expected barrier to be active")
	}
	if _, _, ch := s.Eval(735, -10, r, true, false, false, true); ch || s.Active() {
		t.Fatalf("parked guard still clamped or remained active: ch=%v active=%v", ch, s.Active())
	}
}
