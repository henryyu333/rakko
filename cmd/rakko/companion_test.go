//go:build darwin

package main

// The support line in both places: the home page's milestone week and the
// 关于 page's sentence and button. Rendering is headless; the page's clock,
// the data folder and the bookmark are the real ones, only the start date
// is chosen by the test.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/companion"
	"rakko/internal/mac"
)

// companionFor seeds a data directory with a start that makes today day.
func companionFor(t *testing.T, dir string, day int, clicked bool) *companion.Store {
	t.Helper()
	s, err := companion.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(time.Now().AddDate(0, 0, -(day - 1))); err != nil {
		t.Fatal(err)
	}
	if clicked {
		if err := s.MarkClicked(); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// homeWithCompanion is the idle home page on one chosen day.
func homeWithCompanion(t *testing.T, day int, clicked bool) (*shelf, *ui.Tester) {
	t.Helper()
	a, view, _ := homeShelf(t, nil)
	a.companion = companionFor(t, a.dataDir, day, clicked)
	view.Frame()
	view.Frame()
	return a, view
}

// TestHomeCompanionWindows checks the seven-day window on both sides of
// every milestone: the line appears on the milestone and its six
// following days and nowhere else.
func TestHomeCompanionWindows(t *testing.T) {
	cases := []struct {
		day       int
		milestone int // 0 when hidden
	}{
		{1, 0}, {29, 0}, {30, 30}, {36, 30}, {37, 0},
		{100, 100}, {106, 100}, {107, 0},
		{365, 365}, {371, 365}, {372, 0},
		{730, 730}, {736, 730}, {737, 0},
	}
	for _, c := range cases {
		_, view := homeWithCompanion(t, c.day, false)
		texts := strings.Join(view.Texts(), " | ")
		if c.milestone == 0 {
			if strings.Contains(texts, "已陪伴你") {
				t.Errorf("day %d shows a support line: %s", c.day, texts)
			}
			continue
		}
		days := fmt.Sprintf("Rakko 已陪伴你 %d 天", c.day)
		if !view.HasText(days) || !view.HasText("请我喝杯咖啡") {
			t.Errorf("day %d lacks %q and the ask; texts %s", c.day, days, texts)
		}
	}
}

// TestHomeCompanionThanksAfterClick says one click ends the asking: the
// line stays for the window but only says thank you.
func TestHomeCompanionThanksAfterClick(t *testing.T) {
	_, view := homeWithCompanion(t, 100, true)
	if !view.HasText("Rakko 已陪伴你 100 天") || !view.HasText("感谢你的支持") {
		t.Fatalf("clicked line lacks the days and thanks; texts %v", view.Texts())
	}
	if view.HasText("请我喝杯咖啡") {
		t.Fatalf("clicked line still asks; texts %v", view.Texts())
	}
}

// TestHomeCompanionClickOpensAndRemembers clicks the line, then reopens the
// file as a later launch would.
func TestHomeCompanionClickOpensAndRemembers(t *testing.T) {
	var opened []string
	oldExternal := openExternal
	openExternal = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	t.Cleanup(func() { openExternal = oldExternal })

	a, view := homeWithCompanion(t, 100, false)
	label := "Rakko 已陪伴你 100 天 · 请我喝杯咖啡"
	if err := view.Click(label); err != nil {
		t.Fatalf("click %q: %v", label, err)
	}
	if len(opened) != 1 || opened[0] != supportURL {
		t.Fatalf("opened %v, want %q", opened, supportURL)
	}
	if !a.companion.Clicked() {
		t.Fatal("the click was not remembered in memory")
	}
	again, err := companion.Open(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Clicked() {
		t.Fatal("a later launch would ask again")
	}
	view.Frame()
	view.Frame()
	if view.HasText("请我喝杯咖啡") || !view.HasText("感谢你的支持") {
		t.Fatalf("the line did not switch to thanks; texts %v", view.Texts())
	}
}

// TestHomeCompanionTakesTheLastLine checks the three-line rule: with an
// agent turn, tray files and keep awake already on the page, the support
// line takes keep awake's place.
func TestHomeCompanionTakesTheLastLine(t *testing.T) {
	c := homeShots["zh"]
	a, view, _ := homeShotAgent(t, c)
	// homeShotAgent already has an agent turn and two tray files; add the
	// third resident line, keep awake.
	a.awake = &awakeModule{host: a, id: mac.PowerAssertionID(1), duration: time.Hour,
		until: time.Now().Add(42 * time.Minute), now: time.Now}
	a.companion = companionFor(t, a.dataDir, 30, false)
	view.Frame()
	view.Frame()
	texts := strings.Join(view.Texts(), " | ")
	for _, want := range []string{
		fmt.Sprintf("Codex 正在：%s", c.tasks[0]),
		"托盘里 2 个文件",
		"Rakko 已陪伴你 30 天",
	} {
		if !strings.Contains(texts, want) {
			t.Errorf("the three surviving lines lack %q; texts %s", want, texts)
		}
	}
	if strings.Contains(texts, "保持唤醒") {
		t.Errorf("keep awake was not the line that made room; texts %s", texts)
	}
}

// TestHomeCompanionCorruptShowsNothing says an unreadable companion file is
// silent on the home page rather than guessing a day.
func TestHomeCompanionCorruptShowsNothing(t *testing.T) {
	a, view, _ := homeShelf(t, nil)
	path := filepath.Join(a.dataDir, companion.FileName)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := companion.Open(a.dataDir)
	if !errors.Is(err, companion.ErrCorrupt) {
		t.Fatalf("open error %v, want ErrCorrupt", err)
	}
	a.companion = s
	view.Frame()
	view.Frame()
	for _, text := range view.Texts() {
		if strings.Contains(text, "已陪伴你") {
			t.Fatalf("corrupt file still shows %q", text)
		}
	}
}

// TestSettingsAboutCompanion checks the sentence, its thanks form, and the
// button that opens the same page.
func TestSettingsAboutCompanion(t *testing.T) {
	var opened []string
	oldExternal := openExternal
	openExternal = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	t.Cleanup(func() { openExternal = oldExternal })

	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	a.companion = companionFor(t, a.dataDir, 128, false)
	openSettingsPage(t, view, "关于")
	if !view.HasText("已陪伴你 128 天") {
		t.Fatalf("about lacks the day count; texts %v", view.Texts())
	}
	if !view.HasText("请我喝杯咖啡") {
		t.Fatalf("about lacks the support button; texts %v", view.Texts())
	}
	clickVisible(t, view, "请我喝杯咖啡")
	if len(opened) != 1 || opened[0] != supportURL {
		t.Fatalf("opened %v, want %q", opened, supportURL)
	}
	again, err := companion.Open(a.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Clicked() {
		t.Fatal("the about button did not remember the click")
	}
	view.Frame()
	view.Frame()
	if !view.HasText("已陪伴你 128 天 · 感谢你的支持") {
		t.Fatalf("about did not switch to thanks; texts %v", view.Texts())
	}
	if !view.HasText("请我喝杯咖啡") {
		t.Fatal("the button should stay after the click")
	}
}

// TestSettingsAboutCompanionCorrupt says a broken file hides the sentence
// but keeps the button.
func TestSettingsAboutCompanionCorrupt(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	path := filepath.Join(a.dataDir, companion.FileName)
	if err := os.WriteFile(path, []byte("{}garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, _ := companion.Open(a.dataDir)
	a.companion = s
	openSettingsPage(t, view, "关于")
	for _, text := range view.Texts() {
		if strings.Contains(text, "已陪伴你") {
			t.Fatalf("corrupt file still shows %q", text)
		}
	}
	if !view.HasText("请我喝杯咖啡") {
		t.Fatalf("the button must stay; texts %v", view.Texts())
	}
}

// TestSettingsAboutButtonsFitNarrowWindow keeps the About button row inside
// the panel's content area at the window's minimum width and its default
// width, in both languages. Each settingsButton() reports its own box, so
// the row's last button must end before the page's right padding.
func TestSettingsAboutButtonsFitNarrowWindow(t *testing.T) {
	if !galleryStageOtter(t) {
		t.Skip("no otter art")
	}
	cases := []struct {
		width  int
		lang   string
		page   string
		labels []string
	}{
		{settingsMinWidth, "en", "About", []string{"Check for Updates", "Open Data Folder", "Buy Me a Coffee"}},
		{settingsWidth, "en", "About", []string{"Check for Updates", "Open Data Folder", "Buy Me a Coffee"}},
		{settingsMinWidth, "zh-Hans", "关于", []string{"检查更新", "打开数据文件夹", "请我喝杯咖啡"}},
		{settingsWidth, "zh-Hans", "关于", []string{"检查更新", "打开数据文件夹", "请我喝杯咖啡"}},
	}
	for _, c := range cases {
		stubSettingsSeams(t, false, nil, nil)
		_, view := newSettingsShelf(t)
		view.SetSize(c.width, settingsHeight)
		view.SetLocale(c.lang)
		view.Frame()
		openSettingsPage(t, view, c.page)
		view.Frame()
		contentRight := float32(c.width - 10 - 18) // panel margin 10, page padding 18
		for _, label := range c.labels {
			r, ok := view.Find(label)
			if !ok {
				t.Fatalf("%d/%s: %q is missing; texts %v", c.width, c.lang, label, view.Texts())
			}
			if right := r.X + r.W; right > contentRight {
				t.Errorf("%d/%s: %q ends at %.1f, past the content edge %.1f",
					c.width, c.lang, label, right, contentRight)
			}
		}
	}
}

// TestCompanionStartSource pins the first day of a launch without a
// companion file.
func TestCompanionStartSource(t *testing.T) {
	now := time.Date(2026, 11, 13, 16, 0, 0, 0, time.Local)
	born := time.Date(2024, 5, 1, 3, 0, 0, 0, time.Local)
	if got := companionStart(true, now, func() (time.Time, bool) { return born, true }); !got.Equal(now) {
		t.Fatalf("fresh install starts %v, want today %v", got, now)
	}
	if got := companionStart(false, now, func() (time.Time, bool) { return born, true }); !got.Equal(born) {
		t.Fatalf("existing install starts %v, want the folder's birth %v", got, born)
	}
	if got := companionStart(false, now, func() (time.Time, bool) { return time.Time{}, false }); !got.Equal(now) {
		t.Fatalf("unknown birth starts %v, want today %v", got, now)
	}
	if got := companionStart(false, now, nil); !got.Equal(now) {
		t.Fatalf("no birth probe starts %v, want today %v", got, now)
	}
}

// TestLoadCompanionFirstDay opens a fresh folder, an existing one with a
// known birth date, and a broken file.
func TestLoadCompanionFirstDay(t *testing.T) {
	now := time.Date(2026, 11, 13, 16, 0, 0, 0, time.Local)

	// A fresh install: today, written once, and a second launch keeps it.
	fresh := t.TempDir()
	s, err := loadCompanion(fresh, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if day, ok := s.Day(now); !ok || day != 1 {
		t.Fatalf("fresh install day %d, ok %v; want 1", day, ok)
	}
	later := now.AddDate(0, 0, 29)
	again, err := loadCompanion(fresh, true, later)
	if err != nil {
		t.Fatal(err)
	}
	if day, ok := again.Day(later); !ok || day != 30 {
		t.Fatalf("relaunch day %d, ok %v; want 30", day, ok)
	}

	// An existing install with no file counts from the folder's birth date.
	existing := t.TempDir()
	born := time.Date(2025, 1, 1, 9, 0, 0, 0, time.Local)
	oldBorn := companionBorn
	companionBorn = func(string) (time.Time, bool) { return born, true }
	t.Cleanup(func() { companionBorn = oldBorn })
	s, err = loadCompanion(existing, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if day, ok := s.Day(now); !ok || day != 682 {
		t.Fatalf("existing install day %d, ok %v; want 682", day, ok)
	}

	// A broken file: reported, untouched, no day.
	broken := t.TempDir()
	path := filepath.Join(broken, companion.FileName)
	original := []byte("broken")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = loadCompanion(broken, true, now)
	if !errors.Is(err, companion.ErrCorrupt) {
		t.Fatalf("broken file error %v, want ErrCorrupt", err)
	}
	if _, ok := s.Day(now); ok {
		t.Fatal("a broken file must show no day")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("broken file was rewritten: %q", after)
	}
}

// TestLoadCompanionDamagedFileLeavesItAlone says a file that exists but
// cannot name both its version and a first day is reported and never
// replaced by a guessed date, so a launch cannot rewrite it.
func TestLoadCompanionDamagedFileLeavesItAlone(t *testing.T) {
	now := time.Date(2026, 11, 13, 16, 0, 0, 0, time.Local)
	for _, payload := range []string{`null`, `{}`, `{"version": 1}`, `{"start": "2026-01-01"}`} {
		dir := t.TempDir()
		path := filepath.Join(dir, companion.FileName)
		original := []byte(payload)
		if err := os.WriteFile(path, original, 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := loadCompanion(dir, true, now)
		if !errors.Is(err, companion.ErrCorrupt) {
			t.Errorf("%s: load error %v, want ErrCorrupt", payload, err)
			continue
		}
		if _, ok := s.Day(now); ok {
			t.Errorf("%s: a damaged file must show no day", payload)
		}
		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(original) {
			t.Errorf("%s: file was rewritten to %q", payload, after)
		}
	}
}

// TestDataDirBorn reads the birth time this Mac keeps for a folder.
func TestDataDirBorn(t *testing.T) {
	born, ok := dataDirBorn(t.TempDir())
	if !ok {
		t.Skip("this filesystem keeps no birth time")
	}
	if d := time.Since(born); d < -2*time.Minute || d > 2*time.Minute {
		t.Fatalf("folder birth %v is %v from now", born, d)
	}
}
