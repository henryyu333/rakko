//go:build darwin

package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/mac"
	"rakko/internal/store"
)

func pinReviewDrag(t *testing.T, a *shelf, p *mygo.Point, active bool) {
	t.Helper()
	oldP, oldS, oldT, oldD := cursorPoint, dragState, dragTypes, dragLeftDown
	cursorPoint = func() mygo.Point { return *p }
	dragState = func() (bool, int64) { return active, 7 }
	dragTypes = func() []string { return []string{"public.file-url"} }
	dragLeftDown = func() bool { return true }
	a.dragSerial = 7
	t.Cleanup(func() { cursorPoint, dragState, dragTypes, dragLeftDown = oldP, oldS, oldT, oldD })
}

func TestPinReturnZoneHoldsPeek(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := motionShelf()
	a.notchX, a.notchY, a.notchW, a.notchH = 600, 0, 180, 32
	a.dispBounds = pinTestDisplays[0].Bounds
	p := mygo.Point{X: 690, Y: 78}
	pinReviewDrag(t, a, &p, false)
	a.pinRegister(&pinItem{cardID: "zip", cardW: pinCardW, display: 1, pressed: true, moving: true,
		frame: mygo.Rectangle{X: 690, Y: 78, Width: pinCardW, Height: pinCardH}})
	for range 4 {
		a.monitorDrag(time.Now())
		if !a.pinReturning || !a.peeking {
			t.Fatalf("return invitation folded inside the release zone: returning=%v peeking=%v", a.pinReturning, a.peeking)
		}
	}
	p.Y = 300
	a.monitorDrag(time.Now())
	if a.pinReturning || a.peeking {
		t.Fatal("return invitation survived leaving the release zone")
	}
}

func TestPinReturnPillBeatsOtherContent(t *testing.T) {
	for _, state := range []string{"player", "capsule", "retracting"} {
		t.Run(state, func(t *testing.T) {
			a := motionWindow()
			a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Playing: true, Title: "Review Track"}}
			if state != "player" {
				a.toastStatus("已复制", time.Now().Add(time.Hour), statusOK)
				a.fb.retracting = state == "retracting"
			}
			if state == "capsule" && !a.capsuleUp(time.Now()) {
				t.Fatal("completion capsule fixture is not visible")
			}
			a.pinReturning, a.peekProgress = true, 1
			a.peeking = state != "capsule"
			w, h := a.peekSize()
			if w != float32(max(180, a.notchW))+dropPillExtraW || h != float32(max(32, a.notchH))+dropPillBody {
				t.Fatalf("wrong return shape: %.1fx%.1f", w, h)
			}
			view := ui.NewTester(a.view, sheetW, panelTestH)
			view.Frame()
			if !view.HasText("松开放回") || view.HasText("Review Track") || view.HasText("已复制") || a.fx.peekKind != peekReturn {
				t.Fatalf("return invitation obscured: kind=%v texts=%v", a.fx.peekKind, view.Texts())
			}
		})
	}
}

func TestPinSuppressedDragStillWakes(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := motionShelf()
	a.dispBounds = pinTestDisplays[0].Bounds
	a.suppressed = true
	it := a.pinRegister(&pinItem{cardID: "zip", cardW: pinCardW, display: 1, edge: pinRight,
		frame: pinStripFrame(pinRight, pinTestArea, 400)})
	p := mygo.Point{X: 1470, Y: 400}
	pinReviewDrag(t, a, &p, true)
	a.monitorDrag(time.Now())
	if !it.expanded || a.visible || !a.suppressed {
		t.Fatalf("suppressed drag: expanded=%v visible=%v suppressed=%v", it.expanded, a.visible, a.suppressed)
	}
	a.monitorDrag(time.Now().Add(2 * pinHideDelay))
	if !it.expanded || !it.leftAt.IsZero() {
		t.Fatal("file drag near the pin did not hold it open")
	}
}

func TestPinMenuAlwaysToggleRestartsDelay(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a, it, tr := pinViewShelf(t, "zip", true)
	it.always, it.leftAt = true, time.Now().Add(-time.Hour)
	tr.RightClickAt(40, 40)
	if err := tr.ChooseMenuItem("始终展开"); err != nil {
		t.Fatal(err)
	}
	if it.always {
		t.Fatal("always-open was not disabled")
	}
	t0 := time.Now()
	p := mygo.Point{X: 600, Y: 600}
	a.pinTick(t0, p, false, false)
	a.pinTick(t0.Add(pinHideDelay-time.Nanosecond), p, false, false)
	if !it.expanded {
		t.Fatal("collapsed before the fresh 600 ms delay")
	}
	a.pinTick(t0.Add(pinHideDelay), p, false, false)
	if it.expanded {
		t.Fatal("did not collapse at 600 ms")
	}
}

func TestPinWakeDragBeforeNextPoll(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	a := motionShelf()
	a.dispBounds = pinTestDisplays[0].Bounds
	a.suppressed = true
	it := a.pinRegister(&pinItem{cardID: "zip", cardW: pinCardW, display: 1, edge: pinRight,
		frame: pinStripFrame(pinRight, pinTestArea, 400)})
	p := mygo.Point{X: 1470, Y: 400}
	pinReviewDrag(t, a, &p, true)
	a.pointerDragged() // no 30 ms poll before the release
	if !it.expanded || it.frame.Width != pinCardW || !insideRect(p, it.frame) {
		t.Fatalf("release point has no open drop target: %+v", it.frame)
	}
}

func TestPinDragOutKeepsGridSpan(t *testing.T) {
	withPinDisplays(t, pinTestDisplays)
	oldOpen, oldCursor := pinOpenWindow, cursorPoint
	pinOpenWindow = func(*pinItem) {}
	t.Cleanup(func() { pinOpenWindow, cursorPoint = oldOpen, oldCursor })
	a := &shelf{dataDir: t.TempDir()}
	a.mutateTools(func(l *layout.Layout) { l.Cards = []layout.Card{{ID: "topdf", Span: 2}} })
	p := &toolsPage{host: a, editing: true}
	a.x, a.y = 400, 0
	cursorPoint = func() mygo.Point { return mygo.Point{X: 1400, Y: 500} }
	p.toolCardDrop(&toolPress{id: "topdf", dragging: true, grabDX: 10, grabDY: 10, px: 200, py: 1200}, cursorPoint())
	if len(a.pins) != 1 || a.pins[0].span != 2 || a.pins[0].cardW != pinCardW2 {
		t.Fatalf("grid width lost: %+v", a.pins)
	}
	a.unpin(a.pins[0], true)
	cards := a.toolsLayout().Cards
	if len(cards) != 1 || cards[0].ID != "topdf" || cards[0].Span != 2 {
		t.Fatalf("return lost width: %+v", cards)
	}
}

func TestPinBusyTapShowsSharedFeedback(t *testing.T) {
	a, it, tr := pinViewShelf(t, "zip", true)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a.store = s
	file := writeFile(t, t.TempDir(), "selected.txt", []byte("review"))
	s.Items = []store.Item{{ID: "selected", Kind: store.KindFile, Path: filepath.Clean(file)}}
	a.selection.onlyAll([]string{"selected"})
	job := &convertJob{target: toJPEG}
	a.conv = &converter{host: a, job: job}
	tr.Press(20, 20)
	tr.Release(20, 20)
	if a.fb.text != "一次只跑一个任务，请稍候" || a.fb.kind != statusInfo || it.jiggleAt.IsZero() {
		t.Fatalf("busy tap missing shared feedback: %+v jiggle=%v", a.fb, it.jiggleAt)
	}
	if a.conv.job != job || len(s.Items) != 1 {
		t.Fatal("busy tap changed the running job or tray")
	}
}
