package main

import (
	"math"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type stubVolumeControl struct {
	val atomic.Uint32
}

func (s *stubVolumeControl) level() (float32, bool) {
	return float32(s.val.Load()) / 100.0, true
}

func (s *stubVolumeControl) set(v float32) error {
	s.val.Store(uint32(v * 100))
	return nil
}

func TestVolumeRowPollsWhenPanelHidden(t *testing.T) {
	vol := &stubVolumeControl{}
	vol.val.Store(25)
	a := &shelf{
		visible: false,
		tab:     pageClipboard,
		vol:     vol,
	}
	view := ui.NewTester(func(c *ui.Context) {
		a.volumeRow(c, "pin-vol", 12, 6)
	}, 240, 48)
	view.Frame()
	if !a.music.out.ok || a.music.out.v != 0.25 {
		t.Fatalf("initial output volume = (%v, %v), want (0.25, true)", a.music.out.v, a.music.out.ok)
	}

	vol.val.Store(75)
	view.Advance(volumePoll + time.Millisecond)
	view.Frame()
	if a.music.out.v != 0.75 {
		t.Fatalf("output volume after advance with hidden panel = %v, want 0.75", a.music.out.v)
	}
}

// A canceled release leaves the knob at the track's value and commits
// nothing; a real release still commits once.
func TestRailCanceledReleaseCommitsNothing(t *testing.T) {
	var d railDrag
	var commits []float32
	view := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).FillWidth().Children(func() {
			rail(c, "r", "音量", 0.25, 20, 3, ui.RGB(255, 255, 255), true, &d,
				func(f float32) { commits = append(commits, f) }).Grow(1)
		})
	}, 200, 20)
	view.Frame()
	view.Press(160, 10)
	if !d.drag {
		t.Fatal("the press did not grab the rail")
	}
	view.CancelPress()
	if len(commits) != 0 {
		t.Fatalf("a canceled release committed %v", commits)
	}
	if d.drag {
		t.Fatal("the canceled release left the drag on")
	}
	view.Press(160, 10)
	view.Release(160, 10)
	if len(commits) != 1 || math.Abs(float64(commits[0]-0.8)) > 0.01 {
		t.Fatalf("release commits = %v, want one at 0.8", commits)
	}
}
