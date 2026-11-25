package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/platform"
	"rakko/locales"
)

// awakeModule keeps the Mac from idle-sleeping for a chosen span, meant
// for letting a long task finish. One named IOKit power assertion
// (「Rakko Stay Awake」) holds while the feature is on: PreventUserIdleSystemSleep
// by default — the screen may still dim and turn off — or
// PreventUserIdleDisplaySleep while 「屏幕也保持亮着」 is chosen. Timed
// holds release on their deadline, on 关闭, and when Rakko quits;
// 一直 never expires. Nothing is persisted: a relaunch starts off.
//
// The module runs off the shared monitoring tick and throttles itself to
// a second-granularity countdown redraw; it owns no goroutine. The power
// calls are seam functions so tests never touch IOKit.
type awakeModule struct {
	host *shelf

	// State: id 0 means off; duration 0 with an id means 「一直」.
	id       platform.PowerAssertionID
	display  bool // 「屏幕也保持亮着」
	duration time.Duration
	until    time.Time // deadline for timed holds; zero for 「一直」
	remain   string    // last countdown text drawn, to redraw on change

	// create/release and now are test seams; nil means the real calls.
	create  func(platform.PowerAssertionKind) (platform.PowerAssertionID, error)
	release func(platform.PowerAssertionID)
	now     func() time.Time
}

// awakeDuration is one choice of the 保持唤醒 submenu: its label and the
// span it holds, 0 for 一直.
type awakeDuration struct {
	label string
	d     time.Duration
}

// awakeDurations are the timed choices of the 保持唤醒 submenu, in order.
func awakeDurations(l locales.Messages) []awakeDuration {
	return []awakeDuration{
		{l.AwakeMinutes(30), 30 * time.Minute},
		{l.AwakeHours(1), time.Hour},
		{l.AwakeHours(2), 2 * time.Hour},
		{l.AwakeHours(4), 4 * time.Hour},
		{l.AwakeForever(), 0},
	}
}

// newAwakeModule wires the feature into the shell; the quit handler makes
// sure a held assertion is released even when the deadline never comes —
// the OS would drop it at exit anyway, this just keeps pmset honest.
func newAwakeModule(host *shelf) *awakeModule {
	m := &awakeModule{host: host}
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { m.stop(time.Now()) })
	return m
}

// toggleAwake is the 保持唤醒 card's tap and its global shortcut: a hold
// that runs stops, otherwise a one-hour hold starts.
func (a *shelf) toggleAwake() {
	m := a.awake
	if m == nil {
		return
	}
	if m.active() {
		m.stop(time.Now())
	} else {
		m.start(time.Now(), time.Hour)
	}
}

// awakeMenu is the 保持唤醒 submenu of the tray menu: the timed choices
// with the current one checked, the display toggle, and 关闭 greyed out
// while nothing is held. Rebuilt with the menu on every right-click.
func (a *shelf) awakeMenu() []*mygo.MenuItem {
	m := a.awake
	l := a.appLoc()

	var items []*mygo.MenuItem
	for _, c := range awakeDurations(l) {
		d := c.d
		items = append(items, &mygo.MenuItem{
			Label: c.label, Type: mygo.MenuItemRadio,
			Checked: m.active() && m.duration == d,
			Click:   func(*mygo.MenuItem, *mygo.Window) { m.start(time.Now(), d) },
		})
	}
	items = append(items,
		mygo.Separator(),
		&mygo.MenuItem{
			Label: l.AwakeKeepScreen(), Type: mygo.MenuItemCheckbox, Checked: m.wantsDisplay(),
			Click: func(*mygo.MenuItem, *mygo.Window) { m.setDisplay(time.Now(), !m.display) },
		},
		mygo.Separator(),
		&mygo.MenuItem{
			Label: l.AwakeOff(), Disabled: !m.active(),
			Click: func(*mygo.MenuItem, *mygo.Window) { m.stop(time.Now()) },
		},
	)
	return items
}

// awakeAssertionName is the name powerd records for the hold (AssertName).
// ASCII only: `pmset -g assertions` prints an empty `named:` for any
// non-ASCII name, so this keeps the hold visible in that listing.
const awakeAssertionName = "Rakko Stay Awake"

func (m *awakeModule) active() bool { return m != nil && m.id != 0 }

// wantsDisplay reads 屏幕也保持亮着; nil-safe like active, for menu
// building on shelves that never ran ready().
func (m *awakeModule) wantsDisplay() bool { return m != nil && m.display }

// start turns the feature on for d (0 = 一直); a fresh pick while on just
// re-arms the timer — the same assertion type stays held, so there is no
// sleep gap.
func (m *awakeModule) start(now time.Time, d time.Duration) {
	m.duration, m.until = d, time.Time{}
	if d > 0 {
		m.until = now.Add(d)
	}
	if m.active() {
		m.refresh(now)
		if m.host != nil {
			m.host.wakeMonitor()
		}
		return
	}
	if err := m.reassert(); err != nil {
		m.duration, m.until = 0, time.Time{}
		m.fail(err)
	}
	m.refresh(now)
	if m.host != nil {
		m.host.wakeMonitor()
	}
}

// stop releases the assertion, if any.
func (m *awakeModule) stop(now time.Time) {
	if !m.active() {
		return
	}
	m.releaseAssert(m.id)
	m.id = 0
	m.duration, m.until = 0, time.Time{}
	m.refresh(now)
	if m.host != nil {
		m.host.wakeMonitor()
	}
}

// setDisplay flips 屏幕也保持亮着. While the feature is on it swaps the
// held assertion for the other type, creating the new one first so a
// failure never leaves a gap; on failure the toggle rolls back.
func (m *awakeModule) setDisplay(now time.Time, on bool) {
	if m.display == on {
		return
	}
	m.display = on
	if m.active() {
		if err := m.reassert(); err != nil {
			m.display = !on
			m.fail(err)
		}
	}
	m.refresh(now)
	m.host.invalidatePins("awake")
}

// reassert replaces the held assertion with one matching m.display. The
// new assertion is created before the old id is released, so a failed
// create keeps the old hold and reports an error instead.
func (m *awakeModule) reassert() error {
	id, err := m.createAssert(m.wantKind())
	if err != nil {
		return err
	}
	if m.id != 0 {
		m.releaseAssert(m.id)
	}
	m.id = id
	return nil
}

// wantKind is the assertion type the current display flag asks for.
func (m *awakeModule) wantKind() platform.PowerAssertionKind {
	if m.display {
		return platform.PowerAssertDisplaySleep
	}
	return platform.PowerAssertSystemSleep
}

// fail reports a user-requested assertion failure in the panel's toast or
// the closed notch's capsule without opening the panel over another app.
func (m *awakeModule) fail(err error) {
	m.host.toastStatus(m.host.appLoc().AwakeFailed(err.Error()), m.clock().Add(4*time.Second), statusErr)
}

func (m *awakeModule) showingCountdown() bool {
	return m.host != nil && (m.host.visible || m.host.hasExpandedPin("awake"))
}

// tick releases a timed hold at its deadline and keeps the countdown
// moving at second granularity — only while the panel or an awake pin shows it.
func (m *awakeModule) tick(now time.Time) {
	if !m.active() {
		return
	}
	if !m.until.IsZero() && !now.Before(m.until) {
		m.stop(now)
		return
	}
	if m.showingCountdown() {
		m.refresh(now)
	}
}

// live reports whether the sheet's left ear shows the awake chip.
func (m *awakeModule) live() bool { return m.active() }

// refresh redraws the panel and awake pins when the countdown text changed.
func (m *awakeModule) refresh(now time.Time) {
	next := m.remainText(now)
	if next == m.remain {
		return
	}
	m.remain = next
	if m.host.win != nil {
		m.host.win.Invalidate()
	}
	m.host.invalidatePins("awake")
}

// remainText is the chip's tooltip line: a m:ss / h:mm:ss countdown for
// timed holds, 「直到关闭」 for 一直.
func (m *awakeModule) remainText(now time.Time) string {
	if !m.active() {
		return ""
	}
	return m.remainTextIn(m.host.appLoc(), now)
}

func (m *awakeModule) remainTextIn(l locales.Messages, now time.Time) string {
	if m.until.IsZero() {
		return l.AwakeUntilOff()
	}
	rem := m.until.Sub(now)
	if rem < 0 {
		rem = 0
	}
	return l.AwakeRemaining(formatClock(rem.Seconds()))
}

func (m *awakeModule) clock() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m *awakeModule) createAssert(kind platform.PowerAssertionKind) (platform.PowerAssertionID, error) {
	if m.create != nil {
		return m.create(kind)
	}
	return platform.NewPowerAssertion(kind, awakeAssertionName)
}

func (m *awakeModule) releaseAssert(id platform.PowerAssertionID) {
	if m.release != nil {
		m.release(id)
		return
	}
	platform.ReleasePowerAssertion(id)
}

// awakeSymbol is the design's slashed-moon glyph.
var awakeSymbol = symbol(`<path d="M20.2 14.5A8.5 8.5 0 0 1 9.5 3.8a8.5 8.5 0 1 0 10.7 10.7z"/><path d="m4.5 19.5 15-15"/>`)

// chip is the keep-awake capsule: a moon glyph whose tooltip carries the
// remaining time. Tapping stops the hold.
func (m *awakeModule) chip(c *ui.Context) ui.Element {
	if !m.active() {
		return ui.Box(c).Size(0, 0)
	}
	now := m.clock()
	remain := m.remainText(now)
	l := loc(c)
	chip := ui.Row(c.Key("awake-chip")).Size(chipH, chipH).Radius(chipH / 2).Background(fill1).
		Center().Label(l.AwakeTitle()).Tooltip(l.AwakeChipTooltip(remain))
	chip.Children(func() {
		ui.Icon(c, awakeSymbol).Size(13, 13).TextColor(ink)
	})
	if chip.Clicked() {
		m.stop(now)
	}
	return chip
}

// nextTick is the hold's expiry, or the next countdown repaint while the
// panel or an expanded desktop awake pin shows it.
func (m *awakeModule) nextTick(now time.Time) time.Time {
	if !m.active() {
		return time.Time{}
	}
	if m.showingCountdown() {
		sec := now.Add(time.Second)
		if !m.until.IsZero() && m.until.Before(sec) {
			return m.until
		}
		return sec
	}
	return m.until
}
