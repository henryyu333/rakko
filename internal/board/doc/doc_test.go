package doc

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
	if len(snap1.Shapes) != 1 || snap1.Shapes[s.ID] != s {
		t.Fatal("Snapshot did not keep the original shape pointer")
	}
	d.Shapes[s.ID] = cloned
	if snap1.Shapes[s.ID] == cloned {
		t.Fatal("Snapshot followed a replaced shape pointer")
	}
}

func TestSnapshotDeepCopiesPagesAndUncachedClearsCache(t *testing.T) {
	d := NewDrawing("board1", "白板 1")
	s := NewShape("shape:1", TypeGeo)
	s.Cache = "cached"
	d.Shapes[s.ID] = s
	p1 := d.Pages[0].ID
	if !d.SetPageCamera(p1, PageCamera{X: 10, Y: 20, Z: 1.5}) {
		t.Fatal("SetPageCamera failed")
	}

	snap := d.Snapshot()
	d.SetPageCamera(p1, PageCamera{X: 99, Y: 88, Z: 2.0})
	if snap.Page(p1).Camera == nil || snap.Page(p1).Camera.X != 10 || snap.Page(p1).Camera.Z != 1.5 {
		t.Fatalf("Snapshot shared Page/Camera pointer with subsequent SetPageCamera: %+v", snap.Page(p1).Camera)
	}

	uncached := d.SnapshotUncached()
	if uncached.Shapes[s.ID] == s {
		t.Fatal("SnapshotUncached shared original Shape pointer")
	}
	if uncached.Shapes[s.ID].Cache != nil {
		t.Fatalf("SnapshotUncached kept Cache: %v", uncached.Shapes[s.ID].Cache)
	}
	if s.Cache != "cached" {
		t.Fatal("SnapshotUncached mutated original Shape.Cache")
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
	sh := NewShape("shape:a", TypeGeo)
	sh.Parent = d.ActivePage
	d.Shapes["shape:a"] = sh
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

func TestIndicesBetweenDegradedBounds(t *testing.T) {
	for _, tc := range []struct{ a, b string }{
		{"a1", "a0"},
		{"a1", "a1"},
	} {
		got := IndicesBetween(tc.a, tc.b, 3)
		want := IndicesBetween(tc.a, "", 3)
		if len(got) != len(want) {
			t.Fatalf("IndicesBetween(%q,%q) len=%d, want %d", tc.a, tc.b, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("IndicesBetween(%q,%q)[%d] = %q, want append-after-a %q",
					tc.a, tc.b, i, got[i], want[i])
			}
		}
	}
}

func TestNormalizeRepairsDuplicateIndicesAndOrphans(t *testing.T) {
	d := NewDrawing("b1", "B")
	page := d.ActivePage
	names := []string{"a", "b", "c"}
	for i, n := range names {
		s := NewShape(ID("shape:"+n), TypeGeo)
		s.Parent = page
		s.Index = "a1" // imported duplicate keys
		s.W, s.H = float64(i+1), 1
		d.Shapes[s.ID] = s
	}
	orphan := NewShape("shape:orphan", TypeGeo)
	orphan.Parent = "page:gone"
	d.Shapes[orphan.ID] = orphan
	d.Bindings["binding:x"] = &Binding{ID: "binding:x", From: orphan.ID, To: "shape:a"}

	d.Normalize("b1", "B")

	if d.Shapes[orphan.ID] != nil {
		t.Fatal("Normalize kept a shape whose page does not exist")
	}
	if d.Bindings["binding:x"] != nil {
		t.Fatal("Normalize kept a binding with a missing endpoint")
	}
	order := make([]string, 0, len(names))
	prev := ""
	for _, n := range names {
		s := d.Shapes[ID("shape:"+n)]
		if s == nil {
			t.Fatalf("Normalize dropped shape %q", n)
		}
		if s.Index == prev {
			t.Fatalf("duplicate index still present after Normalize: %q", s.Index)
		}
		prev = s.Index
		order = append(order, string(s.ID))
	}
	// Duplicate keys preserve the old 1px width ordering (a, b, c).
	want := []string{"shape:a", "shape:b", "shape:c"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestImportPackageLimits(t *testing.T) {
	st, err := Open(t.TempDir(), "B")
	if err != nil {
		t.Fatal(err)
	}
	// Whole-file size limit, checked before parsing.
	raw := make([]byte, MaxImportBytes+1)
	if _, err := ImportPackage(raw, st, "B"); err == nil {
		t.Fatal("import accepted a file over MaxImportBytes")
	}

	oldBytes, oldPixels := maxImportAssetBytes, maxImportPixels
	t.Cleanup(func() { maxImportAssetBytes, maxImportPixels = oldBytes, oldPixels })

	maxImportAssetBytes = 8
	pkg := BoardPackage{
		FormatVersion: 1,
		Drawing:       NewDrawing("imported", "Imported"),
		Assets: map[ID]*EmbeddedAsset{
			"asset:a": {
				Meta: &Asset{ID: "asset:a", Type: "image", Path: "assets/a.png", W: 1, H: 1},
				Data: []byte("123456789"),
			},
		},
	}
	body, err := json.Marshal(pkg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ImportPackage(body, st, "B"); err == nil {
		t.Fatal("import accepted an oversized asset")
	}

	maxImportAssetBytes = 1 << 20
	maxImportPixels = 50
	pkg.Assets["asset:a"].Data = []byte("x")
	pkg.Assets["asset:a"].Meta.W, pkg.Assets["asset:a"].Meta.H = 10, 10
	body, _ = json.Marshal(pkg)
	if _, err := ImportPackage(body, st, "B"); err == nil {
		t.Fatal("import accepted an asset over the pixel limit")
	}
}

func TestSegmentJSONCompactAndOldFormat(t *testing.T) {
	s := Segment{Type: "free", Points: []PointZ{{X: 1.234, Y: 2.345, Z: 0.5}, {X: 10, Y: 20, Z: 1}}}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"x"`)) || !bytes.Contains(raw, []byte("[")) {
		t.Fatalf("segment is not a flat array: %s", raw)
	}
	var back Segment
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Points) != 2 || back.Points[0].X != 1.23 || back.Points[1].Z != 1 {
		t.Fatalf("round trip = %+v", back.Points)
	}
	old := []byte(`{"type":"free","points":[{"x":3,"y":4,"z":0.25},{"x":5,"y":6}]}`)
	var legacy Segment
	if err := json.Unmarshal(old, &legacy); err != nil {
		t.Fatal(err)
	}
	if len(legacy.Points) != 2 || legacy.Points[0].X != 3 || legacy.Points[0].Z != 0.25 {
		t.Fatalf("old format = %+v", legacy.Points)
	}
}

func TestBoardMetaCacheAndList(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "Board")
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID()
	if name, count := CardInfo(dir, "fallback"); name != "Board" || count != 0 {
		t.Fatalf("card info = %q %d", name, count)
	}
	d, _ := st.Read()
	s := NewShape("shape:a", TypeGeo)
	s.Parent = d.ActivePage
	d.Shapes[s.ID] = s
	d.Name = "架构图"
	if err := st.Write(d); err != nil {
		t.Fatal(err)
	}
	if name, count := CardInfo(dir, "fallback"); name != "架构图" || count != 1 {
		t.Fatalf("card info after write = %q %d", name, count)
	}
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].Name != "架构图" || list[0].ItemCount != 1 {
		t.Fatalf("list = %+v, err=%v", list, err)
	}
	if list[0].ID != id {
		t.Fatalf("list id = %s", list[0].ID)
	}
}

func TestCleanAssetsKeepsReferencedAndTrashedBoards(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "Board")
	if err != nil {
		t.Fatal(err)
	}
	a, err := st.SaveAsset([]byte("active"), "png", "a", "image/png", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := st.Read()
	d.Assets[a.ID] = a
	if err := st.Write(d); err != nil {
		t.Fatal(err)
	}
	dead, _ := st.SaveAsset([]byte("dead"), "png", "dead", "image/png", 1, 1)

	if _, err := st.New("Trashed"); err != nil {
		t.Fatal(err)
	}
	trashedID := st.ID()
	c, _ := st.SaveAsset([]byte("trashed-ref"), "png", "c", "image/png", 1, 1)
	d2, _ := st.Read()
	d2.Assets[c.ID] = c
	if err := st.Write(d2); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	past := now.Add(-48 * time.Hour)
	for _, asset := range []*Asset{a, dead, c} {
		full, err := st.AssetPath(asset.Path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, past, past); err != nil {
			t.Fatal(err)
		}
	}
	fakeTrash := func(p string) (string, error) { return p, os.Remove(p) }
	if err := st.Trash(trashedID, fakeTrash); err != nil {
		t.Fatal(err)
	}
	removed, err := st.CleanAssets(now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != filepath.Base(dead.Path) {
		t.Fatalf("removed = %v, want only %s", removed, filepath.Base(dead.Path))
	}
	for _, asset := range []*Asset{a, c} {
		full, _ := st.AssetPath(asset.Path)
		if _, err := os.Stat(full); err != nil {
			t.Fatalf("%s was deleted", asset.Path)
		}
	}
}

func TestBoardReadsOldFormatAndWritesCompact(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "Board")
	if err != nil {
		t.Fatal(err)
	}
	old := `{
  "version": 1, "id": "` + st.ID() + `", "name": "Old", "activePage": "page:1",
  "pages": [{"id":"page:1","name":"Page 1","index":"a1"}],
  "shapes": {"shape:a": {"id":"shape:a","type":"draw","parent":"page:1","index":"a1","opacity":1,
    "segments":[{"type":"free","points":[{"x":1.5,"y":2.25,"z":0.5},{"x":3,"y":4}]}]}}
}`
	if err := os.WriteFile(st.Path(), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	d, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Shapes["shape:a"]; got == nil || len(got.Segments) != 1 || len(got.Segments[0].Points) != 2 || got.Segments[0].Points[0].X != 1.5 {
		t.Fatalf("old board read = %+v", got)
	}
	if err := st.Write(d); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"points":[{"x":`)) || bytes.Contains(raw, []byte("\n  ")) {
		t.Fatalf("new board is not compact: %s", raw[:min(len(raw), 200)])
	}
	again, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again.Shapes["shape:a"].Segments, d.Shapes["shape:a"].Segments) {
		t.Fatal("compact round trip changed the stroke")
	}
}
