package doc

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFractionalIndexingOrder(t *testing.T) {
	keys := []string{FirstIndex}
	// Append 50 keys.
	for i := 0; i < 50; i++ {
		keys = append(keys, IndexBetween(keys[len(keys)-1], ""))
	}
	// Prepend 50 keys.
	for i := 0; i < 50; i++ {
		k := IndexBetween("", keys[0])
		keys = append([]string{k}, keys...)
	}
	// Insert 30 keys in tight midpoint intervals.
	a, b := keys[40], keys[41]
	for i := 0; i < 30; i++ {
		mid := IndexBetween(a, b)
		if !(a < mid && mid < b) {
			t.Fatalf("midpoint %d out of order: %q < %q < %q", i, a, mid, b)
		}
		b = mid
	}
	for i := 1; i < len(keys); i++ {
		if !(keys[i-1] < keys[i]) {
			t.Fatalf("keys not strictly increasing at %d: %q >= %q", i, keys[i-1], keys[i])
		}
	}

	batch := IndicesBetween("a1", "a5", 5)
	if len(batch) != 5 {
		t.Fatalf("IndicesBetween len = %d", len(batch))
	}
	prev := "a1"
	for _, k := range batch {
		if !(prev < k && k < "a5") {
			t.Fatalf("batch out of bounds: %q -> %q", prev, k)
		}
		prev = k
	}
}

func TestShapeCloneMovedAndSnapshotEqual(t *testing.T) {
	s := NewShape("shape:1", TypeGeo)
	s.Cache = "cached-path"
	moved := s.Moved()
	if moved.Cache != "cached-path" {
		t.Fatalf("Moved dropped cache")
	}
	cloned := s.Clone()
	if cloned.Cache != nil {
		t.Fatalf("Clone kept cache: %v", cloned.Cache)
	}

	d := NewDrawing("board1", "白板 1")
	d.Shapes[s.ID] = s
	snap1 := d.Snapshot()
	if !Equal(d, snap1) {
		t.Fatal("Snapshot not equal to original")
	}
	d.Shapes[s.ID] = cloned
	if Equal(d, snap1) {
		t.Fatal("Equal should detect replaced shape pointer")
	}
}

func TestDrawingPagesCRUD(t *testing.T) {
	d := NewDrawing("b1", "Board 1")
	if err := d.DeletePage(d.Pages[0].ID); !errors.Is(err, ErrLastPage) {
		t.Fatalf("deleting last page = %v, want ErrLastPage", err)
	}
	p2 := d.AddPage("第二页")
	if len(d.Pages) != 2 || p2.Name != "第二页" {
		t.Fatalf("AddPage failed: %+v", d.Pages)
	}
	if !d.RenamePage(p2.ID, "流程图") || d.Page(p2.ID).Name != "流程图" {
		t.Fatalf("RenamePage failed")
	}
	d.ActivePage = p2.ID
	if err := d.DeletePage(p2.ID); err != nil {
		t.Fatalf("DeletePage failed: %v", err)
	}
	if len(d.Pages) != 1 || d.ActivePage != d.Pages[0].ID {
		t.Fatalf("after DeletePage: pages=%d active=%s", len(d.Pages), d.ActivePage)
	}
}

func TestStoreLifecycleAndTrash(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "白板 1")
	if err != nil {
		t.Fatal(err)
	}
	firstID := st.ID()
	d, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "白板 1" {
		t.Fatalf("Name = %q", d.Name)
	}
	d.Shapes["shape:a"] = NewShape("shape:a", TypeGeo)
	if err := st.Write(d); err != nil {
		t.Fatal(err)
	}

	// Refuses deleting the last board.
	var trashed []string
	fakeTrash := func(p string) (string, error) {
		trashed = append(trashed, p)
		return p, os.Remove(p)
	}
	if err := st.Trash(firstID, fakeTrash); !errors.Is(err, ErrLastBoard) {
		t.Fatalf("Trash last board = %v, want ErrLastBoard", err)
	}

	// Creating a second board switches active board and persists both.
	time.Sleep(5 * time.Millisecond)
	d2, err := st.New("白板 2")
	if err != nil {
		t.Fatal(err)
	}
	if st.ID() == firstID || d2.Name != "白板 2" {
		t.Fatalf("New board id=%s name=%q", st.ID(), d2.Name)
	}
	list, err := st.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %+v, err=%v", list, err)
	}

	// Select back to first board.
	if err := st.Select(firstID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := st.Read()
	if err != nil || len(reloaded.Shapes) != 1 {
		t.Fatalf("reloaded shapes = %d, err=%v", len(reloaded.Shapes), err)
	}

	// Trash active board switches current to surviving second board.
	if err := st.Trash(firstID, fakeTrash); err != nil {
		t.Fatal(err)
	}
	if len(trashed) != 1 || filepath.Base(trashed[0]) != firstID+FileExt {
		t.Fatalf("trashed = %v", trashed)
	}
	if st.ID() != d2.ID {
		t.Fatalf("active board after trash = %s, want %s", st.ID(), d2.ID)
	}

	// Reopening picks the surviving board cleanly.
	reopen, err := Open(dir, "白板 1")
	if err != nil || reopen.ID() != d2.ID {
		t.Fatalf("reopen = %s, err=%v", reopen.ID(), err)
	}
}

func TestStoreCorruptFileNotOverwritten(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "白板 1")
	if err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{broken json")
	if err := os.WriteFile(st.Path(), corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(dir, "白板 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st2.Read(); err == nil {
		t.Fatal("expected error reading corrupt board JSON")
	}
	got, _ := os.ReadFile(st.Path())
	if string(got) != string(corrupt) {
		t.Fatal("corrupt board file was modified on Open")
	}
}

func TestStoreAssetDedupAndPathSafety(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "白板 1")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("fake-png-bytes-12345")
	a1, err := st.SaveAsset(payload, ".PNG", "shot.png", "image/png", 100, 80)
	if err != nil {
		t.Fatal(err)
	}
	a2, err := st.SaveAsset(payload, "png", "other.png", "image/png", 100, 80)
	if err != nil {
		t.Fatal(err)
	}
	if a1.Path != a2.Path || a1.Hash != a2.Hash {
		t.Fatalf("asset not deduplicated: %+v vs %+v", a1, a2)
	}
	full, err := st.AssetPath(a1.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(full); err != nil {
		t.Fatalf("asset missing at %s: %v", full, err)
	}
	if _, err := st.AssetPath("../secret.txt"); err == nil {
		t.Fatal("AssetPath allowed directory traversal")
	}
}
