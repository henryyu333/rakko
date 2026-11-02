package main

import (
	"testing"
	"time"
)

// feed offers samples at fixed 30 ms intervals starting from t0, in the
// same order the monitor loop polls them.
func feed(d *shakeDetector, xs []float64, step time.Duration) bool {
	t := time.Unix(0, 0)
	for _, x := range xs {
		if d.add(t, x) {
			return true
		}
		t = t.Add(step)
	}
	return false
}

// shake builds an alternating wiggle around x0: each leg covers travel
// points and lasts legs*step.
func shakeSeq(x0, travel float64, legs int) []float64 {
	xs := []float64{x0}
	x := x0
	dir := 1.0
	for range legs {
		x += dir * travel
		xs = append(xs, x)
		dir = -dir
	}
	return xs
}

func TestShakeDetectorTriggersOnRealShake(t *testing.T) {
	var d shakeDetector
	// Three reversals need four 25+ pt passes: 0→40→0→40→0 in 120 ms.
	if !feed(&d, shakeSeq(0, 40, 4), 30*time.Millisecond) {
		t.Fatal("a left-right wiggle did not trigger")
	}
}

func TestShakeDetectorTriggersWhenPassesAreMinimal(t *testing.T) {
	var d shakeDetector
	// Exactly shakeMinTravel per pass still counts.
	if !feed(&d, shakeSeq(100, shakeMinTravel, 4), 30*time.Millisecond) {
		t.Fatal("minimum-amplitude shake did not trigger")
	}
}

func TestShakeDetectorIgnoresSlowDrag(t *testing.T) {
	var d shakeDetector
	// A steady pull in one direction commits once and never reverses.
	xs := make([]float64, 30)
	for i := range xs {
		xs[i] = float64(i) * 30
	}
	if feed(&d, xs, 30*time.Millisecond) {
		t.Fatal("a straight drag triggered")
	}
}

func TestShakeDetectorIgnoresJitter(t *testing.T) {
	var d shakeDetector
	// ±2 pt noise around the cursor position: no pass covers 25 pt.
	xs := []float64{0, 2, -2, 2, -2, 2, -2, 2, -2, 2, -2, 2, -2, 2}
	if feed(&d, xs, 30*time.Millisecond) {
		t.Fatal("pointer jitter triggered")
	}
}

func TestShakeDetectorIgnoresVerticalMotion(t *testing.T) {
	var d shakeDetector
	// Vertical movement does not feed the detector at all: add() only sees
	// x, so a vertical drag is a static sample stream.
	xs := make([]float64, 20)
	if feed(&d, xs, 30*time.Millisecond) {
		t.Fatal("vertical motion triggered")
	}
}

func TestShakeDetectorIgnoresOneOvershoot(t *testing.T) {
	var d shakeDetector
	// Out and back once is two passes, one reversal — not a shake.
	if feed(&d, shakeSeq(0, 60, 2), 30*time.Millisecond) {
		t.Fatal("a single out-and-back triggered")
	}
	// Fresh detector: samples from a prior feed would still be in-window.
	var d2 shakeDetector
	if feed(&d2, shakeSeq(0, 60, 3), 30*time.Millisecond) {
		t.Fatal("two reversals triggered")
	}
}

func TestShakeDetectorWindowExpires(t *testing.T) {
	var d shakeDetector
	// Spread the passes over more than the window: old samples stop
	// counting before the third reversal lands.
	xs := shakeSeq(0, 40, 4)
	tm := time.Unix(0, 0)
	if d.add(tm, xs[0]) {
		t.Fatal("single sample triggered")
	}
	for i, x := range xs[1:] {
		if d.add(tm.Add(time.Duration(i+1)*300*time.Millisecond), x) {
			t.Fatalf("spread-out wiggle triggered at sample %d", i+1)
		}
	}
}

func TestShakeDetectorResetsAfterTrigger(t *testing.T) {
	var d shakeDetector
	if !feed(&d, shakeSeq(0, 40, 4), 30*time.Millisecond) {
		t.Fatal("first shake did not trigger")
	}
	// The same shake, replayed from the detector's cleared state, must not
	// keep firing on its tail samples alone.
	tm := time.Unix(0, 0)
	for _, x := range []float64{40, 0, 40} {
		if d.add(tm, x) {
			t.Fatal("detector re-triggered on leftover motion")
		}
		tm = tm.Add(30 * time.Millisecond)
	}
	// But a fresh full shake still triggers.
	if !feed(&d, shakeSeq(0, 40, 4), 30*time.Millisecond) {
		t.Fatal("detector never re-arms after reset")
	}
}

func TestShakeDetectorReversalNeedsTravel(t *testing.T) {
	var d shakeDetector
	// Counter-moves of 24 pt never credit a reversal even though the
	// direction flips each time and the passes themselves are 40 pt.
	xs := []float64{0, 40, 16, 40, 16, 40, 16}
	if feed(&d, xs, 30*time.Millisecond) {
		t.Fatal("a short return pass counted as a reversal")
	}
	// One point further and the same pattern is a shake.
	var d2 shakeDetector
	if !feed(&d2, []float64{0, 40, 15, 40, 15, 40, 15}, 30*time.Millisecond) {
		t.Fatal("a 25 pt return pass failed to credit a reversal")
	}
}
