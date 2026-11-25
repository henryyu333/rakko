package doc

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func encodeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTrashRemovesMetaAndListDetectsStaleMeta(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "Board")
	if err != nil {
		t.Fatal(err)
	}
	id1 := st.ID()
	d1, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	d1.Name = "To Trash"
	if err := st.Write(d1); err != nil {
		t.Fatal(err)
	}
	meta1 := filepath.Join(dir, DirName, id1+metaExt)
	if _, err := os.Stat(meta1); err != nil {
		t.Fatalf("expected meta cache %s: %v", meta1, err)
	}

	if _, err := st.New("Kept"); err != nil {
		t.Fatal(err)
	}
	id2 := st.ID()

	fakeTrash := func(p string) (string, error) { return p, os.Remove(p) }
	if err := st.Trash(id1, fakeTrash); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(meta1); !os.IsNotExist(err) {
		t.Fatalf("Trash left meta cache behind: err=%v", err)
	}

	// Replace <id2>.json externally with a newer ModTime while <id2>.meta.json stays older.
	meta2Path := filepath.Join(dir, DirName, id2+metaExt)
	meta2Info, err := os.Stat(meta2Path)
	if err != nil {
		t.Fatal(err)
	}
	older := meta2Info.ModTime().Add(-30 * time.Second)
	if err := os.Chtimes(meta2Path, older, older); err != nil {
		t.Fatal(err)
	}

	extDoc := NewDrawing(id2, "Externally Replaced")
	s1 := NewShape("shape:e1", TypeGeo)
	s1.Parent = extDoc.ActivePage
	s2 := NewShape("shape:e2", TypeText)
	s2.Parent = extDoc.ActivePage
	extDoc.Shapes[s1.ID] = s1
	extDoc.Shapes[s2.ID] = s2
	raw, err := json.MarshalIndent(extDoc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	board2Path := st.PathFor(id2)
	if err := os.WriteFile(board2Path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	newer := meta2Info.ModTime().Add(30 * time.Second)
	if err := os.Chtimes(board2Path, newer, newer); err != nil {
		t.Fatal(err)
	}

	list, err := st.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Name != "Externally Replaced" || list[0].ItemCount != 2 {
		t.Fatalf("stale List() = %+v, want Name=%q ItemCount=2", list, "Externally Replaced")
	}
	if name, count := CardInfo(dir, "fallback"); name != "Externally Replaced" || count != 2 {
		t.Fatalf("stale CardInfo() = (%q, %d), want (%q, 2)", name, count, "Externally Replaced")
	}
}

func TestPruneUnusedAssetsOnReadAndExport(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir, "Board")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := st.SaveAsset(encodeTestPNG(t), "png", "shot.png", "image/png", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	img := NewShape("shape:img1", TypeImage)
	img.Parent = d.ActivePage
	img.AssetID = asset.ID
	d.Assets[asset.ID] = asset
	d.Shapes[img.ID] = img

	// Simulate deleting the image shape while d.Assets still keeps the entry for in-session Undo.
	delete(d.Shapes, img.ID)

	rawPkg, err := ExportPackage(d, st)
	if err != nil {
		t.Fatal(err)
	}
	var pkg BoardPackage
	if err := json.Unmarshal(rawPkg, &pkg); err != nil {
		t.Fatal(err)
	}
	if len(pkg.Assets) != 0 || len(pkg.Drawing.Assets) != 0 {
		t.Fatalf("ExportPackage packed deleted asset: pkg.Assets=%d drawing.Assets=%d", len(pkg.Assets), len(pkg.Drawing.Assets))
	}
	if d.Assets[asset.ID] == nil {
		t.Fatal("ExportPackage mutated live d.Assets map instead of pruning snapshot copy")
	}

	if err := st.Write(d); err != nil {
		t.Fatal(err)
	}
	reopened, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Assets) != 0 {
		t.Fatalf("reopened drawing kept %d unreferenced assets: %+v", len(reopened.Assets), reopened.Assets)
	}
}

func TestBOMPrefixedBoardFilesAndPackageImport(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	dir := t.TempDir()
	st, err := Open(dir, "Board")
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID()
	d, err := st.Read()
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "BOM Board"
	s := NewShape("shape:bom1", TypeGeo)
	s.Parent = d.ActivePage
	d.Shapes[s.ID] = s

	rawBoard, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.PathFor(id), append(append([]byte(nil), bom...), rawBoard...), 0o600); err != nil {
		t.Fatal(err)
	}

	metaBytes, err := json.Marshal(boardMeta{Name: "BOM Board", ItemCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, DirName, id+metaExt), append(append([]byte(nil), bom...), metaBytes...), 0o600); err != nil {
		t.Fatal(err)
	}

	keptAsset, err := st.SaveAsset(encodeTestPNG(t), "png", "kept.png", "image/png", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-48 * time.Hour)
	keptFull, _ := st.AssetPath(keptAsset.Path)
	_ = os.Chtimes(keptFull, past, past)
	trashBytes, _ := json.Marshal([]string{keptAsset.Path})
	if err := os.WriteFile(filepath.Join(dir, DirName, trashAssetsFile), append(append([]byte(nil), bom...), trashBytes...), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := st.Read()
	if err != nil {
		t.Fatalf("Read BOM board failed: %v", err)
	}
	if got.Name != "BOM Board" || len(got.Shapes) != 1 {
		t.Fatalf("BOM Read = name=%q shapes=%d", got.Name, len(got.Shapes))
	}
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].Name != "BOM Board" || list[0].ItemCount != 1 {
		t.Fatalf("BOM List = %+v, err=%v", list, err)
	}
	removed, err := st.CleanAssets(time.Now(), 24*time.Hour)
	if err != nil {
		t.Fatalf("CleanAssets on BOM trash-assets failed: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("CleanAssets deleted asset protected by BOM .trash-assets.json: %v", removed)
	}

	rawPkg, err := ExportPackage(got, st)
	if err != nil {
		t.Fatal(err)
	}
	importedPkg, err := ImportPackage(append(append([]byte(nil), bom...), rawPkg...), st, "Fallback")
	if err != nil {
		t.Fatalf("ImportPackage BOM BoardPackage failed: %v", err)
	}
	if importedPkg.Name != "BOM Board" {
		t.Fatalf("importedPkg.Name = %q", importedPkg.Name)
	}
	bareRaw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	importedBare, err := ImportPackage(append(append([]byte(nil), bom...), bareRaw...), st, "Fallback")
	if err != nil {
		t.Fatalf("ImportPackage BOM bare Drawing failed: %v", err)
	}
	if importedBare.Name != "BOM Board" {
		t.Fatalf("importedBare.Name = %q", importedBare.Name)
	}
}

func TestOpenCleansTempLeftoversInBoardsAndAssets(t *testing.T) {
	dir := t.TempDir()
	boardsDir := filepath.Join(dir, DirName)
	assetsDir := filepath.Join(boardsDir, AssetsDirName)
	if err := os.MkdirAll(assetsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	tempBoard := filepath.Join(boardsDir, ".b1.json.tmp-123")
	tempAsset := filepath.Join(assetsDir, ".deadbeef.png.tmp-456")
	trashFile := filepath.Join(boardsDir, trashAssetsFile)
	dotFile := filepath.Join(boardsDir, ".DS_Store")
	for _, p := range []string{tempBoard, tempAsset, trashFile, dotFile} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := Open(dir, "Board"); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{tempBoard, tempAsset} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("expected temp leftover %s to be removed, err=%v", gone, err)
		}
	}
	for _, keep := range []string{trashFile, dotFile} {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected non-temp dotfile %s to stay intact: %v", keep, err)
		}
	}
}
