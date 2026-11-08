//go:build darwin

package main

import (
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/note"
	"rakko/locales"
)

// The 便利贴 card and its window (design §6): one Markdown file edited in
// a floating 320 × 380 window. Typing saves 400 ms after it stops, with a
// temporary file renamed over the note; there is no save button and no
// 已保存 line. The window takes the keyboard to type but stays out of the
// Dock and ⌘Tab (the app is an accessory already), and a change made to
// the file by another editor shows up here when nothing is unsaved.
const (
	noteSaveDelay = 400 * time.Millisecond // 停止输入多久后落盘
	noteW         = 320
	noteH         = 380
	noteBarH      = 30
	noteMinW      = 240
	noteMinH      = 200
	notePreviewN  = 3         // the 2 × 1 card's preview lines
	noteBg        = "#1C1C1E" // surfaceRaise, the window's fill before the view paints
)

var (
	// noteSymbol is the design's sticky note glyph; finderSymbol the
	// folder on the bar's 在访达中显示 button.
	noteSymbol   = symbol(`<path d="M5 3.5h14V15l-4.5 5H5z"/><path d="M14.5 20v-5H19M8.5 8.5h7M8.5 12h4.5"/>`)
	finderSymbol = symbol(`<path d="M3.5 6.5A1.5 1.5 0 0 1 5 5h3.8L11 7h8a1.5 1.5 0 0 1 1.5 1.5v9.5A1.5 1.5 0 0 1 19 19.5H5a1.5 1.5 0 0 1-1.5-1.5z"/>`)

	// Seams around the app-level calls, as rename.go keeps them, so a
	// headless test can drive the session without a window server.
	noteActivate  = mac.ActivateSelf
	noteReveal    = mac.RevealInFinder
	noteShow      = func(w *mygo.Window) { w.Show() }
	noteFocus     = func(w *mygo.Window) { w.Focus() }
	noteClose     = func(w *mygo.Window) { w.Close() }
	noteNewWindow = func(opts mygo.WindowOptions) *mygo.Window { return mygo.NewWindow(opts) }
	// notePinRedraw repaints the note's desktop card, whose window only
	// exists with a window server.
	notePinRedraw = func(it *pinItem) {
		if it.win != nil {
			it.win.Invalidate()
		}
	}
)

// noteUI is the note behind the 便利贴 card and, while it is open, its
// window. text is the file as the last read, save or outside change left
// it — what the card draws; draft is the editor's text and equals text
// while nothing is unsaved.
type noteUI struct {
	host  *shelf
	store *note.Store

	win     *mygo.Window
	booted  bool
	top     bool
	prevPID int
	frame   mygo.Rectangle // last frame, so a reopen keeps the spot this launch

	text  string
	draft string
	seen  string    // draft as of the last tick, for the save delay
	since time.Time // when the draft last changed
	// locked is set while the file could not be read: an empty editor over
	// a note nobody has seen would overwrite it, so the window shows the
	// failure instead of an editable copy and nothing is written back.
	locked bool

	watch *note.Watch
}

// noteSession is the note behind the card, created — with its file and
// its change watch — the first time the card is drawn or opened. Nil
// without a data directory or when the folder cannot be made; the card
// then shows an empty note.
func (a *shelf) noteSession() *noteUI {
	if a.note != nil {
		return a.note
	}
	if a.dataDir == "" {
		return nil
	}
	store, err := note.Open(a.dataDir)
	if err != nil {
		a.report(err)
		return nil
	}
	n := &noteUI{host: a, store: store}
	if err := n.load(); err != nil {
		a.report(err)
	}
	if w, werr := note.WatchFile(store.Path(), func() {
		mygo.RunOnMain(n.host.noteOutside)
	}); werr == nil {
		n.watch = w
	} else {
		a.report(werr)
	}
	a.note = n
	return n
}

// load reads the note into text and draft. A note that cannot be read stays
// locked: text and draft keep whatever they had, and no save may run until a
// read succeeds.
func (n *noteUI) load() error {
	text, err := n.store.Read()
	if err != nil {
		n.locked = true
		return err
	}
	n.locked = false
	n.text, n.draft, n.seen = text, text, text
	return nil
}

// selectNote makes another note the one the card edits and reads it in:
// the watch follows the new file, so a change to either still lands. The
// current draft is written first, so a search row never costs a sentence.
// A note that cannot be read keeps the window locked, as load does.
func (n *noteUI) selectNote(id string) error {
	if id == n.store.ID() {
		return nil
	}
	if err := n.flush(); err != nil {
		return err
	}
	if err := n.store.Select(id); err != nil {
		return err
	}
	if n.watch != nil {
		n.watch.Close()
		n.watch = nil
	}
	a := n.host
	if err := n.load(); err != nil {
		a.report(err)
	}
	if w, err := note.WatchFile(n.store.Path(), func() {
		mygo.RunOnMain(a.noteOutside)
	}); err == nil {
		n.watch = w
	} else {
		a.report(err)
	}
	n.invalidate()
	return nil
}

// newNote starts a fresh note beside the current one, loads it and brings
// its window up — the global shortcut's action.
func (a *shelf) newNote() {
	n := a.noteSession()
	if n == nil {
		return
	}
	if err := n.newNote(); err != nil {
		a.report(err)
		return
	}
	a.hide(hideExplicit)
	openNoteFront(n)
}

// newNote makes a fresh empty note the one the card edits: the draft is
// written first, so the old note never loses a sentence.
func (n *noteUI) newNote() error {
	if err := n.flush(); err != nil {
		return err
	}
	if err := n.store.New(); err != nil {
		return err
	}
	if n.watch != nil {
		n.watch.Close()
		n.watch = nil
	}
	a := n.host
	if err := n.load(); err != nil {
		a.report(err)
	}
	if w, err := note.WatchFile(n.store.Path(), func() {
		mygo.RunOnMain(a.noteOutside)
	}); err == nil {
		n.watch = w
	} else {
		a.report(err)
	}
	a.noteStale()
	n.invalidate()
	return nil
}

// dirty reports unsaved text in the editor.
func (n *noteUI) dirty() bool { return n.draft != n.text }

// tick writes the draft once it has been still for noteSaveDelay and
// reports whether it wrote: the debounce the design asks for, driven by
// the app's own 30 ms tick so no timer of its own runs.
func (n *noteUI) tick(now time.Time) bool {
	if n.locked {
		return false
	}
	if !n.dirty() {
		n.since = time.Time{}
		n.seen = n.draft
		return false
	}
	if n.draft != n.seen {
		n.seen, n.since = n.draft, now
	}
	if n.since.IsZero() || now.Sub(n.since) < noteSaveDelay {
		return false
	}
	if err := n.flush(); err != nil {
		n.host.report(err)
	}
	return true
}

// flush writes the draft now: a close, Esc, the panel folding away or the
// app quitting never drops what was typed.
func (n *noteUI) flush() error {
	if n.locked || !n.dirty() {
		return nil
	}
	if err := n.store.Write(n.draft); err != nil {
		return err
	}
	n.text, n.seen, n.since = n.draft, n.draft, time.Time{}
	n.host.noteStale() // the search's index has older text
	n.invalidate()     // the card's heading and preview follow the file
	return nil
}

// adopt takes a change another program made to the file, unless the
// window holds unsaved input: then the user's text wins until it is
// saved, and the card keeps showing the file's own copy.
func (n *noteUI) adopt(text string) bool {
	if n.dirty() || text == n.text {
		return false
	}
	n.text, n.draft, n.seen = text, text, text
	return true
}

// invalidate redraws the card and the window, and the note's pinned card:
// a free pin does not repaint on its own, so its heading and preview would
// keep the old text until something else touched it.
func (n *noteUI) invalidate() {
	if n.win != nil {
		n.win.Invalidate()
	}
	if n.host.win != nil {
		n.host.win.Invalidate()
	}
	for _, it := range n.host.pins {
		if it.cardID == "note" {
			notePinRedraw(it)
		}
	}
}

// noteOutside runs on the main thread when the watch sees the file
// change; our own saves land here too and change nothing.
func (a *shelf) noteOutside() {
	if a.quitting {
		return
	}
	n := a.note
	if n == nil {
		return
	}
	text, err := n.store.Read()
	if err != nil {
		a.report(err)
		return
	}
	a.noteStale() // another editor wrote; the search's index has older text
	if n.locked {
		// The file is readable again (an editor's save replaced it, or the
		// permissions came back): the window returns to editing it.
		n.locked = false
		n.text, n.draft, n.seen = text, text, text
		n.invalidate()
		return
	}
	if n.adopt(text) {
		n.invalidate()
	}
}

// noteTick is the module tick: it releases the panel's global Esc and
// Space while the window is up (a held Space never reaches a text field)
// and lands a debounced save when its delay is over.
type noteModule struct{ host *shelf }

func (noteModule) live() bool { return false }

func (m noteModule) tick(now time.Time) {
	n := m.host.note
	if n == nil {
		return
	}
	if n.win != nil {
		m.host.releaseKeys()
	}
	n.tick(now)
}

// noteFlush lands anything unsaved; the panel folding away and the app
// quitting both call it.
func (a *shelf) noteFlush() {
	if a.note == nil {
		return
	}
	if err := a.note.flush(); err != nil {
		a.report(err)
	}
}

// toggleNote opens the 便利贴 window, or closes it when it is up — the
// card's tap, and the same tap on the pinned card.
func (a *shelf) toggleNote() bool {
	n := a.noteSession()
	if n == nil {
		return false
	}
	if n.win != nil {
		n.close()
		return true
	}
	return n.open()
}

// open shows the window: under the pinned card when the card is on the
// desktop, else under the notch panel, kept inside the work area.
func (n *noteUI) open() bool {
	a := n.host
	a.releaseKeys()
	if n.locked {
		if err := n.load(); err != nil {
			a.report(err)
		}
	}
	if n.win == nil {
		n.prevPID = 0
		if pid := frontmostPID(); pid > 0 && pid != ownPID() {
			n.prevPID = pid
		}
		frame := n.frame
		if frame.Width == 0 || frame.Height == 0 {
			frame = a.freshNoteFrame()
		}
		w := noteNewWindow(mygo.WindowOptions{
			Title: a.noteWindowTitle(), Width: frame.Width, Height: frame.Height,
			X: frame.X, Y: frame.Y,
			Hidden: true, BackgroundColor: noteBg, AlwaysOnTop: n.top,
			TitleBarStyle:     mygo.TitleBarHidden,
			HideWindowButtons: true,
			MinWidth:          noteMinW, MinHeight: noteMinH,
			Content: ui.View(a.noteView),
		})
		w.OnClose(n.onClose)
		w.OnBlur(n.onBlur)
		w.OnResize(func() { n.frame = w.Bounds() })
		w.OnMove(func() { n.frame = w.Bounds() })
		n.win = w
		n.booted = false
	}
	n.frame = n.win.Bounds()
	noteShow(n.win)
	noteActivate()
	noteFocus(n.win)
	n.invalidate()
	return true
}

// showFront shows the note's window and gives it the keyboard; a closed
// note opens first. The home search's 「打开」 uses it, so an open note is
// raised and focused, never toggled away the way the card's own tap does.
func (n *noteUI) showFront() bool {
	if n.win == nil {
		return n.open()
	}
	noteShow(n.win)
	noteActivate()
	noteFocus(n.win)
	n.invalidate()
	return true
}

// close lands what was typed and shuts the window: the close button, Esc
// and a second tap on the card all come here.
func (n *noteUI) close() {
	if err := n.flush(); err != nil {
		n.host.report(err)
	}
	if n.win != nil {
		noteClose(n.win)
	}
}

// onClose is the window going away: flush, forget the window, and give
// the keyboard back to whatever had it before — never hiding this app.
func (n *noteUI) onClose(*mygo.CloseEvent) {
	a := n.host
	n.flush()
	n.win = nil
	if a.quitting {
		return
	}
	prev := n.prevPID
	n.prevPID = 0
	switch a.settingsKeyboardExit() {
	case settingsToPanel:
		// Clipboard search: the panel's search field keeps the keyboard,
		// so the app the user came from must not take it back.
		if a.win != nil {
			a.win.Focus()
		}
		return
	case settingsToShelf:
		a.holdKeys()
	}
	a.handBackApp(prev)
}

// onBlur lands the draft when the window gives up the keyboard: clicking
// the panel or another app is never a reason to lose a sentence.
func (n *noteUI) onBlur() { n.flush() }

// freshNoteFrame is where a note window without a remembered place opens:
// under the pinned note card when it is on the desktop, else under the
// notch panel, centred on it and kept inside the work area.
func (a *shelf) freshNoteFrame() mygo.Rectangle {
	anchor := a.frame
	for _, it := range a.pins {
		if it.cardID == "note" && it.win != nil {
			anchor = it.frame
			break
		}
	}
	area := mygo.Screen.DisplayNearestPoint(mygo.Point{
		X: anchor.X + anchor.Width/2,
		Y: anchor.Y + anchor.Height/2,
	}).WorkArea
	if anchor.Width == 0 || anchor.Height == 0 {
		return centeredFrame(noteW, noteH, area)
	}
	return noteFrameUnder(anchor, area)
}

// noteFrameUnder is where a note window opens for an anchor and a work
// area: centred under the anchor, above it when there is no room below,
// and kept inside the area. Pure; unit-tested.
func noteFrameUnder(anchor, area mygo.Rectangle) mygo.Rectangle {
	const gap = 8
	x := anchor.X + (anchor.Width-noteW)/2
	y := anchor.Y + anchor.Height + gap
	if y+noteH > area.Y+area.Height {
		y = anchor.Y - noteH - gap
	}
	x = max(area.X, min(x, area.X+max(0, area.Width-noteW)))
	y = max(area.Y, min(y, area.Y+max(0, area.Height-noteH)))
	return mygo.Rectangle{X: x, Y: y, Width: noteW, Height: noteH}
}

// noteWindowTitle is the window's title: the note's heading, or 便利贴.
func (a *shelf) noteWindowTitle() string {
	if n := a.note; n != nil {
		if title := note.Title(n.text); title != "" {
			return title
		}
	}
	return a.appLoc().CardNote()
}

// noteView is the window: the 30pt bar with the heading and the three
// buttons, and the note's Markdown under it.
func (a *shelf) noteView(c *ui.Context) {
	n := a.note
	if n == nil {
		return
	}
	l := loc(c)
	c.Root().Background(surfaceRaise)
	ui.Column(c.Key("note")).Fill().Children(func() {
		a.noteBar(c, l)
		a.noteArea(c, l)
	})
}

// noteBar is the window's top row: the heading, then 在访达中显示, 置顶 and
// 关闭. The bar drags the window; the name lives in each button's tip and
// accessible label only. The heading follows the editor, so it changes as
// the first line is typed.
func (a *shelf) noteBar(c *ui.Context, l locales.Messages) {
	n := a.note
	title := note.Title(n.draft)
	if title == "" {
		title = l.CardNote()
	}
	ui.Row(c.Key("note-bar")).Height(noteBarH).Shrink(0).FillWidth().
		AlignItems(ui.Center).Padding(0, 8, 0, 12).Gap(2).DragWindow().Children(func() {
		ui.Text(c.Key("note-title"), title).SingleLine().FontSize(11.5).FontWeight(600).
			TextColor(ink2).Grow(1).MinWidth(0)
		a.noteButton(c, "note-reveal", finderSymbol, l.NoteReveal(), false, func() {
			if n.win != nil {
				n.frame = n.win.Bounds()
			}
			noteReveal(n.store.Path())
		})
		a.noteButton(c, "note-top", pinSymbol, l.NoteKeepOnTop(), n.top, func() {
			n.setTop(!n.top)
		})
		a.noteButton(c, "note-close", closeSymbol, l.NoteClose(), false, n.close)
	})
}

// setTop turns 置顶 on or off. The state lives on the note because the
// window is rebuilt on every reopen: a rebuilt one is told again (open), and
// the bar keeps drawing the flag from here.
func (n *noteUI) setTop(on bool) {
	n.top = on
	if n.win != nil {
		n.win.SetAlwaysOnTop(on)
	}
}

// noteButton is one 22pt bar button: an accent tint while it is on (置顶),
// a quiet fill under the pointer, and the label in the tip only.
func (a *shelf) noteButton(c *ui.Context, key string, icon *ui.SVG, label string, on bool, act func()) {
	b := ui.Row(c.Key(key)).Size(22, 22).Radius(6).Center().Label(label).Tooltip(label)
	switch {
	case on:
		b.Background(accent24)
	case b.Hovered():
		b.Background(fill2)
	}
	b.Children(func() {
		ui.Icon(c, icon).Size(13, 13).TextColor(ink2)
	})
	if b.Clicked() && act != nil {
		act()
	}
}

// noteArea is the note itself: Markdown source at 13pt, 1.55 line
// height, the caret in it on the window's first draw. Esc closes.
func (a *shelf) noteArea(c *ui.Context, l locales.Messages) {
	n := a.note
	if n.locked {
		ui.Column(c.Key("note-locked")).Fill().Grow(1).MinHeight(0).Center().Padding(0, 12, 12, 12).
			Children(func() {
				panelLines(c, l.NoteUnreadable(), 11.5, 0).TextAlign(ui.Center).TextColor(ink3)
			})
		return
	}
	area := ui.TextAreaBase(c.Key("note-area"), &n.draft).Fill().Grow(1).MinHeight(0).
		FontSize(typeBody).LineHeight(1.55).TextColor(ink).
		Padding(0, 12, 12, 12).Label(l.CardNote())
	if !n.booted {
		area.Focus()
		n.booted = true
	}
	area.OnTextKey(func(k ui.TextKey) bool {
		if k.Key == ui.KeyEscape {
			n.close()
			return true
		}
		return false
	})
}

// noteLabel is the card's accessible name: the note's heading, or 便利贴
// while it is blank.
func (a *shelf) noteLabel(l locales.Messages) string {
	n := a.noteSession()
	if n != nil {
		if title := note.Title(n.text); title != "" {
			return title
		}
	}
	return l.CardNote()
}

// noteBody draws the card's content: the 1 × 1 form is the glyph and the
// name; the wide form — 2 × 1 in the grid, every pinned card — is the
// heading and up to three lines of preview.
func (a *shelf) noteBody(c *ui.Context, card ui.Element, span int, surf toolCardSurface) {
	def := toolCardDefFor("note")
	wide := span > 1 || surf == cardDesktop
	n := a.noteSession()
	title, lines := "", []string(nil)
	if n != nil {
		if t := note.Title(n.text); t != "" {
			title = t
		}
		lines = note.Preview(n.text, notePreviewN)
	}
	if !wide {
		card.Center().Gap(5).Children(func() {
			ui.Icon(c, def.icon).Size(20, 20).TextColor(ink)
			fitText(c, def.name(loc(c)), 12, 600, toolColW(span)-8).TextColor(ink)
		})
		return
	}
	// The pin is 278 wide whatever the grid span was; the grid cell is
	// toolColW(span). 12pt padding each side, 14pt glyph, 6pt gap.
	avail := func() float32 {
		if surf == cardDesktop {
			return pinCardW2 - 24 - 20
		}
		return toolColW(span) - 24 - 20
	}()
	card.Padding(0, 12, 0, 12).Justify(ui.Center).Children(func() {
		ui.Column(c.Key("note-lines")).FillWidth().Gap(3).Children(func() {
			ui.Row(c.Key("note-head")).AlignItems(ui.Center).Gap(6).Children(func() {
				ui.Icon(c, def.icon).Size(14, 14).TextColor(ink)
				name := title
				if name == "" {
					name = def.name(loc(c))
				}
				fitText(c, name, 12, 600, avail).TextColor(ink).Label(name)
			})
			for i, line := range lines {
				ui.Text(c.Key("note-line-"+string(rune('a'+i))), line).SingleLine().
					FontSize(11).LineHeight(1.45).TextColor(ink2).Label(line)
			}
		})
	})
}

// nextTick is the pending autosave after typing stops.
func (m noteModule) nextTick(now time.Time) time.Time {
	n := m.host.note
	if n == nil || n.locked || !n.dirty() {
		return time.Time{}
	}
	if n.since.IsZero() {
		return now
	}
	return n.since.Add(noteSaveDelay)
}
