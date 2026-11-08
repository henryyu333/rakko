//go:build darwin

package main

// The 快捷键 page's recorder: a key press becomes an accelerator, saved in
// settings.json and registered at once; a combination the system refuses
// leaves the old one in place; Esc cancels and Delete clears.

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/note"
	"rakko/internal/settings"
	"rakko/locales"
)

// fakeKeys is the real registration layer, replaced: a registry that
// refuses combinations on demand, so a test can hold what the system would
// hold and press what the system would press.
type fakeKeys struct {
	held   map[string]func()
	fail   map[string]int // acc → remaining refusals
	regs   []string
	unregs []string
}

func installFakeKeys(t *testing.T) *fakeKeys {
	t.Helper()
	f := &fakeKeys{held: map[string]func(){}, fail: map[string]int{}}
	origReg, origUnreg := registerKey, unregisterKey
	registerKey = func(acc string, fn func()) error {
		if f.fail[acc] > 0 {
			f.fail[acc]--
			return errShortcutBusy
		}
		if _, ok := f.held[acc]; ok {
			return errShortcutBusy
		}
		f.held[acc] = fn
		f.regs = append(f.regs, acc)
		return nil
	}
	unregisterKey = func(acc string) {
		delete(f.held, acc)
		f.unregs = append(f.unregs, acc)
	}
	t.Cleanup(func() { registerKey, unregisterKey = origReg, origUnreg })
	return f
}

func (f *fakeKeys) has(acc string) bool {
	_, ok := f.held[acc]
	return ok
}

// press delivers a registered combination the way the system would.
func (f *fakeKeys) press(acc string) {
	if fn := f.held[acc]; fn != nil {
		fn()
	}
}

// shortcutShelf is a fresh install with its three default shortcuts
// registered through the fake layer.
func shortcutShelf(t *testing.T) (*shelf, *ui.Tester, *fakeKeys) {
	t.Helper()
	stubSettingsSeams(t, false, nil, nil)
	f := installFakeKeys(t)
	a, view := newSettingsShelf(t)
	a.registerShortcuts()
	return a, view, f
}

func TestShortcutRecordingSavesAndRegisters(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")

	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if a.settingsRecording != shortcutToggle {
		t.Fatalf("the row did not start recording: %v", a.settingsRecording)
	}
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("the recorder did not end after a combination")
	}
	if got := a.settings.Shortcuts.Toggle; got != "Ctrl+Alt+K" {
		t.Fatalf("saved toggle = %q, want Ctrl+Alt+K", got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil || saved.Shortcuts.Toggle != "Ctrl+Alt+K" {
		t.Fatalf("settings.json toggle = %q, %v", saved.Shortcuts.Toggle, err)
	}
	if !f.has("Ctrl+Alt+K") || f.has(settings.DefaultShortcutToggle) {
		t.Fatalf("registry after the change: %v", f.regs)
	}
	// The rows that stood down for the recording came back.
	if !f.has(settings.DefaultShortcutClipboard) || !f.has(settings.DefaultShortcutCapture) {
		t.Fatalf("the other shortcuts were not restored: %v", f.regs)
	}
}

// While a row listens, the app's own combinations must not run their old
// actions: they are released for the duration and claimed again after.
func TestShortcutRecordingSuspendsRegisteredKeys(t *testing.T) {
	a, view, f := shortcutShelf(t)
	a.visible = false
	openSettingsPage(t, view, "快捷键")
	if !f.has("Ctrl+Alt+Space") {
		t.Fatal("the toggle shortcut must start registered")
	}
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("the registered combination still answers while recording")
	}
	// The system would deliver it to the recorder, not to the shelf.
	f.press("Ctrl+Alt+Space")
	if a.visible {
		t.Fatal("the old action ran during the recording")
	}
	// Pressing it reaches the recorder, which keeps the saved value and
	// claims it again for the other rows' sake.
	view.Key(ui.Ctrl|ui.Alt, ui.KeySpace)
	view.Frame()
	if got := a.settings.Shortcuts.Toggle; got != "Ctrl+Alt+Space" {
		t.Fatalf("saved toggle = %q", got)
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("the shortcuts did not come back: %v", f.regs)
	}
}

func TestShortcutRecordingCancelAndClear(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")

	// Esc cancels without touching the saved shortcut, and the suspended
	// combinations come back.
	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("Esc did not end the recording")
	}
	if a.settings.Shortcuts.Clipboard != settings.DefaultShortcutClipboard {
		t.Fatalf("Esc changed the shortcut: %q", a.settings.Shortcuts.Clipboard)
	}
	for _, acc := range []string{"Ctrl+Alt+Space", "Ctrl+Alt+V", "Ctrl+Alt+O"} {
		if !f.has(acc) {
			t.Fatalf("Esc left %q unregistered; registry %v", acc, f.regs)
		}
	}

	// Delete clears it: the file holds "", the system holds nothing for
	// that row, and the rest stay.
	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyBackspace)
	view.Frame()
	if got := a.settings.Shortcuts.Clipboard; got != "" {
		t.Fatalf("cleared clipboard shortcut = %q, want empty", got)
	}
	if f.has(settings.DefaultShortcutClipboard) {
		t.Fatal("clearing left the keys registered")
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("clearing took the other shortcuts down: %v", f.regs)
	}
}

// A key that types text arrives alone or with Shift only: the row asks
// before it takes that key from every app. 仍然使用 saves it; 取消, Esc and a
// click outside keep the previous combination.
func TestShortcutRecordingAsksForTypingKeys(t *testing.T) {
	a, view, f := shortcutShelf(t)
	l := locales.In("")
	openSettingsPage(t, view, "快捷键")
	if err := view.Click(l.SettingsShortcutOCR()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyJ)
	view.Frame()
	if a.shortcutConfirm == nil || a.shortcutConfirm.acc != "J" {
		t.Fatalf("a bare letter did not ask: %+v", a.shortcutConfirm)
	}
	if a.settings.Shortcuts.Capture != settings.DefaultShortcutCapture || f.has("J") {
		t.Fatalf("the question saved or registered the key: %q %v", a.settings.Shortcuts.Capture, f.regs)
	}
	if !view.HasText(l.SettingsShortcutConfirm("J")) || !view.HasText(l.SettingsShortcutUseAnyway()) {
		t.Fatalf("the row does not ask: %v", view.Texts())
	}
	if err := view.Click(l.SettingsShortcutUseAnyway()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if a.settingsRecording != shortcutNone || a.shortcutConfirm != nil {
		t.Fatal("the answer did not end the recording")
	}
	if got := a.settings.Shortcuts.Capture; got != "J" {
		t.Fatalf("saved capture = %q, want J; confirm=%+v recording=%v busy=%v", got, a.shortcutConfirm, a.settingsRecording, a.shortcutBusy)
	}
	if !f.has("J") {
		t.Fatalf("the confirmed key is not registered: %v", f.regs)
	}

	// Shift only counts as bare: ask again, then cancel with the button.
	if err := view.Click(l.SettingsShortcutOCR()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Shift, ui.KeyK)
	view.Frame()
	if a.shortcutConfirm == nil || a.shortcutConfirm.acc != "Shift+K" {
		t.Fatalf("shift with a letter did not ask: %+v", a.shortcutConfirm)
	}
	if err := view.Click(l.SettingsShortcutCancel()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if got := a.settings.Shortcuts.Capture; got != "J" {
		t.Fatalf("cancel changed the shortcut: %q", got)
	}
	if f.has("Shift+K") {
		t.Fatal("cancel registered the key")
	}
	// The recording is over, so a new click starts another question.
	if a.settingsRecording != shortcutNone {
		t.Fatal("cancel left the row recording")
	}

	// Esc drops the question too.
	if err := view.Click(l.SettingsShortcutOCR()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyM)
	view.Frame()
	if a.shortcutConfirm == nil {
		t.Fatal("the letter did not ask")
	}
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if a.shortcutConfirm != nil || a.settingsRecording != shortcutNone || a.settings.Shortcuts.Capture != "J" {
		t.Fatalf("Esc did not drop the question: confirm=%+v recording=%v capture=%q",
			a.shortcutConfirm, a.settingsRecording, a.settings.Shortcuts.Capture)
	}
}

// A click outside the confirm row drops the question, like Esc.
func TestShortcutConfirmClickOutsideCancels(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyJ)
	view.Frame()
	if a.shortcutConfirm == nil {
		t.Fatal("the letter did not ask")
	}
	view.ClickAt(40, settingsHeight-40) // an empty part of the window
	view.Frame()
	if a.shortcutConfirm != nil || a.settingsRecording != shortcutNone {
		t.Fatalf("the click kept the question: %+v %v", a.shortcutConfirm, a.settingsRecording)
	}
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle || f.has("J") {
		t.Fatalf("the click saved the key: %q %v", a.settings.Shortcuts.Toggle, f.regs)
	}
}

// Control, Option or Command saves at once, as before.
func TestShortcutRecordingModifiersSaveAtOnce(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("框选文字"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Shift, ui.KeyJ)
	view.Frame()
	if a.shortcutConfirm != nil {
		t.Fatal("a real modifier asked for confirmation")
	}
	if got := a.settings.Shortcuts.Capture; got != "Ctrl+Shift+J" {
		t.Fatalf("saved capture = %q, want Ctrl+Shift+J", got)
	}
	if !f.has("Ctrl+Shift+J") || !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") {
		t.Fatalf("registry after the change: %v", f.regs)
	}
}

// Keys that type nothing — function keys and the navigation keys — save
// alone or with Shift, without a question.
func TestShortcutRecordingNonTypingKeysSaveAtOnce(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyF5)
	view.Frame()
	if a.shortcutConfirm != nil {
		t.Fatal("F5 asked for confirmation")
	}
	if got := a.settings.Shortcuts.Toggle; got != "F5" || !f.has("F5") {
		t.Fatalf("bare F5 = %q, registry %v", got, f.regs)
	}
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Shift, ui.KeyF5)
	view.Frame()
	if got := a.settings.Shortcuts.Toggle; got != "Shift+F5" || !f.has("Shift+F5") {
		t.Fatalf("shift F5 = %q, registry %v", got, f.regs)
	}
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyLeft)
	view.Frame()
	if got := a.settings.Shortcuts.Toggle; got != "Left" || !f.has("Left") {
		t.Fatalf("bare Left = %q, registry %v", got, f.regs)
	}
}

// The recorder's grammar and the question's rule: a named key always makes
// an accelerator; a text key without Control, Option or Command asks.
func TestShortcutAcceleratorAndConfirmRules(t *testing.T) {
	cases := []struct {
		mods ui.Modifiers
		key  ui.Key
		acc  string
		ask  bool
	}{
		{0, ui.KeyF1, "F1", false},
		{ui.Shift, ui.KeyF5, "Shift+F5", false},
		{0, ui.KeyLeft, "Left", false},
		{0, ui.KeyPageDown, "PageDown", false},
		{0, ui.KeyInsert, "Insert", false},
		{0, ui.KeyA, "A", true},
		{ui.Shift, ui.KeyA, "Shift+A", true},
		{ui.Ctrl, ui.KeyA, "Ctrl+A", false},
		{0, ui.Key0, "0", true},
		{0, ui.KeySpace, "Space", true},
		{ui.Alt, ui.KeySpace, "Alt+Space", false},
		{0, ui.KeyComma, ",", true},
		{0, ui.KeyEnter, "Enter", true},
	}
	for _, tc := range cases {
		acc, ok := shortcutAccelerator(tc.mods, tc.key)
		if !ok || acc != tc.acc {
			t.Errorf("shortcutAccelerator(%v, %v) = %q, %v; want %q", tc.mods, tc.key, acc, ok, tc.acc)
		}
		if got := shortcutNeedsConfirm(tc.mods, tc.key); got != tc.ask {
			t.Errorf("shortcutNeedsConfirm(%v, %v) = %v, want %v", tc.mods, tc.key, got, tc.ask)
		}
	}
	if _, ok := shortcutAccelerator(0, ui.KeyUnknown); ok {
		t.Fatal("an unnamed key made an accelerator")
	}
	// The page shows the new spellings.
	if tokens, ok := shortcutTokens("Shift+F5"); !ok || !slices.Equal(tokens, []string{"⇧", "F5"}) {
		t.Fatalf("Shift+F5 tokens = %v, %v", tokens, ok)
	}
	if got := shortcutLabel("F5"); got != "F5" {
		t.Fatalf("shortcutLabel(F5) = %q", got)
	}
}

// A click outside the keycaps ends the recording, like Esc.
func TestShortcutRecordingClickCancels(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.ClickAt(settingsWidth-80, settingsHeight-60)
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("a click outside the keycaps did not cancel")
	}
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("the click changed the shortcut: %q", a.settings.Shortcuts.Toggle)
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("the click left shortcuts unregistered: %v", f.regs)
	}
}

// Closing the window mid-recording gives the suspended shortcuts back.
func TestSettingsClosedRestoresSuspendedShortcuts(t *testing.T) {
	a, view, f := shortcutShelf(t)
	a.visible = true
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("recording left the combination registered")
	}
	a.settingsClosed()
	if a.settingsRecording != shortcutNone {
		t.Fatal("closing left a row recording")
	}
	for _, acc := range []string{"Ctrl+Alt+Space", "Ctrl+Alt+V", "Ctrl+Alt+O"} {
		if !f.has(acc) {
			t.Fatalf("closing left %q unregistered", acc)
		}
	}
}

// Leaving the window ends the recording: the suspended shortcuts are
// claimed back instead of staying dead while the user works elsewhere.
func TestSettingsRecordingEndsOnBlur(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("recording did not suspend the combination")
	}
	a.settingsBlur()
	view.Frame()
	if a.settingsRecording != shortcutNone {
		t.Fatal("blur left the row recording")
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("blur left shortcuts unregistered: %v", f.regs)
	}
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("blur changed the shortcut: %q", a.settings.Shortcuts.Toggle)
	}
}

// A later successful restore clears the busy note an earlier refusal left:
// the row must not keep saying the combination is taken once it is the
// app's again.
func TestRestoreSuccessClearsBusy(t *testing.T) {
	a, view, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+O"] = 1
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if !a.shortcutBusy[shortcutCapture] {
		t.Fatal("the refused restore was not reported")
	}
	// The next recording restores again, and this time the keys are free.
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(0, ui.KeyEscape)
	view.Frame()
	if !f.has("Ctrl+Alt+O") {
		t.Fatal("the retried restore did not claim the keys")
	}
	if a.shortcutBusy[shortcutCapture] {
		t.Fatal("a successful restore left the row saying 已被占用")
	}
}

// A refused combination keeps the previous shortcut and says so on the row;
// the same combination as another row's counts as refused.
func TestShortcutRecordingBusyKeepsPrevious(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")

	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyV)
	view.Frame()
	if !a.shortcutBusy[shortcutToggle] {
		t.Fatal("a duplicate combination must be reported busy")
	}
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("busy combination replaced the shortcut: %q", a.settings.Shortcuts.Toggle)
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("a refused combination left the registry inconsistent: %v", f.regs)
	}
	view.Frame()
	if !view.HasText(locales.In("").SettingsShortcutBusy()) {
		t.Fatalf("the row does not say the combination is in use: %v", view.Texts())
	}
	// A later successful change clears the note.
	view.Click("显示 / 收起")
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if a.shortcutBusy[shortcutToggle] {
		t.Fatal("the busy note survived a successful change")
	}
	if !f.has("Ctrl+Alt+K") || f.has("Ctrl+Alt+Space") {
		t.Fatalf("registry after the change: %v", f.regs)
	}
}

// A registration the system refuses (another app holds the keys) restores
// the previous registration and saves nothing.
func TestShortcutRecordingSystemRefusal(t *testing.T) {
	a, view, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+P"] = 1
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyP)
	view.Frame()
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("refused combination was saved: %q", a.settings.Shortcuts.Toggle)
	}
	if !a.shortcutBusy[shortcutToggle] {
		t.Fatal("the refusal was not reported on the row")
	}
	if f.has("Ctrl+Alt+P") {
		t.Fatal("the refused combination was registered")
	}
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+V") || !f.has("Ctrl+Alt+O") {
		t.Fatalf("the previous shortcuts were not restored: %v", f.regs)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil || saved.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("settings.json = %q, %v", saved.Shortcuts.Toggle, err)
	}
}

// Claiming the new keys never releases the old registration first: a
// refused change leaves the system exactly as it was.
func TestApplyShortcutClaimsBeforeReleasing(t *testing.T) {
	a, _, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+X"] = 1
	if err := a.applyShortcut(shortcutToggle, "Ctrl+Alt+X"); err == nil {
		t.Fatal("a refused combination was accepted")
	}
	if !f.has("Ctrl+Alt+Space") {
		t.Fatalf("the old shortcut is gone: %v", f.unregs)
	}
	if len(f.unregs) != 0 {
		t.Fatalf("the old registration was released before the new one was claimed: %v", f.unregs)
	}
}

// Restoring a suspended shortcut can fail too (another app took it while
// the row listened): the row says so, and the app knows nothing is held.
func TestShortcutRecordingRestoreFailure(t *testing.T) {
	a, view, f := shortcutShelf(t)
	f.fail["Ctrl+Alt+O"] = 1
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if !a.shortcutBusy[shortcutCapture] {
		t.Fatal("a failed restore was not reported on its row")
	}
	if f.has("Ctrl+Alt+O") {
		t.Fatal("the refused restore reported success")
	}
	if !f.has("Ctrl+Alt+K") || !f.has("Ctrl+Alt+V") {
		t.Fatalf("the rest of the registry: %v", f.regs)
	}
}

// A combination settings.json names but the system refused at launch is
// retried when the user records it again; "already saved" is not "held".
func TestShortcutRecordingRetriesRefusedAtLaunch(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	f := installFakeKeys(t)
	a, view := newSettingsShelf(t)
	f.fail["Ctrl+Alt+V"] = 1
	a.registerShortcuts()
	if !a.shortcutBusy[shortcutClipboard] || f.has("Ctrl+Alt+V") {
		t.Fatalf("the refused launch registration did not stick: busy=%v", a.shortcutBusy)
	}
	openSettingsPage(t, view, "快捷键")
	if err := view.Click("剪贴板搜索"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyV)
	view.Frame()
	if !f.has("Ctrl+Alt+V") {
		t.Fatal("recording the same combination did not retry it")
	}
	if a.shortcutBusy[shortcutClipboard] {
		t.Fatal("the busy note survived the successful retry")
	}
	if a.settings.Shortcuts.Clipboard != settings.DefaultShortcutClipboard {
		t.Fatalf("the retry changed the saved value: %q", a.settings.Shortcuts.Clipboard)
	}
}

// A save that fails releases only the keys it claimed and leaves the file's
// shortcut registered, so the three places still agree afterwards.
func TestShortcutSaveFailureReleasesOnlyTheNewKeys(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	a.dataDir = filepath.Join(t.TempDir(), "gone") // settings.json cannot be written
	f.fail["Ctrl+Alt+Space"] = 1                   // and the old keys are taken meanwhile
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeyK)
	view.Frame()
	if a.settings.Shortcuts.Toggle != settings.DefaultShortcutToggle {
		t.Fatalf("the unsaved combination was kept in memory: %q", a.settings.Shortcuts.Toggle)
	}
	if f.has("Ctrl+Alt+K") {
		t.Fatal("the unsaved combination stayed registered")
	}
	if f.has("Ctrl+Alt+Space") {
		t.Fatal("a restore the system refused was reported as held")
	}
	if !a.shortcutBusy[shortcutToggle] {
		t.Fatal("the failed save was not reported on the row")
	}
	// With the file writable again, recording the same saved value claims
	// it: a value the system does not hold is never reported as success
	// without being registered.
	a.dataDir = t.TempDir()
	os.WriteFile(filepath.Join(a.dataDir, settings.FileName), []byte(`{}`), 0o600)
	a.settings.Shortcuts.Toggle = settings.DefaultShortcutToggle
	if err := view.Click("显示 / 收起"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Key(ui.Ctrl|ui.Alt, ui.KeySpace)
	view.Frame()
	if !f.has("Ctrl+Alt+Space") || a.shortcutBusy[shortcutToggle] {
		t.Fatalf("the retry did not claim the combination: %v busy=%v", f.regs, a.shortcutBusy)
	}
}

// Launch registers what settings.json holds, including a cleared shortcut,
// and keeps a refused one on its row instead of stopping.
func TestRegisterShortcutsAtLaunch(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	f := installFakeKeys(t)
	f.fail["Ctrl+Alt+O"] = 1

	a := &shelf{settings: settings.Default()}
	a.settings.Shortcuts.Clipboard = ""
	a.settings.Shortcuts.Tray = "Ctrl+Alt+T"
	a.registerShortcuts()
	if !f.has("Ctrl+Alt+Space") || !f.has("Ctrl+Alt+T") || f.has("Ctrl+Alt+O") {
		t.Fatalf("registry %v", f.regs)
	}
	if !a.shortcutBusy[shortcutCapture] || a.shortcutBusy[shortcutClipboard] {
		t.Fatalf("busy flags = %v", a.shortcutBusy)
	}
}

// 「打开托盘」 puts the panel on the 托盘 page: it opens a closed shelf, grows
// the overview, and switches a panel that sits on another page.
func TestOpenTrayShortcutShowsTheTrayPage(t *testing.T) {
	a, _ := levelShelf(t)
	a.visible = false
	openTray := a.shortcutAction(shortcutOpenTray)
	openTray()
	if !a.visible || a.currentLevel() != 2 || a.currentPage().id() != pageTray {
		t.Fatalf("open from closed: visible=%v level=%d page=%v",
			a.visible, a.currentLevel(), a.currentPage().id())
	}
	a.setTab(pageClipboard)
	if a.currentPage().id() != pageClipboard {
		t.Fatalf("the test did not switch pages: %v", a.currentPage().id())
	}
	openTray()
	if a.currentPage().id() != pageTray {
		t.Fatalf("open on 托盘 left the panel on %v", a.currentPage().id())
	}
	// The action the shortcut kind runs is this one.
	openTray()
	if a.currentPage().id() != pageTray {
		t.Fatalf("the shortcut left the panel on %v", a.currentPage().id())
	}
}

// Relative update dates are calendar-day categories, checked against an
// explicit clock rather than whichever date the native suite finishes on.
func TestUpdateWhenClassifiesCalendarDays(t *testing.T) {
	l := locales.In("zh-Hans")
	a := &shelf{}
	now := time.Date(2026, 10, 29, 15, 30, 0, 0, time.Local)
	today := time.Date(2026, 10, 29, 9, 12, 0, 0, time.Local)
	if got := a.updateWhen(l, today, now); got != l.SettingsUpdateToday("09:12") {
		t.Fatalf("today = %q", got)
	}
	if got := a.updateWhen(l, time.Date(2026, 10, 28, 21, 5, 0, 0, time.Local), now); got != l.SettingsUpdateYesterday("21:05") {
		t.Fatalf("yesterday = %q", got)
	}
	if got := a.updateWhen(l, time.Date(2026, 10, 20, 8, 0, 0, 0, time.Local), now); got != l.SettingsUpdateOn("2026-10-20 08:00") {
		t.Fatalf("older = %q", got)
	}
}

// 现在检查 runs the same check the menu runs, and the button is locked
// while one runs.
func TestSettingsCheckNowRow(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	// A build that can check: production, with an enabled updater and a
	// stubbed check. The window factory returns nil, so the manual check's
	// progress face runs as a headless mark instead of a real window.
	origCheck, origEnabled, origDev := updateCheck, updateEnabled, updateDevBuild
	origMake := updateWinMake
	origWork, origMain := goWork, dispatchMain
	var calls int
	updateCheck = func(ctx context.Context) (*mygo.Update, error) { calls++; return nil, nil }
	updateEnabled = func() bool { return true }
	updateDevBuild = func() bool { return false }
	updateWinMake = func(*shelf) *mygo.Window { return nil }
	// The check runs on this goroutine: the click returns only after the
	// stubbed check and its main-thread dispatch have both run, so the
	// count is settled and nothing races.
	goWork = func(fn func()) { fn() }
	dispatchMain = func(fn func()) { fn() }
	t.Cleanup(func() {
		updateCheck, updateEnabled, updateDevBuild = origCheck, origEnabled, origDev
		updateWinMake = origMake
		goWork, dispatchMain = origWork, origMain
	})

	_, view := newSettingsShelf(t)
	if err := view.Click("现在检查"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if calls != 1 {
		t.Fatalf("现在检查 did not start a check: calls=%d", calls)
	}
}

// Every new global shortcut ships unset: a fresh install registers only the
// three combinations it always had.
func TestShortcutDefaultsForNewKindsAreUnset(t *testing.T) {
	def := settings.Default().Shortcuts
	for name, acc := range map[string]string{
		"playPause": def.PlayPause, "previous": def.Previous, "next": def.Next,
		"tools": def.Tools, "usage": def.Usage, "awake": def.Awake,
		"note": def.Note, "pet": def.Pet,
	} {
		if acc != "" {
			t.Fatalf("%s defaults to %q, want unset", name, acc)
		}
	}
}

// Launch registers every new combination settings.json holds, the
// recording flow releases them, and restore claims them back.
func TestRegisterShortcutsCoversNewKinds(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	f := installFakeKeys(t)
	a := &shelf{settings: settings.Default()}
	accs := map[shortcutKind]string{
		shortcutPlayPause: "Ctrl+Alt+P", shortcutPrevious: "Ctrl+Alt+Left",
		shortcutNext: "Ctrl+Alt+Right", shortcutTools: "Ctrl+Alt+G",
		shortcutUsage: "Ctrl+Alt+U", shortcutAwake: "Ctrl+Alt+W",
		shortcutNote: "Ctrl+Alt+N", shortcutPet: "Ctrl+Alt+E",
		shortcutAppSwitch: "Ctrl+Alt+J",
	}
	for k, acc := range accs {
		*k.field(&a.settings.Shortcuts) = acc
	}
	a.registerShortcuts()
	for k, acc := range accs {
		if !f.has(acc) || a.shortcutHeld(k) != acc {
			t.Fatalf("%v: registered=%v held=%q, want %q", k, f.has(acc), a.shortcutHeld(k), acc)
		}
	}
	for _, acc := range []string{"Ctrl+Alt+Space", "Ctrl+Alt+V", "Ctrl+Alt+O"} {
		if !f.has(acc) {
			t.Fatalf("the launch registration dropped %q: %v", acc, f.regs)
		}
	}
	a.suspendShortcuts()
	for k, acc := range accs {
		if f.has(acc) || a.shortcutHeld(k) != "" {
			t.Fatalf("%v stayed registered while recording", k)
		}
	}
	a.restoreShortcuts()
	for k, acc := range accs {
		if !f.has(acc) || a.shortcutHeld(k) != acc {
			t.Fatalf("%v was not restored: %v", k, f.regs)
		}
	}
}

// A recorded combination on one of the new rows lands in settings.json,
// registers at once and shows on the row.
func TestRecordingANewShortcutSavesAndRegisters(t *testing.T) {
	a, view, f := shortcutShelf(t)
	openSettingsPage(t, view, "快捷键")
	if err := view.Click(locales.In("").SettingsShortcutPlayPause()); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if a.settingsRecording != shortcutPlayPause {
		t.Fatalf("the row did not start recording: %v", a.settingsRecording)
	}
	view.Key(ui.Ctrl|ui.Alt, ui.KeyP)
	view.Frame()
	if got := a.settings.Shortcuts.PlayPause; got != "Ctrl+Alt+P" {
		t.Fatalf("saved play/pause = %q", got)
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil || saved.Shortcuts.PlayPause != "Ctrl+Alt+P" {
		t.Fatalf("settings.json playPause = %q, %v", saved.Shortcuts.PlayPause, err)
	}
	if !f.has("Ctrl+Alt+P") {
		t.Fatalf("the recorded combination is not registered: %v", f.regs)
	}
}

// The three transport shortcuts reach the player with their own command,
// through the same channel the home player's buttons use.
func TestPlayerShortcutsSendTransportCommands(t *testing.T) {
	orig := playerShortcut
	var got []mac.NowPlayingCommand
	playerShortcut = func(_ *shelf, cmd mac.NowPlayingCommand) { got = append(got, cmd) }
	t.Cleanup(func() { playerShortcut = orig })

	f := installFakeKeys(t)
	a := &shelf{settings: settings.Default()}
	a.settings.Shortcuts.PlayPause, a.settings.Shortcuts.Previous, a.settings.Shortcuts.Next =
		"Ctrl+Alt+P", "Ctrl+Alt+Left", "Ctrl+Alt+Right"
	a.registerShortcuts()
	for _, acc := range []string{"Ctrl+Alt+P", "Ctrl+Alt+Left", "Ctrl+Alt+Right"} {
		f.press(acc)
	}
	if !slices.Equal(got, []mac.NowPlayingCommand{mac.NPPlayPause, mac.NPPrevious, mac.NPNext}) {
		t.Fatalf("commands = %v", got)
	}
}

// 打开工具页 opens the panel on the tools page; 打开 AI 用量 opens its page
// while the feature is on, and the Settings page that turns it on while off.
func TestPageShortcutsOpenTheirPages(t *testing.T) {
	f := installFakeKeys(t)
	a, _ := levelShelf(t)
	a.settings.Shortcuts.Tools = "Ctrl+Alt+G"
	a.registerShortcuts()
	a.visible = false
	f.press("Ctrl+Alt+G")
	if !a.visible || a.currentLevel() != 2 || a.currentPage().id() != pageTools {
		t.Fatalf("tools shortcut: visible=%v level=%d page=%v",
			a.visible, a.currentLevel(), a.currentPage().id())
	}

	// A fresh registry: the fixture shelf's own launch registration must
	// not share the previous shelf's combinations.
	usage := installFakeKeys(t)
	fx := newUsageFixture(t, true)
	fx.a.settings.Shortcuts.Usage = "Ctrl+Alt+U"
	fx.a.registerShortcuts()
	fx.a.visible, fx.a.level, fx.a.tab = true, 2, pageTray
	usage.press("Ctrl+Alt+U")
	if got := fx.a.currentPage().id(); got != pageUsage {
		t.Fatalf("usage shortcut landed on %v", got)
	}

	// Off: the page is not on the ear bar, so the shortcut opens the
	// Settings page that turns it on instead of landing on the tray.
	offReg := installFakeKeys(t)
	origOpen := settingsOpen
	opened := false
	settingsOpen = func(*shelf) { opened = true }
	t.Cleanup(func() { settingsOpen = origOpen })
	off, _ := newSettingsShelf(t)
	off.settings.Shortcuts.Usage = "Ctrl+Alt+U"
	off.registerShortcuts()
	offReg.press("Ctrl+Alt+U")
	if !opened || off.settingsPage != settingsPageAIUsage {
		t.Fatalf("disabled usage: opened=%v page=%v", opened, off.settingsPage)
	}
}

// 保持唤醒开 / 关 holds the same power assertion the card's tap does.
func TestAwakeShortcutTogglesTheHold(t *testing.T) {
	f := installFakeKeys(t)
	power := newFakePower()
	a := &shelf{settings: settings.Default()}
	a.awake = newAwakeStub(power)
	a.awake.host = a
	a.settings.Shortcuts.Awake = "Ctrl+Alt+W"
	a.registerShortcuts()
	f.press("Ctrl+Alt+W")
	if !a.awake.active() || power.held() != 1 {
		t.Fatalf("the shortcut did not start the hold: active=%v held=%d", a.awake.active(), power.held())
	}
	f.press("Ctrl+Alt+W")
	if a.awake.active() || power.held() != 0 {
		t.Fatalf("a second press did not stop the hold: active=%v held=%d", a.awake.active(), power.held())
	}
}

// 新建便利贴 writes a fresh empty note beside the current one and brings
// it to the window; the note it replaces keeps its text.
func TestNewNoteShortcutStartsANote(t *testing.T) {
	f := installFakeKeys(t)
	a := &shelf{dataDir: t.TempDir(), settings: settings.Default()}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	oldID := n.store.ID()
	if err := n.store.Write("旧笔记\n"); err != nil {
		t.Fatal(err)
	}
	if err := n.load(); err != nil {
		t.Fatal(err)
	}
	origFront := openNoteFront
	opened := false
	openNoteFront = func(*noteUI) bool { opened = true; return true }
	t.Cleanup(func() { openNoteFront = origFront })

	a.settings.Shortcuts.Note = "Ctrl+Alt+N"
	a.registerShortcuts()
	f.press("Ctrl+Alt+N")
	if !opened {
		t.Fatal("the shortcut did not bring the new note up")
	}
	if a.note.store.ID() == oldID {
		t.Fatal("the shortcut kept editing the old note")
	}
	if got, _ := os.ReadFile(filepath.Join(a.dataDir, "notes", oldID+".md")); string(got) != "旧笔记\n" {
		t.Fatalf("the old note = %q", got)
	}
	docs, err := note.Docs(a.dataDir)
	if err != nil || len(docs) != 2 {
		t.Fatalf("notes after the shortcut = %d, %v", len(docs), err)
	}
	if a.note.text != "" || a.note.draft != "" {
		t.Fatalf("the new note is not empty: %q", a.note.text)
	}
}

// 放出 / 收回海獭 toggles the same switch the tray menu carries.
func TestPetShortcutShowsAndHidesTheOtter(t *testing.T) {
	f := installFakeKeys(t)
	a := petShelf(t)
	a.settings.Pet.Shown = false
	a.settings.Shortcuts.Pet = "Ctrl+Alt+E"
	a.registerShortcuts()
	f.press("Ctrl+Alt+E")
	if !a.settings.Pet.Shown || a.pet == nil {
		t.Fatalf("the shortcut did not show the otter: shown=%v pet=%v", a.settings.Pet.Shown, a.pet)
	}
	f.press("Ctrl+Alt+E")
	if a.settings.Pet.Shown || a.pet != nil {
		t.Fatalf("a second press did not hide it: shown=%v pet=%v", a.settings.Pet.Shown, a.pet)
	}
}

// The page groups the rows by what they act on and shows every new row
// unset on a fresh install.
func TestShortcutPageGroupsTheRows(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	_, view := newSettingsShelf(t)
	openSettingsPage(t, view, "快捷键")
	l := locales.In("")
	if !view.HasText(l.SettingsKeysGlobalGroup()) {
		t.Fatalf("the page lacks %q; texts %v", l.SettingsKeysGlobalGroup(), view.Texts())
	}
	for _, want := range []string{
		l.SettingsShortcutPlaybackGroup(), l.SettingsShortcutToolsGroup(),
		l.SettingsShortcutPlayPause(), l.SettingsShortcutPrevious(), l.SettingsShortcutNext(),
		l.SettingsShortcutTools(), l.SettingsShortcutUsage(), l.SettingsShortcutAwake(),
		l.SettingsShortcutNewNote(), l.SettingsShortcutPet(),
	} {
		if !view.HasText(want) {
			t.Fatalf("the page lacks %q; texts %v", want, view.Texts())
		}
	}
	if !view.HasText(l.SettingsShortcutNoShortcut()) {
		t.Fatalf("an unset row does not say %q; texts %v", l.SettingsShortcutNoShortcut(), view.Texts())
	}
}

// A page shortcut is a switch: the second press on the page the panel
// already shows folds the panel away; any other state opens or switches.
func TestPageShortcutsToggleTheirPage(t *testing.T) {
	cases := []struct {
		name  string
		kind  shortcutKind
		page  pageID
		build func(t *testing.T) *shelf
	}{
		{"tray", shortcutOpenTray, pageTray, func(t *testing.T) *shelf {
			a, _ := levelShelf(t)
			return a
		}},
		{"tools", shortcutTools, pageTools, func(t *testing.T) *shelf {
			a, _ := levelShelf(t)
			return a
		}},
		{"usage", shortcutUsage, pageUsage, func(t *testing.T) *shelf {
			fx := newUsageFixture(t, true)
			return fx.a
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.build(t)
			a.visible, a.level = false, 0
			run := a.shortcutAction(tc.kind)

			run()
			if !a.visible || a.currentLevel() != 2 || a.currentPage().id() != tc.page {
				t.Fatalf("open from closed: visible=%v level=%d page=%v", a.visible, a.currentLevel(), a.currentPage().id())
			}
			run()
			if a.visible {
				t.Fatalf("the second press did not fold the panel on %v", tc.page)
			}

			// From another page it switches rather than folds.
			run()
			other := pageTray
			if tc.page == pageTray {
				other = pageClipboard
			}
			a.setTab(other)
			if a.currentPage().id() != other {
				t.Fatalf("the test did not switch pages: %v", a.currentPage().id())
			}
			run()
			if !a.visible || a.currentPage().id() != tc.page {
				t.Fatalf("from %v: visible=%v page=%v", other, a.visible, a.currentPage().id())
			}
		})
	}
}

// The clipboard search has no window in a headless shelf, so its toggle is
// driven from the states it changes: the search bar opens it, the page
// folds it with or without a query, and another page switches to it.
func TestClipboardShortcutTogglesItsPage(t *testing.T) {
	a, _ := levelShelf(t)
	a.visible, a.level, a.tab = true, 1, pageClipboard
	run := a.shortcutAction(shortcutClipboard)
	run()
	if !a.visible || a.currentLevel() != 2 || a.currentPage().id() != pageClipboard {
		t.Fatalf("the search bar did not open the clipboard page: visible=%v level=%d page=%v",
			a.visible, a.currentLevel(), a.currentPage().id())
	}
	a.search = "报告"
	run()
	if a.visible {
		t.Fatal("a second press on the clipboard page did not fold while searching")
	}
	a.visible, a.level, a.tab = true, 2, pageTray
	run()
	if !a.visible || a.currentPage().id() != pageClipboard {
		t.Fatalf("from the tray: visible=%v page=%v", a.visible, a.currentPage().id())
	}
}

// While AI 用量 is off the page is not in the panel: both presses open the
// Settings page that turns it on.
func TestUsageShortcutStaysOnSettingsWhileDisabled(t *testing.T) {
	origOpen := settingsOpen
	opened := 0
	settingsOpen = func(*shelf) { opened++ }
	t.Cleanup(func() { settingsOpen = origOpen })
	a, _ := newSettingsShelf(t)
	a.visible, a.level, a.tab = true, 2, pageTray
	run := a.shortcutAction(shortcutUsage)
	run()
	run()
	if opened != 2 || a.settingsPage != settingsPageAIUsage {
		t.Fatalf("disabled usage: opened=%d page=%v", opened, a.settingsPage)
	}
}
