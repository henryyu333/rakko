//go:build darwin

package main

// The Windows update window's height budget follows the caption row it
// actually draws: the faces budget a 28pt title strip, mygo's caption
// controls sit in c.TitleBar().Height, and the page grows by the
// difference — measured from the first frame on, the documented 32pt
// default until then — so a taller bar cannot clip the bottom buttons.

import (
	"math"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/locales"
)

func TestUpdateTitleExtraBudgetsTheCaptionRow(t *testing.T) {
	for _, tc := range []struct {
		titleBarH float32
		want      int
	}{
		{0, updateWindowsTitleExtra}, // unmeasured: the documented default
		{27, 0},
		{28, 0},
		{32, updateWindowsTitleExtra},
		{36, 8},
		{28.4, 1},
	} {
		if got := updateTitleExtra(tc.titleBarH); got != tc.want {
			t.Errorf("updateTitleExtra(%v) = %d, want %d", tc.titleBarH, got, tc.want)
		}
	}
}

// The small faces keep their buttons inside the page whatever the caption
// row's real height is: the page grows by exactly the measured extra.
func TestUpdateWindowButtonsFitTheMeasuredTitleBar(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	l := locales.In("zh-Hans")
	for _, titleBarH := range []float32{28, 32, 36} {
		a := updateShelf()
		a.update = &mygo.Update{Version: "1.2.3"}
		a.updateWinStage = updateStageDownloading
		a.updateProgress = 0.42
		a.updateBytes, a.updateTotal = 7_000_000, 19_000_000
		a.updateWinTitleH = titleBarH
		w, h := a.updateWinSize(updateStageDownloading)
		if want := updateSmallH + updateTitleExtra(titleBarH); h != want {
			t.Fatalf("title bar %v: window height %d, want %d", titleBarH, h, want)
		}
		v := ui.NewTester(a.updateView, w, h)
		v.SetLocale("zh-Hans")
		v.SetTitleBar(ui.TitleBar{Height: titleBarH})
		v.Frame()
		v.Frame()
		for _, want := range []string{l.UpdateDownloadingShort(), l.UpdateCancel()} {
			r, ok := v.Find(want)
			if !ok {
				t.Fatalf("title bar %v: %q missing from the downloading face", titleBarH, want)
			}
			if r.X < -0.5 || r.Y < -0.5 || r.X+r.W > float32(w)+0.5 || r.Y+r.H > float32(h)+0.5 {
				t.Fatalf("title bar %v: %q at %+v leaves the %dx%d page", titleBarH, want, r, w, h)
			}
		}
	}
}

// The bar measures the caption row while drawing and fits the page to it
// once the frame is done — never resizing from inside the frame.
func TestUpdateBarMeasuresTheTitleBarAndRefitsOnce(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	var posted []func()
	origDispatch := dispatchMain
	dispatchMain = func(fn func()) { posted = append(posted, fn) }
	t.Cleanup(func() { dispatchMain = origDispatch })

	a := updateShelf()
	a.update = &mygo.Update{Version: "1.2.3"}
	a.updateWinStage = updateStageDownloading
	if _, h := a.updateWinSize(updateStageDownloading); h != updateSmallH+updateWindowsTitleExtra {
		t.Fatalf("unmeasured height %d, want the documented default %d", h, updateSmallH+updateWindowsTitleExtra)
	}
	w, h := a.updateWinSize(updateStageDownloading)
	v := ui.NewTester(a.updateView, w, h)
	v.SetLocale("zh-Hans")
	v.SetTitleBar(ui.TitleBar{Height: 36})
	v.Frame()
	if a.updateWinTitleH != 36 {
		t.Fatalf("the bar measured %v, want 36", a.updateWinTitleH)
	}
	if len(posted) != 1 {
		t.Fatalf("the frame posted %d resize callbacks, want the one", len(posted))
	}
	posted[0]()
	// The refit is idempotent: the next frame measures the same height and
	// posts nothing.
	v.Frame()
	if len(posted) != 1 {
		t.Fatalf("an unchanged height posted another %d callbacks", len(posted)-1)
	}
	if _, h := a.updateWinSize(updateStageDownloading); h != updateSmallH+8 {
		t.Fatalf("refit height %d, want %d", h, updateSmallH+8)
	}
}

func TestUpdateTitleExtraIsFloatExact(t *testing.T) {
	// A fractional bar height rounds up, as the drawn bar does.
	if got := updateTitleExtra(36.4); got != int(math.Ceil(36.4))-updateBarH {
		t.Fatalf("updateTitleExtra(36.4) = %d, want %d", got, int(math.Ceil(36.4))-updateBarH)
	}
	if got := updateTitleExtra(27.9); got != 0 {
		t.Fatalf("updateTitleExtra(27.9) = %d, want 0", got)
	}
}
