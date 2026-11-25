//go:build darwin

package main

// Headless evidence for the sticky note's resting chrome (solid blue pin,
// hairline under the bar, the faded reveal button, breathing room above the
// text) and for the panel's empty state plate.

import (
	"image/color"
	"os"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/locales"
)

func TestPolish1189NoteAndEmptyShots(t *testing.T) {
	if os.Getenv("RAKKO_SHOTS") == "" {
		t.Skip("set RAKKO_SHOTS to write the note and empty-state shots")
	}
	a := &shelf{dataDir: t.TempDir()}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	t.Cleanup(func() {
		if n.watch != nil {
			closeNoteWatch(n)
			n.watch = nil
		}
	})
	n.win = nil
	a.note = n
	n.draft = "1. 快捷键悬停 × 一键清除\n2. 截图序号反色与续号\n3. 剪贴板悬停置顶与删除\n4. 便利贴置顶更明确，留出顶部呼吸感"
	n.top = true
	note := ui.NewTester(a.noteView, noteW, noteH)
	note.SetScale(2)
	note.Frame()
	note.Frame()
	saveTestShot(t, "polish1189", "note-window.png", note.Image())

	_, empty, _ := clipShelf(t)
	empty.SetScale(2)
	empty.Frame()
	empty.Frame()
	saveTestShot(t, "polish1189", "panel-empty.png", empty.Image())
}

// The bar's idle buttons fade until the pointer reaches the bar; the pin
// stays at full strength while it is on. A pixel comparison catches the
// faded glyph and the hover restore without a native window.
func TestNoteBarFadesUntilHovered(t *testing.T) {
	a := &shelf{dataDir: t.TempDir()}
	n := a.noteSession()
	if n == nil {
		t.Fatal("no note session")
	}
	t.Cleanup(func() {
		if n.watch != nil {
			closeNoteWatch(n)
			n.watch = nil
		}
	})
	n.win = nil
	a.note = n
	n.draft = "idle bar"
	view := ui.NewTester(a.noteView, noteW, noteH)
	view.SetScale(2)
	view.Frame()
	view.Frame()
	label := osText(locales.In("").NoteReveal(), locales.In("").NoteRevealWin())
	r, ok := view.Find(label)
	if !ok {
		t.Fatalf("reveal button did not draw: %v", view.Texts())
	}
	lum := func(img interface {
		At(x, y int) color.Color
	}) float64 {
		sum, count := 0.0, 0
		for y := int(r.Y * 2); y < int((r.Y+r.H)*2); y++ {
			for x := int(r.X * 2); x < int((r.X+r.W)*2); x++ {
				cr, cg, cb, _ := img.At(x, y).RGBA()
				sum += 0.2126*float64(cr>>8) + 0.7152*float64(cg>>8) + 0.0722*float64(cb>>8)
				count++
			}
		}
		return sum / float64(count)
	}
	idle := lum(view.Image())
	view.Move(r.X+r.W/2, r.Y+r.H/2)
	view.Frame()
	view.Frame()
	hovered := lum(view.Image())
	if hovered <= idle {
		t.Fatalf("the bar button did not brighten on hover: idle %.1f hovered %.1f", idle, hovered)
	}
}
