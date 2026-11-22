package main

import (
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
