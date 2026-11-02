package main

import "time"

// Pure logic: the shake-to-summon detector has no platform dependency, so
// it is unit-tested with synthetic cursor samples.

const (
	// shakeReversals is how many left-right direction changes a shake needs.
	shakeReversals = 3
	// shakeMinTravel is the horizontal distance each direction pass must
	// cover for a reversal to count; pointer jitter never reaches it.
	shakeMinTravel = 25
	// shakeWindow is how far back a shake can reach; samples older than
	// this no longer contribute.
	shakeWindow = 600 * time.Millisecond
	// shakeDeadband is the slack inside a pass: x must move this far
	// beyond the furthest point before a pass extends or can turn. It
	// absorbs pixel jitter without resetting the committed direction.
	shakeDeadband = 4.0
)

// shakeDetector watches cursor samples during a system file drag and
// reports a shake: at least shakeReversals horizontal direction changes
// where every pass covers at least shakeMinTravel points, all inside
// shakeWindow.
//
// Travel is measured as direction runs. A run is committed once it covers
// shakeMinTravel; a reversal is credited when the pointer then moves
// shakeMinTravel against it — so the pass ending at each credited
// reversal, and the return pass that credits it, each covered the minimum.
// A real "wiggle" (fast L-R-L-R-L) triggers; slow drags, small jitter and
// vertical motion never commit a direction. add returns true once per
// shake: triggering resets the detector so the same shake cannot
// re-report, and the caller decides when a new shake may arm.
type shakeDetector struct {
	samples []shakeSample
}

type shakeSample struct {
	t time.Time
	x float64
}

// add folds one cursor sample into the detector and reports whether it
// completes a shake. It is cheap at the 30 ms poll rate: the window holds
// ~20 samples and the rescan is linear.
func (d *shakeDetector) add(t time.Time, x float64) bool {
	cutoff := t.Add(-shakeWindow)
	for len(d.samples) > 0 && !d.samples[0].t.After(cutoff) {
		d.samples = d.samples[1:]
	}
	d.samples = append(d.samples, shakeSample{t, x})
	if d.reversed() >= shakeReversals {
		d.reset()
		return true
	}
	return false
}

// reversed re-derives the run state from the samples inside the window and
// returns the credited reversal count.
func (d *shakeDetector) reversed() int {
	if len(d.samples) == 0 {
		return 0
	}
	dir, n := 0, 0
	runStart, runMax := d.samples[0].x, d.samples[0].x
	for _, s := range d.samples[1:] {
		x := s.x
		if dir == 0 {
			if x-runStart >= shakeMinTravel {
				dir = 1
			} else if runStart-x >= shakeMinTravel {
				dir = -1
			}
			if dir != 0 {
				runMax = x
			}
			continue
		}
		dx := (x - runMax) * float64(dir)
		if dx >= shakeDeadband {
			runMax = x
			continue
		}
		if -dx >= shakeMinTravel {
			n++
			dir = -dir
			runStart, runMax = runMax, x
		}
	}
	return n
}

// reset clears the detector: after a trigger, and when the drag ends or a
// basket show is no longer wanted.
func (d *shakeDetector) reset() {
	d.samples = d.samples[:0]
}
