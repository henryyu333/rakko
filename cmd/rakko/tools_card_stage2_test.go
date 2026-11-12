//go:build darwin

package main

// Stage 2 card components: the timer's select-then-start flow, the +1
// minute action, the drag count badge in place of the old subtitle, the
// busy spinner, and the per-digit value roll.

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/layout"
	"rakko/internal/settings"
)

func stage2Shelf(t *testing.T, cards []layout.Card) (*shelf, *clockModule, time.Time) {
	t.Helper()
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), level: 2, visible: true, tab: pageTools,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: cards}
	now := time.Date(2026, 11, 11, 9, 41, 0, 0, time.Local)
	m := newClockModule(a)
	m.now = func() time.Time { return now }
	m.playSound = func() {}
	m.stopSound = func() {}
	a.clock = m
	return a, m, now
}

// TestTimerPresetThenStart drives the v3 §5 flow on the real page: a
// preset only selects the length — the chip turns accent and the hero
// rolls to it — and the ▶ in the action row starts it.
func TestTimerPresetThenStart(t *testing.T) {
	a, m, now := stage2Shelf(t, []layout.Card{{ID: "timer", Span: 2, Rows: 1}})
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	l := a.appLoc()
	r, ok := view.Find("10")
	if !ok {
		t.Fatalf("the 10-minute preset is missing; texts %v", view.Texts())
	}
	view.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	view.Frame()
	if m.phase != timerIdle || m.dur != 10*time.Minute {
		t.Fatalf("choosing 10 minutes = phase %v dur %v, want still idle", m.phase, m.dur)
	}
	if _, ok := view.Find("10:00"); !ok {
		t.Fatalf("the reading did not follow the preset; texts %v", view.Texts())
	}
	start, ok := view.Find(l.TimerStart())
	if !ok {
		t.Fatalf("▶ is missing from the idle card; texts %v", view.Texts())
	}
	view.ClickAt(start.X+start.W/2, start.Y+start.H/2)
	view.Frame()
	if m.phase != timerRunning || !m.end.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("▶ started phase %v end %v, want 10 minutes from now", m.phase, m.end)
	}
}

// TestTimerPlusOneMinute: the running card's secondary pill extends the
// countdown by a minute; the old card had no such control.
func TestTimerPlusOneMinute(t *testing.T) {
	a, m, now := stage2Shelf(t, []layout.Card{{ID: "timer", Span: 2, Rows: 1}})
	m.startTimer(now, 5*time.Minute)
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	l := a.appLoc()
	r, ok := view.Find(l.TimerPlusOne())
	if !ok {
		t.Fatalf("the +1 分钟 pill is missing; texts %v", view.Texts())
	}
	view.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	view.Frame()
	if !m.end.Equal(now.Add(6 * time.Minute)) {
		t.Fatalf("+1 minute ended at %v, want %v", m.end, now.Add(6*time.Minute))
	}
	if m.dur < 6*time.Minute {
		t.Fatalf("the card's length is %v, want at least 6 minutes", m.dur)
	}
}

// TestToolActionCountBadge: while files an action card accepts are over
// the page, the card shows the accent treatment and an 18pt count badge
// in the corner — the old subtitle is gone.
func TestToolActionCountBadge(t *testing.T) {
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), tab: pageTools, level: 2, visible: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "zip"}}}
	a.drop = dropZoneState{files: []string{"a.png", "b.png"}, class: classImages, loaded: true}
	a.dragging, a.dragOpenTools = true, true
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	r, ok := view.Find("2")
	if !ok {
		t.Fatalf("the count badge is missing; texts %v", view.Texts())
	}
	if r.H > 18.5 || r.W < 17.5 {
		t.Fatalf("the count badge is %+v, want an 18pt capsule", r)
	}
	card, ok := view.Find(a.appLoc().CardZip())
	if !ok {
		t.Fatalf("the action card is missing")
	}
	if r.X < card.X+card.W/2 || r.Y > card.Y+card.H/2 {
		t.Fatalf("the badge at %+v is not in the card's top right", r)
	}
}

// TestToolBusyCardSpins: the card whose conversion runs draws the
// spinner and stays legible; it is not faded like the old busy page.
func TestToolBusyCardSpins(t *testing.T) {
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), tab: pageTools, level: 2, visible: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.ensurePages()
	a.toolsLay = &layout.Layout{Version: layout.Version, Cards: []layout.Card{{ID: "tojpeg"}}}
	a.conv = &converter{host: a, job: &convertJob{target: toJPEG}}
	if !a.toolCardBusy("tojpeg") || a.toolCardBusy("zip") {
		t.Fatalf("busy mapping: tojpeg=%v zip=%v", a.toolCardBusy("tojpeg"), a.toolCardBusy("zip"))
	}
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	name := a.appLoc().CardToJPEG()
	r, ok := view.Find(name)
	if !ok {
		t.Fatalf("the busy card lost its name; texts %v", view.Texts())
	}
	img := view.Image()
	bright := 0
	for y := int(r.Y + 2); y < int(r.Y+r.H-2); y++ {
		for x := int(r.X + 2); x < int(r.X+r.W-2); x++ {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			if pr>>8 > 150 && pg>>8 > 150 && pb>>8 > 150 {
				bright++
			}
		}
	}
	if bright == 0 {
		t.Fatalf("the busy card's name is faded out (no bright pixels in %+v)", r)
	}
}

// TestCardRollDirection: the roll's direction comes from the reading, not
// its length; non-numeric or equal values do not roll.
func TestCardRollDirection(t *testing.T) {
	cases := []struct {
		old, new string
		want     int
	}{
		{"4:12", "4:11", -1},
		{"4:11", "4:12", 1},
		{"1:02:03", "59:59", -1},
		{"∞", "42:00", 0},
		{"42:00", "42:00", 0},
	}
	for _, tc := range cases {
		if got := cardRollDirection(tc.old, tc.new); got != tc.want {
			t.Errorf("%q -> %q direction = %d, want %d", tc.old, tc.new, got, tc.want)
		}
	}
}

// TestTimerHeroRollsChangedDigits: a rising reading keeps the old digit
// on screen while the new one comes in, so the roll is per digit; after
// the 280 ms it shows only the new reading.
func TestTimerHeroRollsChangedDigits(t *testing.T) {
	a, m, now := stage2Shelf(t, []layout.Card{{ID: "timer", Span: 2, Rows: 1}})
	m.startTimer(now, 5*time.Minute)
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	if _, ok := view.Find("5:00"); !ok {
		t.Fatalf("the running reading is missing; texts %v", view.Texts())
	}
	m.addMinute(now)
	view.Advance(16 * time.Millisecond)
	view.Frame()
	texts := view.Texts()
	has := func(s string) bool {
		for _, x := range texts {
			if x == s {
				return true
			}
		}
		return false
	}
	if !has("5") || !has("6") {
		t.Fatalf("the changed digit did not roll (old 5 / new 6); texts %v", texts)
	}
	view.Advance(400 * time.Millisecond)
	view.Frame()
	if _, ok := view.Find("6:00"); !ok {
		t.Fatalf("the roll did not land on 6:00; texts %v", view.Texts())
	}
}

// TestTimerEditCardStartsFromPlay: entering the custom length editor keeps
// the new skeleton — glyph + three fields on the header, presets and the
// warning ▶ in the action row — and ▶ starts the typed length with the
// mouse. Before this, the edit state had no start control at all.
func TestTimerEditCardStartsFromPlay(t *testing.T) {
	a, m, now := stage2Shelf(t, []layout.Card{{ID: "timer", Span: 2, Rows: 1}})
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	hero, ok := view.Find("5:00")
	if !ok {
		t.Fatalf("the idle reading is missing; texts %v", view.Texts())
	}
	view.ClickAt(hero.X+hero.W/2, hero.Y+hero.H/2)
	view.Frame()
	if m.edit == nil {
		t.Fatalf("the reading did not open the editor; texts %v", view.Texts())
	}
	preset, ok := view.Find("10")
	if !ok {
		t.Fatalf("the edit state lost its presets; texts %v", view.Texts())
	}
	view.ClickAt(preset.X+preset.W/2, preset.Y+preset.H/2)
	view.Frame()
	if m.edit == nil {
		t.Fatal("choosing a preset closed the editor")
	}
	if got := m.edit.value(); got != 10*time.Minute {
		t.Fatalf("the preset set %v in the editor, want 10 minutes", got)
	}
	start, ok := view.Find(a.appLoc().TimerStart())
	if !ok {
		t.Fatalf("the edit state has no ▶; texts %v", view.Texts())
	}
	view.ClickAt(start.X+start.W/2, start.Y+start.H/2)
	view.Frame()
	if m.phase != timerRunning || !m.end.Equal(now.Add(10*time.Minute)) {
		t.Fatalf("▶ started phase %v end %v, want 10 minutes from now", m.phase, m.end)
	}
	if m.edit != nil {
		t.Fatal("starting from the editor left it open")
	}
}

// TestCardHeroRollRequestsFrames: a changed hero value keeps asking for
// frames until the 150–280 ms roll/fade lands, and stops afterwards. A
// preset click has no other animation to carry the repaint, so without
// this the digits freeze mid-flight.
func TestCardHeroRollRequestsFrames(t *testing.T) {
	a, m, _ := stage2Shelf(t, []layout.Card{{ID: "timer", Span: 2, Rows: 1}})
	view := toolsTester(a)
	view.Frame()
	view.Frame()
	if view.FramePending() {
		t.Fatal("a settled card still asks for a frame")
	}
	m.dur = 10 * time.Minute
	m.refresh()
	view.Frame()
	if !view.FramePending() {
		t.Fatal("the hero change did not ask for the next frame")
	}
	view.Advance(100 * time.Millisecond)
	view.Frame()
	if !view.FramePending() {
		t.Fatal("the roll stopped asking for frames mid-flight")
	}
	view.Advance(300 * time.Millisecond)
	view.Frame()
	if view.FramePending() {
		t.Fatal("the settled hero still asks for frames")
	}
}
