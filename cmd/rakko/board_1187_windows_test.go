//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"

	"rakko/internal/board/doc"
)

func TestBoardExportFailsAtomicallyWhenTargetLockedOnWindows(t *testing.T) {
	_, b := boardTestShelf(t)
	s := doc.NewShape(doc.NewRecordID("shape"), doc.TypeGeo)
	s.Parent, s.W, s.H = b.ed.Page, 80, 60
	s.Index = doc.FirstIndex
	b.ed.Doc.Shapes[s.ID] = s

	orig := boardSaveFileDialog
	t.Cleanup(func() { boardSaveFileDialog = orig })

	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		run  func()
	}{
		{"locked.png", func() { b.exportPNGFile(nil) }},
		{"locked.svg", func() { b.exportSVGFile(nil) }},
		{"locked.rakkoboard.json", func() { b.exportBoardFile() }},
	} {
		target := filepath.Join(dir, tc.name)
		if err := os.WriteFile(target, []byte("keep-me"), 0o644); err != nil {
			t.Fatal(err)
		}
		ptr, err := windows.UTF16PtrFromString(target)
		if err != nil {
			t.Fatal(err)
		}
		// Allow read+write sharing but disallow delete/rename so bare os.WriteFile
		// truncates the target while atomicfile.WriteFile refuses and keeps it whole.
		h, err := windows.CreateFile(ptr, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		boardSaveFileDialog = func(*mygo.Window, string, string, []string) (string, error) { return target, nil }
		tc.run()
		windows.CloseHandle(h)

		got, err := os.ReadFile(target)
		if err != nil || string(got) != "keep-me" {
			t.Fatalf("Windows locked export %s modified target instead of failing atomically: got=%q err=%v", tc.name, got, err)
		}
	}
}
