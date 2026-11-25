//go:build darwin

package main

// Headless evidence for the sticky note's resting chrome (solid blue pin,
// hairline under the bar, the faded reveal button, breathing room above the
// text) and for the panel's empty state plate.

import (
	"os"
	"testing"

	"github.com/egoist/mygo/ui"
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
