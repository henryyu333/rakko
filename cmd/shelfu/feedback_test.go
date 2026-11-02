//go:build darwin

package main

// The motion and completion-feedback rules of DESIGN.md 「动效」 and
// visual-v1 groups 1, 3 and 4: the springs, the frame that holds the
// opening's overshoot, the content box's bottom fade, and the notch capsule's
// states. The headless frames come from ui.Tester, so the springs are driven
// by a fake clock and every drawn frame can be checked.

import (
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"shelf/internal/mac"
)

// motionShelf is a closed, empty shelf without a native window: state
// tests never reach the window.
func motionShelf() *shelf {
	return &shelf{
		level:        2,
		notchX:       600,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		x:            410,
		y:            0,
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
}

// motionWindow is motionShelf with a placeholder native window, so view()
// takes its production branch (the tester supplies the surface).
func motionWindow() *shelf {
	a := motionShelf()
	a.win = &mygo.Window{}
	return a
}

// TestOpeningIsElasticAndTheFrameNeverClips drives the opening frame by
// frame: the drawn shell must overshoot its final height (the spring's
// elasticity) and the window must hold every frame of that flight — the
// bottom corners are never cut — then settle back to the panel's size.
func TestOpeningIsElasticAndTheFrameNeverClips(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame() // the idle frame the spring starts from
	a.visible = true
	target := float32(a.panelH())
	peak := float32(0)
	for i := range 120 {
		view.Frame()
		peak = max(peak, a.fx.h)
		f := a.desiredFrame()
		if f.Width < int(a.fx.w+0.5) || f.Height < int(a.fx.h+0.5) {
			t.Fatalf("frame %d: window %dx%d cuts the shell drawn at %.1fx%.1f", i, f.Width, f.Height, a.fx.w, a.fx.h)
		}
		if f.Y != a.y {
			t.Fatalf("frame %d: the window moved off the top edge: %+v", i, f)
		}
		if f.X != a.stripCenter()-f.Width/2 {
			t.Fatalf("frame %d: the shell is not centred in the window: %+v", i, f)
		}
		view.Advance(16 * time.Millisecond)
	}
	if a.fx.openMoving {
		t.Fatal("the opening spring never came to rest")
	}
	if peak <= target+1 {
		t.Fatalf("the shell peaked at %.1f, only %.1f%% past the final %.1f: the opening has no bounce",
			peak, (peak/target-1)*100, target)
	}
	if got := a.fx.h; got > target+0.5 || got < target-2 {
		t.Fatalf("the opening settled at %.1f, not the panel's %.1f", got, target)
	}
	sw, _ := sheetFrame()
	if got := a.desiredFrame().Width; got != sw {
		t.Fatalf("the settled frame is %d wide, want the panel plus both ears (%d)", got, sw)
	}
}

// TestClosingFoldsCrisp closes a settled panel frame by frame: the shell may
// only shrink — a bouncy collapse would visibly pop — and must reach the
// notch band without dipping below it.
func TestClosingFoldsCrisp(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.visible = true
	for range 120 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	base := a.fx.h
	a.visible = false
	prev := base
	for i := range 120 {
		view.Frame()
		if a.fx.h > prev+0.01 {
			t.Fatalf("frame %d: the closing shell grew from %.1f to %.1f", i, prev, a.fx.h)
		}
		if a.fx.h < 32-0.01 {
			t.Fatalf("frame %d: the shell folded to %.1f, below the notch band", i, a.fx.h)
		}
		prev = a.fx.h
		view.Advance(16 * time.Millisecond)
	}
	if prev > 32.5 {
		t.Fatalf("the closing shell stopped at %.1f, above the notch band", prev)
	}
}

// TestPeekStripHoldsFoldingShapes keeps the native strip as big as the
// shape it is folding back from: a capsule that just expired, or wings
// whose track stopped, must not be clipped on its way down.
func TestPeekStripHoldsFoldingShapes(t *testing.T) {
	a := motionShelf()
	a.fx = shellMotion{peek: true, w: 380, h: 54}
	if f := a.desiredFrame(); f.Width < 380 || f.Height < 54 {
		t.Fatalf("strip %+v cuts the folding shell 380x54", f)
	}
	// The capsule's own room — shell, ears, shake and overshoot, no glow
	// margin — appears with it.
	a.fx = shellMotion{}
	a.toastStatus("已压缩 3 张", time.Now().Add(3*time.Second), statusOK)
	if f := a.desiredFrame(); f.Width < capsuleFrameW() || f.Height < 32+fbBody {
		t.Fatalf("strip %+v cannot hold the capsule", f)
	}
}

// TestCapsuleOnlyWhileThePanelIsClosed: the capsule belongs to the closed
// notch; opening, hovering and a file drag all take the peek shape back.
func TestCapsuleOnlyWhileThePanelIsClosed(t *testing.T) {
	now := time.Now()
	a := motionShelf()
	a.toastStatus("已压缩 3 张", now.Add(3*time.Second), statusOK)
	if !a.capsuleUp(now) {
		t.Fatal("a closed panel with a fresh toast has no capsule")
	}
	a.visible = true
	if a.capsuleUp(now) {
		t.Fatal("an open panel must keep its toast at the bottom, not the notch")
	}
	a.visible = false
	a.peeking = true
	if a.capsuleUp(now) {
		t.Fatal("hover must take the peek shape back from the capsule")
	}
	a.peeking = false
	a.dragging = true
	if a.capsuleUp(now) {
		t.Fatal("an incoming file drag must take the peek shape back")
	}
	a.ownDrag = true
	if !a.capsuleUp(now) {
		t.Fatal("Rakko's own drag-out does not ask for the drop pill: the capsule stays")
	}
	a.ownDrag, a.dragging = false, false
	a.fb.arm(now)
	if a.capsuleUp(now.Add(3*time.Second + time.Millisecond)) {
		t.Fatal("the capsule outlived the caller's deadline")
	}
}

// TestToastWhileClosedBecomesCapsule: a status posted with the panel closed
// is what the capsule says, in the status colour and for the design's time;
// with the panel open the bottom pill keeps it instead.
func TestToastWhileClosedBecomesCapsule(t *testing.T) {
	a := motionShelf()
	now := time.Now()
	a.toastStatus("已压缩 3 张", now.Add(3*time.Second), statusOK)
	if a.fb.text != "已压缩 3 张" || a.fb.kind != statusOK {
		t.Fatalf("capsule = %q kind=%v, want the success text", a.fb.text, a.fb.kind)
	}
	if got := a.fb.color(); got != fbGreen {
		t.Fatalf("success colour %v, want %v", got, fbGreen)
	}
	// The caller's 3s deadline wins over the 1.6s floor (a few ns of clock
	// skew between the post and show's own now is expected).
	if held := a.fb.until.Sub(a.fb.shownAt); held < 3*time.Second-100*time.Millisecond || held > 3*time.Second {
		t.Fatalf("success held %v, want the caller's 3s", held)
	}
	a.toastStatus("压缩包已加密", now.Add(4*time.Second), statusErr)
	if a.fb.color() != fbRed {
		t.Fatalf("failure colour %v, want %v", a.fb.color(), fbRed)
	}
	if held := a.fb.until.Sub(a.fb.shownAt); held < 4*time.Second-100*time.Millisecond || held > 4*time.Second {
		t.Fatalf("failure held %v, want the caller's 4s", held)
	}
	// The panel open: no capsule, the toast layer's pill carries it.
	a.fb = feedback{}
	a.visible = true
	a.toastStatus("已放入", now.Add(3*time.Second), statusOK)
	if a.fb.text != "" || a.fb.up(now) {
		t.Fatal("an open panel must not grow a capsule")
	}
	if a.status != "已放入" {
		t.Fatalf("the open panel's toast is %q", a.status)
	}
}

// TestLiveJobDrivesTheCapsuleAndHandsOverToItsVerdict: a closed panel while
// a conversion runs shows the job's progress in orange, never expires on its
// own, and the job's finishing toast replaces it with the verdict.
func TestLiveJobDrivesTheCapsuleAndHandsOverToItsVerdict(t *testing.T) {
	a := motionWindow()
	j := &convertJob{target: toJPEG, unit: "张", started: time.Now().Add(-time.Second)}
	j.total.Store(3)
	j.done.Store(1)
	a.conv = &converter{host: a, job: j}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if a.fb.text != "正在转成 JPEG · 2/3" || !a.fb.live {
		t.Fatalf("capsule = %q live=%v, want the job's progress", a.fb.text, a.fb.live)
	}
	if a.fb.color() != fbOrange {
		t.Fatalf("running colour %v, want %v", a.fb.color(), fbOrange)
	}
	if !a.fb.until.IsZero() {
		t.Fatal("a running job's capsule must not expire by itself")
	}
	if !view.HasText("正在转成 JPEG · 2/3") {
		t.Fatal("the closed notch does not show the running job")
	}
	// The job ends and its toast lands: the capsule turns into the verdict.
	a.conv.job = nil
	a.toastStatus("已转换 3 项 · 已选中，可直接拖出", time.Now().Add(3*time.Second), statusOK)
	view.Frame()
	if a.fb.live || a.fb.text != "已转换 3 项 · 已选中，可直接拖出" {
		t.Fatalf("after the job the capsule is %q live=%v", a.fb.text, a.fb.live)
	}
	if a.fb.color() != fbGreen {
		t.Fatalf("verdict colour %v, want %v", a.fb.color(), fbGreen)
	}
	// And it goes away when the hold runs out.
	a.fb.shownAt = time.Now().Add(-fbExpand - fbHoldOK - time.Second)
	a.fb.until = time.Now().Add(-time.Second)
	view.Frame()
	if a.fb.drawn {
		t.Fatal("the capsule is still drawn past its hold")
	}
}

// TestCapsuleGeometry: as wide as the words, at least the notch plus 60,
// never wider than fbMaxW, and exactly the notch band plus 22 tall.
func TestCapsuleGeometry(t *testing.T) {
	f := feedback{text: "好"}
	if w, h := f.capsule(180, 32); w != 240 || h != 54 {
		t.Fatalf("a short text makes %0.fx%0.f, want the 240 minimum and 54 tall", w, h)
	}
	f.text = "已压缩 · 132 KB · 已选中，可直接拖出"
	w, h := f.capsule(180, 32)
	if w < 240 || w > fbMaxW {
		t.Fatalf("a normal text makes %0.f wide, want within 240…%d", w, fbMaxW)
	}
	if h != 54 {
		t.Fatalf("capsule height %.0f, want the notch band plus %d", h, fbBody)
	}
	f.pillW = 200
	if w, _ := f.capsule(180, 32); w != 200+fbDot+fbGap+2*fbPadX {
		t.Fatalf("a measured text makes %0.f wide, want its width plus the chrome", w)
	}
	f.pillW = 1000
	if w, _ := f.capsule(180, 32); w != fbMaxW {
		t.Fatalf("an overlong text makes %0.f wide, want the %d cap", w, fbMaxW)
	}
	f.pillW = 0
	f.text = "已压缩 3 张，2 张本来就小于目标 · 4.6 MB → 1.8 MB，另外还有一些没能压到 1 MB 以内"
	if w, _ := f.capsule(180, 32); w != fbMaxW {
		t.Fatalf("a long unmeasured text makes %0.f wide, want the %d cap", w, fbMaxW)
	}
}

// TestFailureShakesOnce: the failure's shake swings left first, decays, and
// is over within fbShake; successes and Reduce Motion never shake.
func TestFailureShakesOnce(t *testing.T) {
	now := time.Now()
	f := feedback{text: "失败", kind: statusErr, shownAt: now, until: now.Add(time.Hour)}
	if got := f.shake(now, false); got != 0 {
		t.Fatalf("the shake started before the capsule grew: %.2f", got)
	}
	first := f.shake(now.Add(fbExpand+fbShake/4), false)
	if first >= 0 || -first < fbShakeX*0.7 {
		t.Fatalf("first swing %.2f, want a strong left swing", first)
	}
	second := f.shake(now.Add(fbExpand+3*fbShake/4), false)
	if second <= 0 || second >= -first*0.7 {
		t.Fatalf("second swing %.2f, want a weaker right swing than %.2f", second, -first)
	}
	if got := f.shake(now.Add(fbExpand+fbShake), false); got != 0 {
		t.Fatalf("the shake outlived its run: %.2f", got)
	}
	if got := f.shake(now.Add(fbExpand+fbShake/10), true); got != 0 {
		t.Fatal("Reduce Motion must not shake")
	}
	f.kind = statusOK
	if got := f.shake(now.Add(fbExpand+fbShake/10), false); got != 0 {
		t.Fatalf("a success shook: %.2f", got)
	}
}

// TestCapsuleClickExpandsThroughTheMonitor: with the window still letting
// clicks through, the global monitor is what expands the panel from the
// capsule; a press away from it must not.
func TestCapsuleClickExpandsThroughTheMonitor(t *testing.T) {
	a := motionShelf()
	a.toastStatus("已转换 1 项", time.Now().Add(3*time.Second), statusOK)
	a.fb.drawn = true
	r := a.peekPillRect()
	if r.Width < 240 {
		t.Fatalf("the capsule's click box is only %d wide: %+v", r.Width, r)
	}
	a.mouseDown(false, 0, mygo.Point{X: r.X + r.Width/2, Y: r.Y + r.Height - 6})
	if !a.visible {
		t.Fatal("a press on the capsule must expand the shelf")
	}
	b := motionShelf()
	b.toastStatus("已转换 1 项", time.Now().Add(3*time.Second), statusOK)
	b.fb.drawn = true
	b.mouseDown(false, 0, mygo.Point{X: b.notchX - 120, Y: b.notchH + 10})
	if b.visible {
		t.Fatal("a press beside the capsule must pass through")
	}
}

// TestCapsuleAccentBreathes: a running job's dot breathes between dim and
// full over fbBreathe; a verdict's accent is steady, and Reduce Motion
// keeps it steady too.
func TestCapsuleAccentBreathes(t *testing.T) {
	now := time.Unix(0, 0)
	live := feedback{text: "正在解压 · 1/3", live: true}
	var lo, hi float32 = 1, 0
	for i := range 40 {
		v := live.accent(now.Add(time.Duration(i)*fbBreathe/40), false)
		lo, hi = min(lo, v), max(hi, v)
	}
	if lo > 0.5 || hi < 0.9 {
		t.Fatalf("the breath swings %.2f…%.2f, want about 0.35…1", lo, hi)
	}
	if got := live.accent(now, true); got != 0.9 {
		t.Fatalf("Reduce Motion accent %.2f, want steady 0.9", got)
	}
	done := feedback{text: "已完成"}
	if got := done.accent(now, false); got != 0.9 {
		t.Fatalf("a verdict's accent %.2f, want steady 0.9", got)
	}
}

// TestLiveCapsuleRearmsAfterAToastExpires: a transient toast that interrupts
// the running job's capsule expires on its own clock; once it is over, the
// job's progress returns and stays — the toast's deadline must not follow it.
func TestLiveCapsuleRearmsAfterAToastExpires(t *testing.T) {
	a := motionWindow()
	j := &convertJob{target: toJPEG, unit: "张", started: time.Now().Add(-time.Second)}
	j.total.Store(3)
	j.done.Store(1)
	a.conv = &converter{host: a, job: j}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	if !a.fb.live {
		t.Fatal("the job's capsule is not up")
	}
	// A toast lands while the capsule shows progress: it takes the words.
	a.toastStatus("已自动清理 2 项", time.Now().Add(2*time.Second), statusOK)
	view.Frame()
	if a.fb.live || a.fb.text != "已自动清理 2 项" {
		t.Fatalf("the toast did not take the capsule: %q live=%v", a.fb.text, a.fb.live)
	}
	// The toast's span runs out on the tester's clock: the job returns.
	view.Advance(fbExpand + fbHoldOK + time.Second)
	view.Frame()
	if !a.fb.live || a.fb.text != "正在转成 JPEG · 2/3" {
		t.Fatalf("after the toast the capsule is %q live=%v, want the job's progress", a.fb.text, a.fb.live)
	}
	if !a.fb.until.IsZero() {
		t.Fatalf("the returning job capsule carried the toast's deadline %v", a.fb.until)
	}
	// And it keeps showing the job long after the toast would have died.
	view.Advance(fbHoldOK + 2*time.Second)
	view.Frame()
	if !a.capsuleUp(view.Now()) || !view.HasText("正在转成 JPEG") {
		t.Fatal("the job's capsule died with the toast instead of re-arming")
	}
}

// TestToastPostedWhileClosingStillShows: a status landing mid-close is not
// swallowed: it posts to the capsule, which waits out the fold and then
// holds for its full span from the first drawn frame.
func TestToastPostedWhileClosingStillShows(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.visible = true
	for range 40 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	// The panel is on its way down when the job reports.
	a.visible, a.progress = false, 0.5
	a.toastStatus("已转换 3 项", time.Now().Add(3*time.Second), statusOK)
	if a.fb.text != "已转换 3 项" {
		t.Fatalf("a toast posted mid-close never reached the capsule: %q", a.fb.text)
	}
	if !a.fb.up(view.Now()) {
		t.Fatal("the posted capsule is already expired")
	}
	a.progress = 0
	view.Frame()
	view.Advance(500 * time.Millisecond) // the fold plus the grow-out
	view.Frame()
	if !a.fb.drawn || !view.HasText("已转换 3 项") {
		t.Fatal("the capsule never appeared after the close")
	}
	if !a.fb.armed {
		t.Fatal("the capsule's clock never started on its drawn frame")
	}
	if got := a.fb.until.Sub(a.fb.shownAt); got < fbExpand+fbHoldOK {
		t.Fatalf("the shown capsule holds %v, less than the %v floor", got, fbExpand+fbHoldOK)
	}
	// Past its hold it folds away and clears itself.
	view.Advance(fbHoldOK + time.Second)
	for range 60 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fb.text != "" {
		t.Fatalf("the folded capsule still says %q", a.fb.text)
	}
}

// TestCollapsedToastKeepsItsClockInThePill: posting a status while the panel
// is closed must not start the seen clock — when the panel opens the toast
// still lives until its first paint, as it always did.
func TestCollapsedToastKeepsItsClockInThePill(t *testing.T) {
	a := motionShelf()
	a.toastStatus("出错了", time.Time{}, statusErr)
	if a.statusSeen != 0 || !a.statusSeenAt.IsZero() {
		t.Fatal("a closed-panel toast already counts down before it is ever painted")
	}
	if a.fb.text != "出错了" {
		t.Fatalf("the capsule did not take the toast: %q", a.fb.text)
	}
}

// TestCapsuleGrowNeverPaintsPastTheShell: while the shell is still a sliver
// growing out of the notch, neither the dot nor the words may reach beyond
// its drawn edge — nothing may paint over the menu bar.
func TestCapsuleGrowNeverPaintsPastTheShell(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame() // the idle frame the grow starts from
	a.toastStatus("已压缩 3 张 · 4.6 MB → 1.8 MB", time.Now().Add(3*time.Second), statusOK)
	view.Frame()
	view.Advance(60 * time.Millisecond) // mid grow-out: the shell is narrow
	view.Frame()
	if a.fx.w >= 230 || a.fx.w < 80 {
		t.Fatalf("expected a mid-grow shell, got %.1f wide", a.fx.w)
	}
	img := view.Image()
	cx := img.Rect.Dx() / 2
	x0 := cx - int(a.fx.w/2)
	x1 := cx + int(a.fx.w/2)
	// Nothing paints outside the shell anymore: neither black nor light
	// pixels may appear more than 4pt outside the drawn edge.
	opaque := func(x, y int) bool {
		r, g, b, al := img.At(x, y).RGBA()
		if al < 200*257 {
			return false
		}
		return max(max(r, g), b) < 60*257 || min(min(r, g), b) > 200*257
	}
	y := int(a.fx.h) - 10
	for x := x0 - 70; x < x0-4; x++ {
		if opaque(x, y) {
			t.Fatalf("opaque paint at x=%d reaches %dpt left of the %.0f-wide shell", x, x0-x, a.fx.w)
		}
	}
	for x := x1 + 4; x < x1+70; x++ {
		if opaque(x, y) {
			t.Fatalf("opaque paint at x=%d reaches %dpt right of the %.0f-wide shell", x, x-x1, a.fx.w)
		}
	}
}

// TestPeekMorphFlightKeepsStripRoom: while a peek morph is still in flight
// (chevron → wings, or the capsule growing), the strip must hold the target
// plus the spring's overshoot, so no mid-flight frame is clipped.
func TestPeekMorphFlightKeepsStripRoom(t *testing.T) {
	a := motionShelf()
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Title: "T"}}
	pw, ph := a.peekSize()
	a.fx.sizeMoving = true
	f := a.desiredFrame()
	if f.Width < int(pw*1.04) || f.Height < int(ph*1.04) {
		t.Fatalf("a morphing strip is %dx%d, the %0.fx%0.f shape plus its overshoot needs more", f.Width, f.Height, pw, ph)
	}
	a.fx.sizeMoving = false
	f2 := a.desiredFrame()
	if f2.Width < int(pw+0.5) || f2.Width >= f.Width || f2.Height >= f.Height {
		// Settled: the strip returns to the shape itself, no margin left.
		t.Fatalf("the settled strip should shrink back to the shape: %+v vs moving %+v", f2, f)
	}
}

// TestShakeIsOneLeftRightPass: the failure rocks once — left, then right at
// about half strength — and is done; the old five-lobe shiver is gone.
func TestShakeIsOneLeftRightPass(t *testing.T) {
	now := time.Now()
	f := feedback{text: "失败", kind: statusErr, shownAt: now, until: now.Add(time.Hour)}
	if got := f.shake(now.Add(fbExpand+fbShake/2+30*time.Millisecond), false); got <= 0 {
		// t≈0.55: the single pass must already be on its right-hand return;
		// a 2.5-cycle shiver is still swinging left here.
		t.Fatalf("mid-pass offset %.2f, want the right-hand swing", got)
	}
	if got := f.shake(now.Add(fbExpand+fbShake/10), false); got >= 0 {
		t.Fatalf("the pass starts left, got %.2f", got)
	}
}

// TestCapsuleAccentAlphaIsTheSpec: the verdict's accent alpha is 0.9 once,
// not multiplied down a second time at the painter, and the breath stays
// inside 0.3…0.9.
func TestCapsuleAccentAlphaIsTheSpec(t *testing.T) {
	now := time.Unix(0, 0)
	live := feedback{text: "正在解压 · 1/3", live: true}
	var lo, hi float32 = 1, 0
	for i := range 40 {
		v := live.accent(now.Add(time.Duration(i)*fbBreathe/40), false)
		lo, hi = min(lo, v), max(hi, v)
	}
	if lo < 0.28 || lo > 0.32 || hi > 0.901 || hi < 0.85 {
		t.Fatalf("the breath swings %.2f…%.2f, want about 0.3…0.9", lo, hi)
	}
	done := feedback{text: "已完成"}
	if got := done.accent(now, false); got != 0.9 {
		t.Fatalf("a verdict's accent %.2f, want exactly 0.9", got)
	}
}

// TestSettledCapsuleAsksForNoFrames: a capsule that is holding still must not
// make the whole view repaint at display rate; the failure shake does, and
// the breath runs on its timed beats.
func TestSettledCapsuleAsksForNoFrames(t *testing.T) {
	now := time.Now()
	ok := feedback{text: "已完成", shownAt: now.Add(-time.Second), until: now.Add(time.Hour)}
	if ok.animating(now, false) {
		t.Fatal("a settled success capsule still asks for per-frame redraws")
	}
	fail := feedback{text: "失败", kind: statusErr, shownAt: now.Add(-fbExpand - 100*time.Millisecond)}
	if !fail.animating(now, false) {
		t.Fatal("a shaking failure must keep per-frame redraws")
	}
	fail2 := fail
	fail2.shownAt = now.Add(-fbExpand - fbShake - time.Second)
	if fail2.animating(now, false) {
		t.Fatal("a finished shake still asks for per-frame redraws")
	}
	live := feedback{text: "正在转成 JPEG · 1/3", live: true}
	if live.animating(now, false) {
		// The breath runs on the timed beats settle() hands to After —
		// four frames per breath, not one per display frame.
		t.Fatal("the breathing accent must not ask for per-frame redraws")
	}
	if got := live.settle(now); got.Sub(now) != fbBreathe/4 {
		t.Fatalf("a running job's next beat is %v out, want %v", got.Sub(now), fbBreathe/4)
	}
}

// TestWingsFadeOutWithTheShell: when the pointer leaves hovering wings,
// the pill keeps drawing them while the shell folds — fading with the
// shell (peekK) — instead of snapping back to the bare chevron mid-retract.
func TestWingsFadeOutWithTheShell(t *testing.T) {
	a := motionShelf()
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
		Playing: true, Title: "Fashion Killa", Duration: 200, Elapsed: 30, Received: time.Now(),
	}}
	// peekK mid-decay, the pointer gone: the wings are still the shape on
	// show, drawn at their fading opacity.
	view := ui.NewTester(func(c *ui.Context) {
		a.peekView(c, 284, 36, 0, 0.6, 0, false, false)
	}, sheetW, panelTestH)
	view.Frame()
	if _, ok := view.Find("正在播放：Fashion Killa · 展开 Rakko"); !ok {
		t.Fatal("the wings vanished mid-retract instead of fading with the shell")
	}
}
