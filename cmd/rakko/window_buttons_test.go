package main

import (
	"os"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
	"rakko/locales"
)

func TestNoteWindowOptionsAndRedCloseSaves(t *testing.T) {
	a, n := noteTestShelf(t)
	origNew, origShow, origAct, origFocus := noteNewWindow, noteShow, noteActivate, noteFocus
	var gotOpts *mygo.WindowOptions
	noteNewWindow = func(opts mygo.WindowOptions) *mygo.Window {
		copyOpts := opts
		gotOpts = &copyOpts
		return nil
	}
	noteShow = func(*mygo.Window) {}
	noteActivate = func() {}
	noteFocus = func(*mygo.Window) {}
	t.Cleanup(func() {
		noteNewWindow, noteShow, noteActivate, noteFocus = origNew, origShow, origAct, origFocus
	})

	n.frame = mygo.Rectangle{X: 100, Y: 100, Width: noteW, Height: noteH}
	if !n.open() {
		t.Fatal("n.open() returned false")
	}
	if gotOpts == nil {
		t.Fatal("noteNewWindow was not called")
	}
	if gotOpts.TitleBarStyle != mygo.TitleBarHidden || gotOpts.HideWindowButtons {
		t.Fatalf("unexpected note title bar options: style=%v hide=%v", gotOpts.TitleBarStyle, gotOpts.HideWindowButtons)
	}
	if gotOpts.TrafficLightPosition == nil || gotOpts.TrafficLightPosition.X <= 0 || gotOpts.TrafficLightPosition.Y <= 0 {
		t.Fatalf("expected non-zero TrafficLightPosition, got %+v", gotOpts.TrafficLightPosition)
	}
	if !gotOpts.DisableFullScreen {
		t.Fatal("note window must disable full screen")
	}

	// Simulate window open + unsaved edit + system red close button (OnClose).
	n.win = &mygo.Window{}
	want := "# 红色按钮关窗\n保存这一句\n"
	n.draft = want
	n.onClose(nil)
	if n.win != nil {
		t.Fatal("n.win not cleared by onClose")
	}
	got, err := os.ReadFile(n.store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("onClose saved %q, want %q", string(got), want)
	}
	_ = a
}

func TestBoardWindowOptionsAndRedCloseSaves(t *testing.T) {
	a, b := boardTestShelf(t)
	origNew, origShow, origAct, origFocus := boardNewWindow, boardShow, boardActivate, boardFocus
	var gotOpts *mygo.WindowOptions
	boardNewWindow = func(opts mygo.WindowOptions) *mygo.Window {
		copyOpts := opts
		gotOpts = &copyOpts
		return nil
	}
	boardShow = func(*mygo.Window) {}
	boardActivate = func() {}
	boardFocus = func(*mygo.Window) {}
	t.Cleanup(func() {
		boardNewWindow, boardShow, boardActivate, boardFocus = origNew, origShow, origAct, origFocus
	})

	b.frame = mygo.Rectangle{X: 100, Y: 100, Width: boardW, Height: boardH}
	if !b.open() {
		t.Fatal("b.open() returned false")
	}
	if gotOpts == nil {
		t.Fatal("boardNewWindow was not called")
	}
	if gotOpts.TitleBarStyle != mygo.TitleBarHidden || gotOpts.HideWindowButtons {
		t.Fatalf("unexpected board title bar options: style=%v hide=%v", gotOpts.TitleBarStyle, gotOpts.HideWindowButtons)
	}
	if gotOpts.TrafficLightPosition == nil || gotOpts.TrafficLightPosition.X <= 0 || gotOpts.TrafficLightPosition.Y <= 0 {
		t.Fatalf("expected non-zero TrafficLightPosition, got %+v", gotOpts.TrafficLightPosition)
	}
	if gotOpts.DisableFullScreen {
		t.Fatal("board window must allow full screen")
	}

	// Simulate window open + active inline text edit + pending board rename + system red close (OnClose).
	id := b.ed.CreateText(geo.V(120, 120), "初始")
	b.ed.StartEditing(id)
	b.ed.EditText = "红按钮保存"
	b.ed.EditChanged()
	b.renaming = true
	b.renameDraft = "验收白板"

	b.win = &mygo.Window{}
	b.onClose(nil)
	if b.win != nil {
		t.Fatal("b.win not cleared by onClose")
	}
	if len(b.bitmaps) != 0 {
		t.Fatalf("onClose kept %d bitmaps", len(b.bitmaps))
	}

	reloaded, err := b.store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "验收白板" {
		t.Fatalf("reloaded board name = %q, want 验收白板", reloaded.Name)
	}
	sh := reloaded.Shapes[id]
	if sh == nil || sh.Type != doc.TypeText || sh.Text != "红按钮保存" {
		t.Fatalf("reloaded text shape = %+v, want 红按钮保存", sh)
	}
	_ = a
}

func TestBoardAndNoteTitleBarClearance(t *testing.T) {
	l := locales.In("zh-Hans")

	t.Run("board", func(t *testing.T) {
		a, b := boardTestShelf(t)
		cases := []struct {
			name string
			w, h int
			bar  ui.TitleBar
		}{
			{"mac-windowed", 1040, 680, ui.TitleBar{Height: 30, Left: 72, Bottom: 22}},
			{"mac-half-tiled", 720, 900, ui.TitleBar{Height: 30, Left: 72, Bottom: 22}},
			{"mac-fullscreen", 1440, 900, ui.TitleBar{}},
			{"windows-windowed", 1040, 680, ui.TitleBar{Height: 32, Right: 138, Bottom: 32}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				v := ui.NewTester(a.boardView, tc.w, tc.h)
				v.SetLocale("zh-Hans")
				v.SetTitleBar(tc.bar)
				v.Frame()

				if _, ok := v.Find("board-close"); ok {
					t.Fatal("self-drawn board close button still exists")
				}
				titleRect, ok := v.Find(b.currentTitle(l))
				if !ok {
					t.Fatal("board title button missing")
				}
				if titleRect.X < tc.bar.Left {
					t.Fatalf("board title X=%.1f overlaps mac traffic lights Left=%.1f", titleRect.X, tc.bar.Left)
				}
				topRect, ok := v.Find(l.BoardKeepOnTop())
				if !ok {
					t.Fatal("board keep-on-top button missing")
				}
				if tc.bar.Right > 0 && topRect.X+topRect.W > float32(tc.w)-tc.bar.Right {
					t.Fatalf("board top button right=%.1f overlaps windows caption buttons left=%.1f",
						topRect.X+topRect.W, float32(tc.w)-tc.bar.Right)
				}
				if b.stageRect.W != float32(tc.w) {
					t.Fatalf("stageRect.W=%.1f, want %d immediately on resize", b.stageRect.W, tc.w)
				}
			})
		}
	})

	t.Run("note", func(t *testing.T) {
		a, n := noteTestShelf(t)
		n.text = "# 便利贴标题\n正文\n"
		n.draft = n.text
		cases := []struct {
			name string
			w, h int
			bar  ui.TitleBar
		}{
			{"mac-windowed", noteW, noteH, ui.TitleBar{Height: 30, Left: 72, Bottom: 22}},
			{"windows-windowed", noteW, noteH, ui.TitleBar{Height: 32, Right: 138, Bottom: 32}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				v := ui.NewTester(a.noteView, tc.w, tc.h)
				v.SetLocale("zh-Hans")
				v.SetTitleBar(tc.bar)
				v.Frame()

				if _, ok := v.Find(l.NoteClose()); ok {
					t.Fatal("self-drawn note close button still exists")
				}
				titleRect, ok := v.Find("便利贴标题")
				if !ok {
					t.Fatal("note title missing")
				}
				if titleRect.X < tc.bar.Left {
					t.Fatalf("note title X=%.1f overlaps mac traffic lights Left=%.1f", titleRect.X, tc.bar.Left)
				}
				topRect, ok := v.Find(l.NoteKeepOnTop())
				if !ok {
					t.Fatal("note keep-on-top button missing")
				}
				if tc.bar.Right > 0 && topRect.X+topRect.W > float32(tc.w)-tc.bar.Right {
					t.Fatalf("note top button right=%.1f overlaps windows caption buttons left=%.1f",
						topRect.X+topRect.W, float32(tc.w)-tc.bar.Right)
				}
			})
		}
	})
}

func TestWindowsNoteLongTitleNotTruncated(t *testing.T) {
	oldFit := fitIssue
	var issues []string
	fitIssue = func(msg string) { issues = append(issues, msg) }
	t.Cleanup(func() { fitIssue = oldFit })

	a, n := noteTestShelf(t)
	title := "Windows 便利贴实测"
	n.text = "# " + title + "\n这是 Windows 320px 窗口长标题完整显示测试。\n"
	n.draft = n.text
	l := locales.In("zh-Hans")

	for _, tc := range []struct {
		name string
		bar  ui.TitleBar
	}{
		{"mac", ui.TitleBar{Height: 30, Left: 72, Bottom: 22}},
		{"windows", ui.TitleBar{Height: 32, Right: 138, Bottom: 32}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issues = nil
			v := ui.NewTester(a.noteView, noteW, noteH)
			v.SetLocale("zh-Hans")
			v.SetTitleBar(tc.bar)
			v.Frame()
			v.Frame()

			titleRect, ok := v.Find(title)
			if !ok {
				t.Fatalf("title %q missing; texts=%v", title, v.Texts())
			}
			revealRect, ok := v.Find(osText(l.NoteReveal(), l.NoteRevealWin()))
			if !ok {
				t.Fatal("note reveal button missing")
			}
			if gap := revealRect.X - (titleRect.X + titleRect.W); gap < 6-0.5 {
				t.Fatalf("%s title touches reveal button: titleRight=%.1f revealX=%.1f gap=%.1f",
					tc.name, titleRect.X+titleRect.W, revealRect.X, gap)
			}
			if len(issues) > 0 {
				t.Fatalf("%s note title overflowed/truncated: %v", tc.name, issues)
			}
			saveTestShot(t, "seg2", "note-title-"+tc.name+".png", v.Image())
		})
	}
}
