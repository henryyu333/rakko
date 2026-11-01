//go:build darwin

package main

// Completion capsules preserve status priority and visible time across
// player, hover, drag and panel transitions.

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/mac"
	"shelf/internal/store"
)

// grownCapsule posts nothing itself: it runs the frames a capsule needs to
// grow out of the notch on the tester's fake clock.
func grownCapsule(a *shelf, view *ui.Tester) {
	view.Frame()
	view.Advance(700 * time.Millisecond)
	view.Frame()
}

// fixLiveJob is a live JPEG job mid-flight for follow()/capsule tests.
func fixLiveJob() *convertJob {
	j := &convertJob{target: toJPEG, unit: "张", started: time.Now().Add(-time.Second)}
	j.total.Store(3)
	j.done.Store(1)
	return j
}

// #1: with a track loaded, the capsule still wins the peek shape — the
// status words draw, the player does not.
func TestCapsuleOutranksThePlayer(t *testing.T) {
	a := motionWindow()
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{
		Playing: true, Title: "Song Title", Artist: "Artist", Duration: 200, Elapsed: 30,
		Received: time.Now(),
	}}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已转换 3 项", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	if !view.HasText("已转换 3 项") {
		t.Fatalf("the player stole the capsule's shell: texts %v", view.Texts())
	}
	if view.HasText("Song Title") {
		t.Fatal("the player drew over the status capsule")
	}
	if !a.fb.armed {
		t.Fatal("the capsule's clock never started under the player")
	}
	// The strip holds room for both shapes, so the hand-off needs no resize.
	f := a.desiredFrame()
	pw, ph := a.wingsSize()
	if f.Width < int(pw+0.5) || f.Height < int(ph+0.5) {
		t.Fatalf("strip %+v cannot hold the wings %.0fx%.0f", f, pw, ph)
	}
}

// #2: a toast landing while the previous capsule folds away re-grows a
// capsule with its words — retracting must not stick.
func TestToastDuringFoldBackDrawsAgain(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("第一条", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	if !view.HasText("第一条") {
		t.Fatal("the first capsule never drew")
	}
	a.fb.until = view.Now().Add(-time.Second) // expire on the fake clock
	view.Frame()                              // fold takeoff: retracting set
	view.Advance(80 * time.Millisecond)       // mid-fold
	a.toastStatus("第二条", time.Now().Add(3*time.Second), statusOK)
	view.Frame()
	view.Advance(800 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn || !view.HasText("第二条") {
		t.Fatalf("re-grown capsule is an empty shell (retracting=%v text=%q drawn=%v)",
			a.fb.retracting, a.fb.text, a.fb.drawn)
	}
	if a.fb.retracting {
		t.Fatal("retracting stuck across the new capsule's grow")
	}
}

// #2 via follow(): a job starting during the fold-back re-grows a capsule
// with the progress words.
func TestJobStartDuringFoldBackDraws(t *testing.T) {
	a := motionWindow()
	a.conv = &converter{host: a}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("旧消息", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	a.fb.until = view.Now().Add(-time.Second)
	view.Frame()
	view.Advance(80 * time.Millisecond)
	a.conv.job = fixLiveJob()
	view.Frame()
	if !a.fb.live {
		t.Fatalf("the job did not take the folding capsule: text=%q live=%v", a.fb.text, a.fb.live)
	}
	view.Advance(800 * time.Millisecond)
	view.Frame()
	if !view.HasText("正在转成") {
		t.Fatalf("running-job capsule is an empty shell (retracting=%v)", a.fb.retracting)
	}
}

// #3: the capsule's whole fold-back rides the 300ms non-bouncing retract
// spring, not just its first frame.
func TestCapsuleFoldsBackOnTheRetractSpring(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已转换 3 项", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	a.fb.until = view.Now().Add(-time.Second)
	view.Frame()
	var curve []float32
	for range 16 {
		view.Advance(20 * time.Millisecond)
		view.Frame()
		curve = append(curve, a.fx.h)
	}
	t.Logf("fold curve: %.0f", curve)
	// The 160ms bouncy hover spring would be back at the 32pt notch band by
	// +160ms; the 300ms critically damped retract still has shell left.
	if curve[7] <= 34 {
		t.Fatalf("fold reached the notch by +160ms (h=%.1f): the capsule left on the hover spring", curve[7])
	}
	// And it does arrive: critically damped means no bounce back up.
	for i := 1; i < len(curve); i++ {
		if curve[i] > curve[i-1]+0.01 {
			t.Fatalf("the fold-back grew at frame %d (%.1f → %.1f): the spring bounced", i, curve[i-1], curve[i])
		}
	}
}

// #4: 回车复制 posts the confirmation and hides at once — the capsule must
// take it over and show it for its remaining time.
func TestHideHandsTheFreshToastToTheCapsule(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.visible, a.progress = true, 1
	a.toastStatus("已复制 · 在其他应用按 ⌘V 粘贴", time.Now().Add(3*time.Second), statusOK)
	view.Frame() // the pill paints once
	// one frame ago on the real clock: barely seen
	a.statusSeen, a.statusSeenAt = 0, time.Now()
	if !view.HasText("已复制") {
		t.Fatal("the pill did not show the confirmation")
	}
	a.hide(hideExplicit)
	a.progress = 0
	view.Frame()
	view.Advance(700 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn || !view.HasText("已复制 · 在其他应用按 ⌘V 粘贴") {
		t.Fatalf("Enter-copy's confirmation never reached the capsule (drawn=%v text=%q)",
			a.fb.drawn, a.fb.text)
	}
}

// #7: a toast the open panel already showed long enough must not grow a
// second capsule when the panel closes.
func TestToastReadInThePanelDoesNotReplay(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已转换 3 项", time.Now().Add(8*time.Second), statusOK)
	grownCapsule(a, view)
	a.visible, a.progress = true, 1
	view.Frame() // the pill starts showing
	// The pill has been on screen past the readable floor (hide works on
	// the real clock, so stamp the pill's first paint directly).
	a.statusSeen, a.statusSeenAt = statusOK.holdFloor()+200*time.Millisecond, time.Now()
	a.hide(hideExplicit)
	a.progress = 0
	view.Frame()
	view.Advance(800 * time.Millisecond)
	view.Frame()
	if a.fb.drawn || a.fb.text != "" {
		t.Fatalf("a toast read in the panel replayed in the capsule (text=%q)", a.fb.text)
	}
}

// #7 remaining time: a capsule toast visited briefly in the panel plays
// only its remaining time afterwards — no fresh hold.
func TestCapsuleToastKeepsItsClockAcrossAPanelVisit(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已转换 3 项", time.Now().Add(8*time.Second), statusOK)
	grownCapsule(a, view)
	if !a.fb.armed {
		t.Fatal("the capsule never showed")
	}
	until := a.fb.until
	a.visible, a.progress = true, 1 // the user opens the panel and reads the pill
	view.Frame()
	view.Advance(500 * time.Millisecond)
	view.Frame()
	// The visit was brief on the panel's clock; keep it brief on the wall
	// clock too — the content's opening blur makes headless frames slow,
	// and handoffStatus inside hide measures the pill's read time in real
	// time.
	// the visit was brief: seen time resets to now
	a.statusSeen, a.statusSeenAt = 0, time.Now()
	a.hide(hideExplicit)
	a.progress = 0
	view.Frame()
	view.Advance(700 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn {
		t.Fatal("the capsule did not come back after the panel visit")
	}
	if !a.fb.until.Equal(until) {
		t.Fatalf("the replay extended the hold: was %v off, now %v", until, a.fb.until)
	}
	// The pill and the capsule share one clock: the panel visit counted.
	view.Advance(a.fb.until.Sub(view.Now()) + 100*time.Millisecond)
	for range 60 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fb.text != "" {
		t.Fatalf("the replayed capsule never expired: %q", a.fb.text)
	}
}

// #5: hover suppressing the capsule pauses its clock; the toast resumes
// with its remaining time when the pointer leaves.
func TestHoverPausesTheCapsuleClock(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已转换 3 项", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	until := a.fb.until
	a.peeking, a.peekProgress = true, 1 // the pointer rests on the notch
	view.Frame()
	view.Advance(3 * time.Second) // the hover outlasts the whole posting deadline
	a.peeking, a.peekProgress = false, 0
	view.Frame()
	view.Advance(600 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn || !view.HasText("已转换 3 项") {
		t.Fatalf("the hover ate the toast (drawn=%v text=%q)", a.fb.drawn, a.fb.text)
	}
	if d := a.fb.until.Sub(until); d < 2500*time.Millisecond {
		t.Fatalf("the hold was not paused for the hover: until moved only %v", d)
	}
}

// #5 the drag twin: an incoming file drag pauses the capsule the same way.
func TestDragPausesTheCapsuleClock(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已压缩 2 张", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	a.dragging = true // an incoming file drag claims the peek shape
	view.Frame()
	view.Advance(3 * time.Second)
	a.dragging = false
	view.Frame()
	view.Advance(600 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn || !view.HasText("已压缩 2 张") {
		t.Fatalf("the drag ate the toast (drawn=%v text=%q)", a.fb.drawn, a.fb.text)
	}
}

// #6: a result toast keeps the capsule for its whole span; a new job's
// progress waits, then takes over.
func TestNewJobNeverStompsAVerdictToast(t *testing.T) {
	a := motionWindow()
	a.conv = &converter{host: a}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("1 项成功，2 项失败", time.Now().Add(4*time.Second), statusErr)
	grownCapsule(a, view)
	if !view.HasText("1 项成功，2 项失败") || a.fb.color() != fbRed {
		t.Fatal("the error verdict never reached the capsule")
	}
	a.conv.job = fixLiveJob() // a new job starts while the verdict is up
	view.Frame()
	if a.fb.live {
		t.Fatalf("the running job replaced the visible verdict: %q", a.fb.text)
	}
	// Once the verdict's span ends, the progress takes over.
	view.Advance(5 * time.Second)
	view.Frame()
	if !a.fb.live || !view.HasText("正在转成 JPEG") {
		t.Fatalf("the job never returned after the verdict: %q live=%v", a.fb.text, a.fb.live)
	}
}

// #8: the capsule honours the caller's deadline — longer when asked, a
// floor for short ones, 4s minimum for a deadline-free error, and only the
// first line of a multi-line toast.
func TestCapsuleRespectsTheCallerDeadline(t *testing.T) {
	a := motionShelf()
	now := time.Now()
	a.toastStatus("检查更新失败：x", now.Add(8*time.Second), statusErr)
	if got := a.fb.until.Sub(now); got < 8*time.Second {
		t.Fatalf("the caller's 8s deadline became %v", got)
	}
	// A short deadline is floored at grow-out plus the readable hold.
	b := motionShelf()
	b.toastStatus("已放入", now.Add(300*time.Millisecond), statusOK)
	if got := b.fb.until.Sub(now); got < fbExpand+fbHoldOK {
		t.Fatalf("the success floor became %v", got)
	}
	// A deadline-free error stays at least 4s.
	c := motionShelf()
	c.report(assertErr("磁盘已满"))
	if got := c.fb.until.Sub(time.Now()); got < 4*time.Second-100*time.Millisecond {
		t.Fatalf("a deadline-free error held %v, want at least 4s", got)
	}
	// Multi-line toasts show their first line, not a flattened one-liner.
	d := motionShelf()
	d.toastStatus("第一行有问题\n第二行是细节", now.Add(8*time.Second), statusErr)
	if d.fb.text != "第一行有问题" {
		t.Fatalf("multi-line capsule = %q, want the first line", d.fb.text)
	}
	if d.status != "第一行有问题\n第二行是细节" {
		t.Fatalf("the pill lost the full text: %q", d.status)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

// #9: a running job's capsule breathes on timed beats and re-measures only
// when the words change — no per-frame redraw, no per-frame measure.
func TestLiveCapsulePacesItself(t *testing.T) {
	now := time.Now()
	live := feedback{text: "正在转成 JPEG · 1/3", live: true, kind: statusOK}
	if live.animating(now, false) {
		t.Fatal("the breathing accent must not request per-frame redraws")
	}
	if got := live.settle(now); got.Sub(now) != fbBreathe/4 {
		t.Fatalf("the next breath beat is %v out, want %v", got.Sub(now), fbBreathe/4)
	}
	// follow() with an unchanged step keeps the measured width.
	a := motionShelf()
	a.conv = &converter{host: a, job: fixLiveJob()}
	a.fb.follow(a, now)
	a.fb.pillW = 123
	a.fb.follow(a, now.Add(16*time.Millisecond))
	if a.fb.pillW != 123 {
		t.Fatal("an unchanged progress step dropped the measured width")
	}
	// A new step invalidates it exactly once.
	a.conv.job.done.Store(2)
	a.fb.follow(a, now.Add(32*time.Millisecond))
	if a.fb.pillW != 0 || !a.fb.live {
		t.Fatal("a changed step kept the stale width")
	}
}

// #10: the three status levels colour the capsule: green ✓, a neutral
// ink2 dot without an accent for plain information, red ! for errors.
func TestStatusLevelsColourTheCapsule(t *testing.T) {
	now := time.Now()
	info := feedback{text: "已取消", kind: statusInfo, shownAt: now, until: now.Add(time.Hour)}
	if got := info.color(); got != ink2 {
		t.Fatalf("information colour %v, want the neutral ink2", got)
	}
	if info.glyph() != "" {
		t.Fatalf("information glyph %q, want none", info.glyph())
	}
	if got := info.accent(now, false); got != 0 {
		t.Fatalf("information accent %.2f, want none", got)
	}
	ok := feedback{text: "已转换 3 项", kind: statusOK, shownAt: now, until: now.Add(time.Hour)}
	if ok.color() != fbGreen || ok.glyph() != "✓" || ok.accent(now, false) != 0.9 {
		t.Fatal("a success is not the green ✓ with the steady accent")
	}
	fail := feedback{text: "失败", kind: statusErr, shownAt: now, until: now.Add(time.Hour)}
	if fail.color() != fbRed || fail.glyph() != "!" {
		t.Fatal("an error is not the red !")
	}
}

// #11: background bookkeeping (automatic cleanup) never grows the capsule,
// even with the panel closed; it waits in the pill for the next opening.
func TestQuietToastNeverCapsules(t *testing.T) {
	a := motionShelf()
	a.toastQuiet("已自动清理 2 项超过 30 天的内容", time.Now().Add(5*time.Second), statusInfo)
	if a.fb.text != "" {
		t.Fatalf("a background toast reached the capsule: %q", a.fb.text)
	}
	if a.status == "" || a.statusCapsule {
		t.Fatal("the quiet toast did not stay in the pill")
	}
	// And hide() does not hand it off either.
	b := motionShelf()
	b.visible = true
	b.toastQuiet("已自动清理 3 项超过 30 天的内容", time.Now().Add(5*time.Second), statusInfo)
	b.hide(hideExplicit)
	if b.fb.text != "" {
		t.Fatalf("hide() handed a background toast to the capsule: %q", b.fb.text)
	}
}

// #12: while the capsule folds away, the click box follows the shell
// actually drawn, not the dead capsule's final size.
func TestClickBoxFollowsTheFoldingCapsule(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已转换 1 项", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	full := a.peekPillRect().Width
	a.fb.until = view.Now().Add(-time.Second)
	view.Frame()
	view.Advance(100 * time.Millisecond) // mid-fold
	view.Frame()
	r := a.peekPillRect()
	if r.Width >= full {
		t.Fatalf("mid-fold click box is still %d wide (full %d)", r.Width, full)
	}
	if want := int(a.fx.w + 0.5); r.Width != want {
		t.Fatalf("mid-fold click box %d, want the drawn shell's %d", r.Width, want)
	}
	if r.Height != int(a.fx.h+0.5) {
		t.Fatalf("mid-fold click box height %d, want the drawn shell's %.0f", r.Height, a.fx.h)
	}
}

func TestUnseenCapsuleKeepsItsRequestedVisibleTime(t *testing.T) {
	a := motionWindow()
	a.peeking = true
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("检查更新失败：offline", time.Now().Add(8*time.Second), statusErr)
	view.Frame()
	view.Advance(10 * time.Second)
	a.peeking = false
	view.Frame()
	view.Advance(600 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn || !a.fb.armed || a.fb.until.Sub(a.fb.shownAt) < 8*time.Second-time.Millisecond {
		t.Fatalf("unseen status lost its visible time: drawn=%v hold=%v", a.fb.drawn, a.fb.until.Sub(a.fb.shownAt))
	}
}

func TestPausedVerdictKeepsPriorityOverANewJob(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	a.toastStatus("已转换 3 项", time.Now().Add(3*time.Second), statusOK)
	grownCapsule(a, view)
	a.peeking = true
	view.Frame()
	view.Advance(5 * time.Second)
	a.conv = &converter{host: a, job: fixLiveJob()}
	a.peeking = false
	view.Frame()
	view.Advance(500 * time.Millisecond)
	view.Frame()
	if a.fb.live || !view.HasText("已转换 3 项") {
		t.Fatalf("the new job replaced a paused result: %q", a.fb.text)
	}
}

func TestDeferredDeadlineFreeErrorKeepsFourSeconds(t *testing.T) {
	now := time.Now()
	f := feedback{}
	f.show("操作失败：磁盘已满", statusErr, time.Time{}, now)
	f.arm(now.Add(10 * time.Second))
	if f.until.Sub(f.shownAt) < 4*time.Second {
		t.Fatalf("deferred error held only %v", f.until.Sub(f.shownAt))
	}
}

func TestAutomaticCleanupWaitsInThePanel(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.AddText("old content"); err != nil {
		t.Fatal(err)
	}
	a := motionShelf()
	a.store = s
	a.settings.RetentionDays = 1
	a.removeExpired(time.Now().Add(48 * time.Hour))
	if len(s.Items) != 0 || a.status == "" {
		t.Fatal("automatic cleanup did not complete")
	}
	if a.fb.text != "" || a.statusCapsule || a.statusKind != statusInfo {
		t.Fatal("automatic cleanup interrupted the closed notch")
	}
}

func TestStartupNoticesWaitInThePanel(t *testing.T) {
	a := motionShelf()
	a.notice("快捷键被占用")
	if a.fb.text != "" || a.statusCapsule || a.statusKind != statusErr || a.status == "" {
		t.Fatal("a startup notice grew a capsule instead of waiting in the panel")
	}
}

func TestIdenticalFeedbackKeepsItsMeasuredWidth(t *testing.T) {
	now := time.Now()
	f := feedback{}
	f.show("已复制", statusOK, now.Add(3*time.Second), now)
	f.pillW = 123
	f.show("已复制", statusOK, now.Add(4*time.Second), now)
	if f.pillW != 123 {
		t.Fatal("posting identical words discarded their measured width")
	}
	f.show("已转换", statusOK, now.Add(3*time.Second), now)
	if f.pillW != 0 {
		t.Fatal("new words retained the previous text width")
	}
}

func TestCancelledActionUsesNeutralFeedback(t *testing.T) {
	a := motionShelf()
	job := &convertJob{target: toJPEG}
	job.cancel.Store(true)
	a.conv = &converter{host: a, job: job}
	a.finishConvert(job, nil)
	if a.fb.color() != ink2 || a.fb.glyph() != "" {
		t.Fatal("cancellation presented a green success instead of neutral information")
	}
}

func TestFoldingCapsuleReceivesClicksAcrossItsVisibleWidth(t *testing.T) {
	a := motionShelf()
	a.fb.retracting = true
	a.fx = shellMotion{w: 300, h: 48, peek: true}
	a.mouseDown(false, 0, mygo.Point{X: a.stripCenter() + 135, Y: a.y + 38})
	if !a.visible {
		t.Fatal("a click inside the folding shell passed outside the pill")
	}
}

// TestOpeningFrameUsesThePanelSpring: while the panel is still opening,
// the in-flight margin comes from the panel's own ~1.1% overshoot — not
// the capsule's 4.5% — so the extra strip that catches clicks stays a
// couple of points, not a dozen.
func TestOpeningFrameUsesThePanelSpring(t *testing.T) {
	a := motionShelf()
	a.visible = true
	a.progress, a.animationFrom = 0.5, 0
	a.fx.openMoving = true
	f := a.desiredFrame()
	// The panel's own spring overshoot (≤1.5%) is what the frame keeps,
	// not the capsule's 4.5% — the extra click-catching strip stays tiny.
	want := int(math.Ceil(float64(sheetH)*(1+panelOpenOvershoot))) + 2
	if f.Height != want {
		t.Fatalf("in-flight window height %d, want %d", f.Height, want)
	}
	// A capsule-scale overshoot would have asked for noticeably more.
	old := int(math.Ceil(float64(sheetH)*(1+openOvershoot))) + 2
	if f.Height >= old {
		t.Fatalf("in-flight window still keeps the capsule's margin: %d >= %d", f.Height, old)
	}
}

// TestCapsuleTakesMouseOnlyOverItsBody: while a completion capsule is up,
// the strip accepts the mouse over the drawn shape — a press there must
// reach ShelfU — and passes it through over the frame's margins.
func TestCapsuleTakesMouseOnlyOverItsBody(t *testing.T) {
	a := motionShelf()
	a.winH = 1 // pretend a live native surface
	a.toastStatus("已复制", time.Now().Add(3*time.Second), statusOK)
	a.fb.drawn = true
	a.fx = shellMotion{w: 240, h: 54}
	old := cursorPoint
	t.Cleanup(func() { cursorPoint = old })
	r := a.peekPillRect()
	cursorPoint = func() mygo.Point { return mygo.Point{X: r.X + r.Width/2, Y: r.Y + r.Height/2} }
	a.syncMousePassThrough()
	if a.ignoringMouse {
		t.Fatal("the capsule's own body ignores the mouse")
	}
	cursorPoint = func() mygo.Point { return mygo.Point{X: r.X + r.Width + 20, Y: a.y + 10} }
	a.syncMousePassThrough()
	if !a.ignoringMouse {
		t.Fatal("clicks beside the capsule still reach the strip window")
	}
}

// TestShakeMovesTheCapsuleHitBox: the error capsule's shake shifts the
// drawn shell; the hit box must shift with it or presses miss the moving
// capsule.
func TestShakeMovesTheCapsuleHitBox(t *testing.T) {
	a := motionShelf()
	a.fx.shakeX = -fbShakeX
	withShake := a.peekPillRect()
	a.fx.shakeX = 0
	plain := a.peekPillRect()
	if withShake.X != plain.X-fbShakeX {
		t.Fatalf("shake not followed: box X %d, want %d", withShake.X, plain.X-fbShakeX)
	}
}

// TestPausedCapsuleDefersTheSharedDeadline: a capsule paused by hover
// must extend the status's panel deadline — the words parked off screen
// must not be cleared mid-pause, and the remaining hold resumes on
// un-pause.
func TestPausedCapsuleDefersTheSharedDeadline(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.toastStatus("已复制", time.Now().Add(1200*time.Millisecond), statusOK)
	view.Frame()
	view.Advance(200 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn || !a.fb.armed {
		t.Fatal("the capsule never showed")
	}
	// Hover parks the capsule; the panel deadline passes mid-pause.
	a.peeking = true
	view.Frame()
	view.Advance(1500 * time.Millisecond)
	view.Frame()
	if !a.fb.pausedAt.IsZero() == false && a.fb.text == "" {
		t.Fatal("the paused capsule lost its words")
	}
	if a.status == "" {
		t.Fatal("the panel deadline cleared a status still parked in the capsule")
	}
	// Un-pause: the hold resumes where it stopped.
	a.peeking = false
	view.Frame()
	view.Advance(400 * time.Millisecond)
	view.Frame()
	if !a.fb.drawn {
		t.Fatal("the capsule did not resume after the pause")
	}
}

// TestPlayedOutCapsuleNeverReplays: a capsule that ran its whole span is
// done — a short open/close must not post it again.
func TestPlayedOutCapsuleNeverReplays(t *testing.T) {
	a := motionWindow()
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	a.toastStatus("已复制", time.Now().Add(800*time.Millisecond), statusOK)
	view.Frame()
	// Let it play out fully: the hold floors at expand+holdFloor (~2s), so
	// run past it and the fold-back.
	view.Advance(2600 * time.Millisecond)
	for range 30 {
		view.Frame()
		view.Advance(16 * time.Millisecond)
	}
	if a.fb.drawn {
		t.Fatal("the capsule still shows after its hold")
	}
	// Panel opens and closes quickly: nothing new may appear on the notch.
	a.visible, a.progress = true, 1
	view.Frame()
	a.hide(hideExplicit)
	a.progress = 0
	view.Frame()
	view.Advance(600 * time.Millisecond)
	view.Frame()
	// The played-out words may still linger until the fold clears them —
	// what must not happen is a fresh capsule drawing.
	if a.fb.drawn {
		t.Fatalf("a played-out capsule replayed on panel close (text=%q)", a.fb.text)
	}
}
