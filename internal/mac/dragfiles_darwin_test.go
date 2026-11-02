//go:build darwin

package mac

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"unsafe"
)

// nsObjects returns an autoreleased NSArray of ids, as convert_darwin.go
// builds arrays: arrayWithObjects:count: over a Go slice.
func nsObjects(objs []id) id {
	if len(objs) == 0 {
		return send(class("NSArray"), "array")
	}
	return send(class("NSArray"), "arrayWithObjects:count:",
		uintptr(unsafe.Pointer(&objs[0])), uintptr(len(objs)))
}

// writeTestBoard returns a fresh private pasteboard carrying items. Reading
// a scratch board keeps the user's clipboard and the real drag board out of
// unit tests entirely.
func writeTestBoard(t *testing.T, files []string, filenames []string) id {
	t.Helper()
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	board := send(class("NSPasteboard"), "pasteboardWithUniqueName")
	if board == 0 {
		t.Fatal("no scratch pasteboard")
	}
	t.Cleanup(func() {
		withPool(func() { send(board, "releaseGlobally") })
	})
	withPool(func() {
		send(board, "clearContents")
		if len(files) > 0 {
			urls := make([]id, 0, len(files))
			for _, p := range files {
				urls = append(urls, send(class("NSURL"), "fileURLWithPath:", uintptr(nsString(p))))
			}
			send(board, "writeObjects:", uintptr(nsObjects(urls)))
		}
		if filenames != nil {
			names := make([]id, 0, len(filenames))
			for _, p := range filenames {
				names = append(names, nsString(p))
			}
			send(board, "declareTypes:owner:", uintptr(nsObjects([]id{nsString("NSFilenamesPboardType")})), 0)
			send(board, "setPropertyList:forType:", uintptr(nsObjects(names)), uintptr(nsString("NSFilenamesPboardType")))
		}
	})
	return board
}

func TestDragFilesFromReadsItemURLs(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "报告 汇总.pdf")
	folder := filepath.Join(dir, "素材 dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	board := writeTestBoard(t, []string{file, folder}, nil)
	var paths []string
	withPool(func() { paths = dragFilesFrom(board) })
	want := []string{file, folder}
	if !slices.Equal(paths, want) {
		t.Fatalf("dragFilesFrom = %q, want %q", paths, want)
	}
}

func TestDragFilesFromFallsBackToFilenames(t *testing.T) {
	// A source that only declares the classic filename list still names its
	// files; the same path coming back through a file URL must not double.
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	board := writeTestBoard(t, []string{a}, []string{a, b})
	var paths []string
	withPool(func() { paths = dragFilesFrom(board) })
	if !slices.Equal(paths, []string{a, b}) {
		t.Fatalf("dragFilesFrom = %q, want %q (deduped union)", paths, []string{a, b})
	}
}

func TestDragFilesFromEmptyBoard(t *testing.T) {
	board := writeTestBoard(t, nil, nil)
	withPool(func() {
		send(board, "setString:forType:", uintptr(nsString("hello")), uintptr(nsString("public.utf8-plain-text")))
	})
	var paths []string
	withPool(func() { paths = dragFilesFrom(board) })
	if len(paths) != 0 {
		t.Fatalf("a text-only board answered files %q", paths)
	}
}

// TestDragFilesOnLiveDragBoard exercises the real NSDragPboard end to end:
// it writes the two drag representations there and reads them back through
// DragFiles. Writing a shared board outside a session is how the parent
// learns whether its parse path survives the live board; the board is
// cleared afterwards so no stale test content outlives the run. Off by
// default because it mutates a shared system object; run with
// RAKKO_DRAGFILES_LIVE=1.
func TestDragFilesOnLiveDragBoard(t *testing.T) {
	if os.Getenv("RAKKO_DRAGFILES_LIVE") != "1" {
		t.Skip("set RAKKO_DRAGFILES_LIVE=1 to touch the real drag pasteboard")
	}
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "live 图片.png")
	folder := filepath.Join(dir, "live 文件夹")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	board := dragPasteboard()
	t.Cleanup(func() {
		withPool(func() { send(board, "clearContents") })
	})
	withPool(func() {
		send(board, "clearContents")
		urls := []id{send(class("NSURL"), "fileURLWithPath:", uintptr(nsString(file)))}
		send(board, "writeObjects:", uintptr(nsObjects(urls)))
		send(board, "declareTypes:owner:", uintptr(nsObjects([]id{nsString("NSFilenamesPboardType")})), 0)
		send(board, "setPropertyList:forType:", uintptr(nsObjects([]id{nsString(file), nsString(folder)})),
			uintptr(nsString("NSFilenamesPboardType")))
	})
	paths := DragFiles()
	want := []string{file, folder}
	if !slices.Equal(paths, want) {
		t.Fatalf("DragFiles on the live drag board = %q, want %q", paths, want)
	}
}
