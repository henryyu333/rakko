//go:build windows

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/internal/store"
)

func windowsDropShelf(t *testing.T) *shelf {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	a := &shelf{
		store:        s,
		settings:     settings.Default(),
		notchX:       870,
		notchY:       0,
		notchW:       180,
		notchH:       32,
		hasNotch:     true,
		thumbnails:   map[string]*ui.Bitmap{},
		thumbPending: map[string]bool{},
		appNames:     map[string]string{},
	}
	a.conv = &converter{host: a}
	return a
}

const winDropTestH = sheetH + navGap + navH + 2

func TestWindowsDropZoneUsesHoverOfferFiles(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "sample.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := windowsDropShelf(t)
	a.dragging = true
	a.dragSerial = 1

	// Before any Offer arrives, Windows dragFiles stays nil.
	if got := dragFiles(a); got != nil {
		t.Fatalf("initial dragFiles = %#v; want nil", got)
	}

	// ui.DataDragOver delivers Offer.Files; mutating caller slice cannot corrupt shelf state.
	paths := []string{pdf}
	a.noteHoverOffer(true, transfer.Offer{
		Formats: []transfer.Format{transfer.FileList},
		Files:   paths,
	})
	paths[0] = "mutated"
	if got := dragFiles(a); !slices.Equal(got, []string{pdf}) {
		t.Fatalf("dragFiles after Offer = %#v; want [%q]", got, pdf)
	}
	if !a.hoverOfferKnown || a.hoverOfferCount != 1 {
		t.Fatalf("offer state: known=%v count=%d; want true, 1", a.hoverOfferKnown, a.hoverOfferCount)
	}

	// Render panel: shows PDF drop zone with 3 action cells and no nearby-share entry.
	a.visible = true
	view := ui.NewTester(a.view, sheetW, winDropTestH)
	for _, want := range []string{"投放", "1 个 PDF", "放入托盘", "转 Word", "每页存成图片", "打成 zip"} {
		if !view.HasText(want) {
			t.Fatalf("Windows PDF drop zone missing %q; texts=%v", want, view.Texts())
		}
	}
	for _, forbid := range []string{"附近共享", "隔空投送"} {
		if view.HasText(forbid) {
			t.Fatalf("Windows PDF drop zone must not show %q; texts=%v", forbid, view.Texts())
		}
	}

	// Hovering over child action cells causes root zone's over to become false;
	// cached Offer.Files must stay intact without incrementing hoverOfferCount.
	a.noteHoverOffer(false, transfer.Offer{})
	if got := dragFiles(a); !slices.Equal(got, []string{pdf}) || a.hoverOfferCount != 1 {
		t.Fatalf("after !over: files=%#v count=%d; want [%q], 1", got, a.hoverOfferCount, pdf)
	}

	// Tools page accepts the same offer paths to highlight matching action cards.
	a.dragOpenTools = true
	a.dropZoneSnap(true)
	if a.drop.on {
		t.Fatal("drop zone must stay hidden when dragOpenTools is true")
	}
	if class, n, live := a.toolCardDrag(); !live || n != 1 || class != classPDFs {
		t.Fatalf("toolCardDrag = (%v, %d, %v); want (%v, 1, true)", class, n, live, classPDFs)
	}

	// New drag serial clears the previous drag's Offer.Files.
	a.dragSerial = 2
	a.noteHoverOffer(false, transfer.Offer{})
	if a.hoverOfferKnown || dragFiles(a) != nil || a.hoverOfferCount != 0 {
		t.Fatalf("new dragSerial did not clear Offer state: known=%v files=%#v count=%d",
			a.hoverOfferKnown, dragFiles(a), a.hoverOfferCount)
	}
}

func TestWindowsEdgeOpenDoesNotFlashTrayBeforeOffer(t *testing.T) {
	dir := t.TempDir()
	pdf := filepath.Join(dir, "doc.pdf")
	if err := os.WriteFile(pdf, []byte("%PDF-1.4\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := windowsDropShelf(t)
	a.dragging = true
	a.dragSerial = 4
	a.show(true) // edge-open before OLE DragEnter has delivered Offer.Files

	if !a.awaitingHoverOffer(time.Now()) {
		t.Fatal("expected edge-open before Offer to wait briefly before drawing Tray")
	}
	view := ui.NewTester(a.view, sheetW, winDropTestH)
	if view.HasText("把文件拖到这里") || view.HasText("松开放入") || view.HasText("投放") {
		t.Fatalf("edge-open before Offer flashed unexpected UI: %v", view.Texts())
	}

	// Once OLE DragEnter delivers Offer.Files, the drop zone appears immediately.
	a.noteHoverOffer(true, transfer.Offer{
		Formats: []transfer.Format{transfer.FileList},
		Files:   []string{pdf},
	})
	view.Frame()
	if !view.HasText("投放") || !view.HasText("1 个 PDF") {
		t.Fatalf("drop zone did not appear after Offer.Files arrived: %v", view.Texts())
	}
	if view.HasText("把文件拖到这里") {
		t.Fatalf("tray empty state lingered alongside drop zone: %v", view.Texts())
	}
}

func TestWindowsNonFileOfferAndTimeoutFallbackToTray(t *testing.T) {
	// Non-file OLE drag (Offer.Files == nil) resolves immediately to normal Tray page.
	a := windowsDropShelf(t)
	a.dragging = true
	a.dragSerial = 9
	a.show(true)
	a.noteHoverOffer(true, transfer.Offer{Formats: []transfer.Format{transfer.Text}})
	if a.awaitingHoverOffer(time.Now()) {
		t.Fatal("settled non-file offer should not keep waiting")
	}
	view := ui.NewTester(a.view, sheetW, winDropTestH)
	if view.HasText("投放") || !view.HasText("把文件拖到这里") {
		t.Fatalf("non-file offer did not fall back to tray: %v", view.Texts())
	}

	// If no OLE Offer arrives within dropOfferWaitGrace, fallback draws Tray cleanly.
	a2 := windowsDropShelf(t)
	a2.dragging = true
	a2.dragSerial = 10
	a2.show(true)
	a2.hoverWaitUntil = time.Now().Add(-time.Millisecond)
	view2 := ui.NewTester(a2.view, sheetW, winDropTestH)
	if view2.HasText("投放") || !view2.HasText("把文件拖到这里") {
		t.Fatalf("expired offer wait did not fall back to tray: %v", view2.Texts())
	}
}
