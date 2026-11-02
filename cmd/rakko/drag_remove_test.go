//go:build darwin

package main

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/transfer"

	"rakko/internal/store"
)

// dropOut finishes a drag of ids out of the tray as a successful drop.
func dropOut(a *shelf, ids ...string) {
	a.dragOutIDs = ids
	a.completed(transfer.Result{Operation: transfer.Copy})
}

func trayIDs(a *shelf) []string {
	var ids []string
	for _, it := range a.itemsFor(pageTray) {
		ids = append(ids, it.ID)
	}
	return ids
}

// With 拖出后从托盘移除 on, a dropped screenshot leaves the tray at once
// but its file stays for the grace period (the app it was dropped on may
// still be reading it); a pinned item stays; a clipboard capture stays in
// the clipboard history.
func TestRemoveAfterDrag(t *testing.T) {
	a, _, paths := convertShelf(t, "a.txt")
	a.settings.RemoveAfterDrag = true
	if err := a.store.AddImage(testPNG(t)); err != nil {
		t.Fatal(err)
	}
	shot := a.store.Items[0]
	if err := a.store.AddClipText("会议纪要", "com.apple.Notes"); err != nil {
		t.Fatal(err)
	}
	if err := a.store.SetPinned(a.store.Items[0].ID, true); err != nil { // pinned clip text shows on the tray
		t.Fatal(err)
	}
	pinned := a.store.Items[0]
	file := a.store.Items[2]
	dropOut(a, shot.ID, pinned.ID, file.ID)
	if got := trayIDs(a); !slices.Equal(got, []string{pinned.ID}) {
		t.Fatalf("tray = %v, want only the pinned item", got)
	}
	a.flushLeaving(time.Now().Add(-leaveGrace))
	if _, err := os.Stat(shot.Path); err != nil {
		t.Fatalf("the screenshot's file must outlive the drop by the grace: %v", err)
	}
	a.flushLeaving(time.Now().Add(time.Second))
	if _, err := os.Stat(shot.Path); !os.IsNotExist(err) {
		t.Fatalf("the screenshot's file must be gone after the grace: %v", err)
	}
	if len(a.store.Items) != 1 || a.store.Items[0].ID != pinned.ID {
		t.Fatalf("store = %v, want only the pinned item left", a.store.Items)
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("the original file must never be deleted: %v", err)
	}
}

// A clipboard screenshot dragged off the tray stays in the clipboard
// history; the setting off leaves the tray alone; adding an item again
// during the grace brings it back.
func TestRemoveAfterDragKeepsHistoryAndReAdds(t *testing.T) {
	a, _, paths := convertShelf(t, "a.txt")
	if err := a.store.AddClipImage(testPNG(t), "com.apple.screencaptureui"); err != nil {
		t.Fatal(err)
	}
	clip := a.store.Items[0]
	file := a.store.Items[1]

	dropOut(a, clip.ID, file.ID)
	if len(trayIDs(a)) != 2 {
		t.Fatal("with the setting off a drag must not remove anything")
	}

	a.settings.RemoveAfterDrag = true
	dropOut(a, clip.ID, file.ID)
	if len(trayIDs(a)) != 0 {
		t.Fatalf("tray = %v, want both gone", trayIDs(a))
	}
	time.Sleep(2 * time.Millisecond) // the re-add must be stamped after the drop
	if err := a.store.AddFiles([]string{paths[0]}); err != nil {
		t.Fatal(err)
	}
	if got := trayIDs(a); !slices.Equal(got, []string{file.ID}) {
		t.Fatalf("tray = %v, want the re-added file back", got)
	}
	a.flushLeaving(time.Now().Add(time.Second))
	if !slices.ContainsFunc(a.itemsFor(pageClipboard), func(it store.Item) bool { return it.ID == clip.ID }) {
		t.Fatal("the capture must stay in the clipboard history")
	}
	if got := trayIDs(a); !slices.Equal(got, []string{file.ID}) {
		t.Fatalf("tray = %v, the re-added file must survive the flush", got)
	}
}

// When saving fails the removal is not lost: the item stays off the tray
// and is removed on the next try a grace later.
func TestRemoveAfterDragRetriesAFailedSave(t *testing.T) {
	a, _, _ := convertShelf(t, "a.txt")
	a.settings.RemoveAfterDrag = true
	file := a.store.Items[0]
	dropOut(a, file.ID)
	if err := os.Chmod(a.store.Root, 0o500); err != nil { // state.json cannot be replaced
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(a.store.Root, 0o700) })
	a.flushLeaving(time.Now().Add(time.Second))
	if len(a.store.Items) != 1 || len(trayIDs(a)) != 0 {
		t.Fatalf("after a failed save: store %d items, tray %v; want kept but hidden", len(a.store.Items), trayIDs(a))
	}
	if err := os.Chmod(a.store.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	a.flushLeaving(time.Now().Add(-leaveGrace))
	if len(a.store.Items) != 1 {
		t.Fatal("the retry must wait for another grace")
	}
	a.flushLeaving(time.Now().Add(time.Second))
	if len(a.store.Items) != 0 {
		t.Fatalf("the retry must remove the item: %v", a.store.Items)
	}
}
