package store

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s, root
}

// realRoot returns the canonical (symlink-resolved) form the store uses
// for Root, since Open resolves symlinks (e.g. macOS /var → /private/var).
func realRoot(t *testing.T, root string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", root, err)
	}
	return resolved
}

// symlinkTo creates a symlink pointing at dir and returns its path.
func symlinkTo(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "data-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	return link
}

func makePNG(t *testing.T, w, h int, seed color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetRGBA(x, y, seed)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// pngHeaderOnly builds a valid signature + IHDR with arbitrary
// dimensions (no image data): DecodeConfig alone accepts it, which is
// exactly the truncated shape AddImage must reject via full decoding.
func pngHeaderOnly(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8], ihdr[9] = 8, 6 // 8-bit RGBA
	chunk := append([]byte("IHDR"), ihdr...)
	binary.Write(&b, binary.BigEndian, uint32(len(ihdr)))
	b.Write(chunk)
	binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(chunk))
	return b.Bytes()
}

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
}

// itemIDs returns the IDs of s.Items in order.
func itemIDs(s *Store) []string {
	ids := make([]string, len(s.Items))
	for i, it := range s.Items {
		ids[i] = it.ID
	}
	return ids
}

// setAge backdates or forward-dates an item's CreatedAt directly so
// retention tests never depend on wall-clock waits.
func setAge(t *testing.T, s *Store, id string, at time.Time) {
	t.Helper()
	for i := range s.Items {
		if s.Items[i].ID == id {
			s.Items[i].CreatedAt = at
			return
		}
	}
	t.Fatalf("no item with id %q", id)
}

func TestTextHistoryLimitDedupAndOrder(t *testing.T) {
	s, _ := newStore(t)
	for i := range 35 {
		if err := s.AddText(fmt.Sprintf("clip-%02d", i)); err != nil {
			t.Fatalf("AddText %d: %v", i, err)
		}
	}
	textCount := 0
	for _, it := range s.Items {
		if it.Kind == KindText {
			textCount++
		}
	}
	if textCount != 30 {
		t.Fatalf("got %d text items, want 30", textCount)
	}
	if s.Items[0].Text != "clip-34" {
		t.Fatalf("newest item = %q, want clip-34", s.Items[0].Text)
	}
	if s.Items[len(s.Items)-1].Text != "clip-05" {
		t.Fatalf("oldest item = %q, want clip-05 (clip-00..04 evicted)", s.Items[len(s.Items)-1].Text)
	}
	// Re-adding existing text dedups and moves it to the front.
	midID := ""
	for _, it := range s.Items {
		if it.Text == "clip-10" {
			midID = it.ID
		}
	}
	if err := s.AddText("clip-10"); err != nil {
		t.Fatalf("re-AddText: %v", err)
	}
	if s.Items[0].Text != "clip-10" || s.Items[0].ID != midID {
		t.Fatalf("dedup did not move existing item to front keeping ID")
	}
	dup := 0
	for _, it := range s.Items {
		if it.Text == "clip-10" {
			dup++
		}
	}
	if dup != 1 {
		t.Fatalf("clip-10 appears %d times", dup)
	}
}

func TestTextLimitDoesNotEvictFiles(t *testing.T) {
	s, _ := newStore(t)
	f := filepath.Join(t.TempDir(), "keep-me.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{f}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	for i := range 35 {
		if err := s.AddText(fmt.Sprintf("t%d", i)); err != nil {
			t.Fatalf("AddText: %v", err)
		}
	}
	found := false
	for _, it := range s.Items {
		if it.Kind == KindFile && it.Path == f {
			found = true
		}
	}
	if !found {
		t.Fatal("file item was evicted by text limit")
	}
}

func TestAddFilesNormalizesAndDedups(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty := filepath.Join(dir, "sub", "..", "a.txt")
	if err := s.AddFiles([]string{dirty}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	want, _ := filepath.Abs(f)
	if s.Items[0].Path != want {
		t.Fatalf("path = %q, want normalized %q", s.Items[0].Path, want)
	}
	id := s.Items[0].ID
	// Same file via different spellings → same item moved to front.
	if err := s.AddText("separator"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{filepath.Join(dir, ".", "a.txt")}); err != nil {
		t.Fatalf("AddFiles dup: %v", err)
	}
	if len(s.Items) != 2 || s.Items[0].Kind != KindFile || s.Items[0].ID != id {
		t.Fatalf("dedup failed: %+v", s.Items[0])
	}
	// Never copies the original.
	if got := s.Items[0].Path; got != want {
		t.Fatalf("file item does not reference original: %q", got)
	}
}

func TestAddFilesOrderLastIsNewest(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	var paths []string
	for _, n := range []string{"1", "2", "3"} {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if err := s.AddFiles(paths); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if s.Items[0].Path != paths[2] || s.Items[2].Path != paths[0] {
		t.Fatalf("order = [%s %s %s]", s.Items[0].Path, s.Items[1].Path, s.Items[2].Path)
	}
}

func TestAddImageValidatesDedupsAndStores(t *testing.T) {
	s, root := newStore(t)
	pngA := makePNG(t, 4, 4, color.RGBA{R: 255, A: 255})
	pngB := makePNG(t, 4, 4, color.RGBA{G: 200, A: 255})

	if err := s.AddImage(pngA); err != nil {
		t.Fatalf("AddImage A: %v", err)
	}
	img := s.Items[0]
	if img.Kind != KindImage || img.Path == "" {
		t.Fatalf("bad image item: %+v", img)
	}
	data, err := os.ReadFile(img.Path)
	if err != nil || !bytes.Equal(data, pngA) {
		t.Fatalf("managed png bytes mismatch: err=%v", err)
	}
	if p := filePerm(t, img.Path); p != 0o600 {
		t.Fatalf("managed png perm = %o, want 600", p)
	}
	if filepath.Dir(img.Path) != filepath.Join(realRoot(t, root), imagesDirName) {
		t.Fatalf("image not under images dir: %q", img.Path)
	}

	// Identical bytes → dedup, move to front, no extra file.
	if err := s.AddText("x"); err != nil {
		t.Fatal(err)
	}
	idA := img.ID
	if err := s.AddImage(pngA); err != nil {
		t.Fatalf("AddImage A dup: %v", err)
	}
	if s.Items[0].ID != idA || s.Items[0].Kind != KindImage {
		t.Fatalf("image dedup failed: %+v", s.Items[0])
	}
	nImg := 0
	for _, it := range s.Items {
		if it.Kind == KindImage {
			nImg++
		}
	}
	if nImg != 1 {
		t.Fatalf("%d image items after dup add", nImg)
	}
	ents, _ := os.ReadDir(filepath.Join(root, imagesDirName))
	if len(ents) != 1 {
		t.Fatalf("%d files in images dir after dup add", len(ents))
	}

	if err := s.AddImage(pngB); err != nil {
		t.Fatalf("AddImage B: %v", err)
	}
	if len(s.Items) != 3 || s.Items[0].Kind != KindImage {
		t.Fatalf("second image not added at front: %v", s.Items)
	}
}

func TestAddImageRejectsBadInput(t *testing.T) {
	s, root := newStore(t)
	for name, blob := range map[string][]byte{
		"empty":       {},
		"not-an-img":  []byte("hello world, not an image"),
		"header-only": pngHeaderOnly(10, 10),     // 合法签名+IHDR，没有像素数据
		"huge-dim":    pngHeaderOnly(20000, 10),  // width > maxImageDim
		"huge-pixels": pngHeaderOnly(8192, 8193), // dims OK, ~67 MP > 64 MP limit
		"zero-width":  pngHeaderOnly(0, 100),
	} {
		if err := s.AddImage(blob); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
	// Nothing was persisted or written.
	if len(s.Items) != 0 {
		t.Fatalf("rejected images left %d items", len(s.Items))
	}
	ents, err := os.ReadDir(filepath.Join(root, imagesDirName))
	if err != nil || len(ents) != 0 {
		t.Fatalf("images dir not empty: ents=%v err=%v", ents, err)
	}
}

// 尺寸正常但数据截断或损坏的 PNG 不得被当作成功
// 图片保存；必须报错、不新增历史、不遗留图片文件。
func TestAddImageRejectsTruncatedAndCorruptData(t *testing.T) {
	full := makePNG(t, 8, 8, color.RGBA{R: 1, B: 2, A: 255})

	// 从尾部截断，直到完整解码失败（DecodeConfig 仍然通过）。
	var truncated []byte
	for cut := 1; cut < len(full); cut++ {
		cand := full[:len(full)-cut]
		if _, _, err := image.DecodeConfig(bytes.NewReader(cand)); err != nil {
			continue
		}
		if _, _, err := image.Decode(bytes.NewReader(cand)); err != nil {
			truncated = cand
			break
		}
	}
	if truncated == nil {
		t.Fatal("could not build a header-valid truncated png")
	}

	// 中间数据被破坏（尺寸与头部正常）的版本。
	var corrupt []byte
	for off := 40; off < len(full)-8; off++ {
		cand := bytes.Clone(full)
		cand[off] ^= 0xff
		if _, _, err := image.DecodeConfig(bytes.NewReader(cand)); err != nil {
			continue
		}
		if _, _, err := image.Decode(bytes.NewReader(cand)); err != nil {
			corrupt = cand
			break
		}
	}
	if corrupt == nil {
		t.Fatal("could not build a corrupt png")
	}

	s, root := newStore(t)
	for name, blob := range map[string][]byte{
		"truncated": truncated,
		"corrupt":   corrupt,
	} {
		if err := s.AddImage(blob); err == nil {
			t.Fatalf("%s: expected error, got nil", name)
		}
	}
	if len(s.Items) != 0 {
		t.Fatalf("rejected images left %d items", len(s.Items))
	}
	ents, err := os.ReadDir(filepath.Join(root, imagesDirName))
	if err != nil || len(ents) != 0 {
		t.Fatalf("images dir not empty: ents=%v err=%v", ents, err)
	}
	if _, err := os.Stat(filepath.Join(root, stateFileName)); !os.IsNotExist(err) {
		t.Fatalf("state.json created by rejected images: %v", err)
	}
}

func TestOpenCleansNonexistentKeepsErrors(t *testing.T) {
	s, root := newStore(t)
	dir := t.TempDir()
	okFile := filepath.Join(dir, "ok.txt")
	goneFile := filepath.Join(dir, "gone.txt")
	target := filepath.Join(dir, "missing-target")
	link := filepath.Join(dir, "dangling")
	lockedDir := filepath.Join(dir, "locked")
	lockedFile := filepath.Join(lockedDir, "secret.txt")

	os.WriteFile(okFile, []byte("o"), 0o600)
	os.WriteFile(goneFile, []byte("g"), 0o600)
	os.Symlink(target, link)
	os.Mkdir(lockedDir, 0o700)
	os.WriteFile(lockedFile, []byte("s"), 0o600)

	if err := s.AddFiles([]string{okFile, goneFile, link, lockedFile}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	os.Remove(goneFile)
	os.Chmod(lockedDir, 0o000)
	t.Cleanup(func() { os.Chmod(lockedDir, 0o700) })

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 3 {
		t.Fatalf("got %d items, want 3 (gone cleaned): %+v", len(s2.Items), s2.Items)
	}
	byPath := map[string]Item{}
	for _, it := range s2.Items {
		byPath[it.Path] = it
	}
	if it, ok := byPath[okFile]; !ok || it.Missing {
		t.Fatalf("ok file: %+v present=%v", it, ok)
	}
	if it, ok := byPath[link]; !ok || !it.Missing {
		t.Fatalf("dangling symlink should be kept Missing: %+v present=%v", it, ok)
	}
	if it, ok := byPath[lockedFile]; !ok || !it.Missing {
		t.Fatalf("unreadable file should be kept Missing: %+v present=%v", it, ok)
	}
	if _, ok := byPath[goneFile]; ok {
		t.Fatal("nonexistent file was not cleaned")
	}
}

func TestCorruptStateErrorsAndNeverOverwritten(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, imagesDirName), 0o700)
	bad := []byte("{ this is not json !!!")
	if err := os.WriteFile(filepath.Join(root, stateFileName), bad, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil {
		t.Fatal("expected error for corrupt state.json")
	}
	got, _ := os.ReadFile(filepath.Join(root, stateFileName))
	if !bytes.Equal(got, bad) {
		t.Fatal("corrupt state.json was overwritten")
	}
}

func TestUnsupportedVersionErrors(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, imagesDirName), 0o700)
	data, _ := json.Marshal(stateFile{Version: 99, Items: []Item{}})
	os.WriteFile(filepath.Join(root, stateFileName), data, 0o600)
	if _, err := Open(root); err == nil {
		t.Fatal("expected version error")
	}
}

func TestPersistenceRoundTrip(t *testing.T) {
	s, root := newStore(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.txt")
	os.WriteFile(f, []byte("d"), 0o600)
	s.AddImage(makePNG(t, 3, 3, color.RGBA{B: 7, A: 255}))
	s.AddText("persistent words")
	s.AddFiles([]string{f})

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != len(s.Items) {
		t.Fatalf("item count %d, want %d", len(s2.Items), len(s.Items))
	}
	for i := range s.Items {
		a, b := s.Items[i], s2.Items[i]
		if a.ID != b.ID || a.Kind != b.Kind || a.Path != b.Path || a.Text != b.Text {
			t.Fatalf("item %d changed across restart: %+v vs %+v", i, a, b)
		}
		if b.Missing {
			t.Fatalf("item %d wrongly Missing after restart", i)
		}
	}
	// Order is load order: file added last is first.
	if s2.Items[0].Kind != KindFile || s2.Items[1].Kind != KindText || s2.Items[2].Kind != KindImage {
		t.Fatalf("load order wrong: %v", s2.Items)
	}
}

func TestRefreshFlagsMissingWithoutRemoving(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "later-gone.txt")
	os.WriteFile(f, []byte("x"), 0o600)
	s.AddFiles([]string{f})
	s.Refresh()
	if s.Items[0].Missing {
		t.Fatal("existing file flagged Missing")
	}
	os.Remove(f)
	s.Refresh()
	if !s.Items[0].Missing || len(s.Items) != 1 {
		t.Fatalf("missing flag/removal wrong: %+v len=%d", s.Items[0], len(s.Items))
	}
	// File comes back → flag clears; text items are never stat'd.
	os.WriteFile(f, []byte("x"), 0o600)
	s.AddText("t")
	s.Refresh()
	if s.Items[1].Missing {
		t.Fatal("restored file still Missing")
	}
	if len(s.Items) != 2 {
		t.Fatal("Refresh removed an item")
	}
}

func TestRemove(t *testing.T) {
	s, root := newStore(t)
	dir := t.TempDir()
	original := filepath.Join(dir, "precious.txt")
	os.WriteFile(original, []byte("keep me"), 0o600)
	s.AddFiles([]string{original})
	fileID := s.Items[0].ID

	if err := s.Remove("no-such-id"); err == nil {
		t.Fatal("expected error for unknown id")
	}
	if err := s.Remove(fileID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if len(s.Items) != 0 {
		t.Fatal("item not removed")
	}
	// Original user file is never deleted.
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("original file deleted: %v", err)
	}
	// Removal persisted.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 0 {
		t.Fatal("removal not persisted")
	}
}

func TestRemoveManagedImageDeletesFile(t *testing.T) {
	s, _ := newStore(t)
	s.AddImage(makePNG(t, 2, 2, color.RGBA{A: 1}))
	managed := s.Items[0].Path
	id := s.Items[0].ID
	if err := s.Remove(id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("managed png still exists: %v", err)
	}
}

func TestRemoveManagedImageKeepsReferencedFile(t *testing.T) {
	s, _ := newStore(t)
	pngA := makePNG(t, 2, 2, color.RGBA{R: 9, A: 255})
	s.AddImage(pngA)
	managed := s.Items[0].Path
	imgID := s.Items[0].ID
	// A file-kind item ends up referencing the same path (e.g. user
	// dropped the managed file back in).
	if err := s.AddFiles([]string{managed}); err != nil {
		t.Fatalf("AddFiles managed path: %v", err)
	}
	if s.Items[0].Kind != KindFile || s.Items[0].Path != managed {
		t.Fatalf("file ref to managed path missing: %+v", s.Items[0])
	}
	if err := s.Remove(imgID); err != nil {
		t.Fatalf("Remove image: %v", err)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("referenced managed png wrongly deleted: %v", err)
	}
	// Remove the file ref too → next cleanup of an unreferenced
	// managed file would apply; the file itself stays until then.
	if err := s.Remove(s.Items[0].ID); err != nil {
		t.Fatalf("Remove file ref: %v", err)
	}
}

func TestRemoveNeverDeletesOutsideImages(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "not-ours.png")
	os.WriteFile(outside, []byte("x"), 0o600)
	// Crafted state: an image item pointing outside images/.
	sf := stateFile{Version: stateVersion, Items: []Item{{
		ID: "crafted", Kind: KindImage, Path: outside,
	}}}
	data, _ := json.Marshal(sf)
	os.MkdirAll(filepath.Join(root, imagesDirName), 0o700)
	os.WriteFile(filepath.Join(root, stateFileName), data, 0o600)

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if len(s.Items) != 1 {
		t.Fatalf("item dropped: %+v", s.Items)
	}
	if err := s.Remove("crafted"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was deleted: %v", err)
	}
}

// 根目录不可写导致 Save 失败时，AddImage 只能清理
// 本次新建且回滚后无人引用的文件；原本已有、尤其仍被文件引用指着的
// 托管 PNG 必须原样保留，否则既有引用失效、托盘图片丢失。
func TestAddImageSaveFailureKeepsExistingReferencedPng(t *testing.T) {
	s, root := newStore(t)
	pngBytes := makePNG(t, 3, 3, color.RGBA{R: 42, A: 255})
	if err := s.AddImage(pngBytes); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	imgID := s.Items[0].ID

	// 文件项引用同一托管 PNG，然后移除图片项：文件因仍被引用而保留。
	if err := s.AddFiles([]string{managed}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if err := s.Remove(imgID); err != nil {
		t.Fatalf("Remove image item: %v", err)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("precondition: managed png missing after Remove: %v", err)
	}
	stateBefore, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}

	// 根目录不可写（images 子目录仍可写）→ Save 失败。
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	if err := s.AddImage(pngBytes); err == nil {
		t.Fatal("expected save failure with read-only root")
	}
	// 原 PNG 必须还在且内容不变。
	got, err := os.ReadFile(managed)
	if err != nil || !bytes.Equal(got, pngBytes) {
		t.Fatalf("referenced png damaged: err=%v", err)
	}
	// 内存回滚：没有图片项，文件引用仍在。
	if len(s.Items) != 1 || s.Items[0].Kind != KindFile || s.Items[0].Path != managed {
		t.Fatalf("memory not rolled back: %+v", s.Items)
	}
	// 原历史 JSON 原样保留。
	stateAfter, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatalf("state.json changed on failed save: err=%v", err)
	}
}

// 已存在但当前无人引用的托管文件，失败清理同样不得删除：清理只区分
// “本次新建”与“原本已有”，与是否仍被引用无关。
func TestAddImageSaveFailureKeepsPreexistingOrphanFile(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pngBytes := makePNG(t, 2, 2, color.RGBA{G: 5, A: 255})
	sum := sha256.Sum256(pngBytes)
	orphan := filepath.Join(root, imagesDirName, fmt.Sprintf("%x.png", sum))
	if err := os.WriteFile(orphan, pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	if err := s.AddImage(pngBytes); err == nil {
		t.Fatal("expected save failure with read-only root")
	}
	if got, err := os.ReadFile(orphan); err != nil || !bytes.Equal(got, pngBytes) {
		t.Fatalf("pre-existing file deleted or changed: err=%v", err)
	}
	if len(s.Items) != 0 {
		t.Fatalf("items not rolled back: %+v", s.Items)
	}
}

// 本次新建、回滚后无人引用的文件仍按原设计清理，不留孤儿。
func TestAddImageSaveFailureCleansOnlyNewFile(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{B: 3, A: 255})); err == nil {
		t.Fatal("expected save failure with read-only root")
	}
	if len(s.Items) != 0 {
		t.Fatalf("items not rolled back: %+v", s.Items)
	}
	ents, err := os.ReadDir(filepath.Join(root, imagesDirName))
	if err != nil || len(ents) != 0 {
		t.Fatalf("new orphan not cleaned: ents=%v err=%v", ents, err)
	}
}

func TestSaveFailureRollsBackAndPreservesJSON(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("first"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	if err := s.AddText("second"); err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if len(s.Items) != 1 || s.Items[0].Text != "first" {
		t.Fatalf("memory not rolled back: %+v", s.Items)
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("previous state.json lost/corrupted on failed save")
	}
	// Remove also rolls back.
	if err := s.Remove(s.Items[0].ID); err == nil {
		t.Fatal("expected save error on Remove")
	}
	if len(s.Items) != 1 {
		t.Fatal("Remove memory not rolled back")
	}
}

func TestIDsUniqueAndStable(t *testing.T) {
	s, _ := newStore(t)
	for i := range 100 {
		if err := s.AddText(fmt.Sprintf("u%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, it := range s.Items {
		if it.ID == "" {
			t.Fatal("empty ID")
		}
		if seen[it.ID] {
			t.Fatalf("duplicate ID %q", it.ID)
		}
		seen[it.ID] = true
	}
}

func TestEmptyDirAndPermSetup(t *testing.T) {
	root := filepath.Join(t.TempDir(), "deep", "root")
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open nested root: %v", err)
	}
	if len(s.Items) != 0 {
		t.Fatal("fresh store not empty")
	}
	if p := filePerm(t, filepath.Join(root, imagesDirName)); p != 0o700 {
		t.Fatalf("images dir perm = %o, want 700", p)
	}
	if err := s.AddText("x"); err != nil {
		t.Fatal(err)
	}
	if p := filePerm(t, filepath.Join(root, stateFileName)); p != 0o600 {
		t.Fatalf("state.json perm = %o, want 600", p)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// 审查复现：.lock 是软链接时，打开数据目录既不跟随链接、也不截断它的
// 目标；正常的不存在 / 普通文件两种情况不受影响（上面的锁测试覆盖）。
func TestLockFileSymlinkNotFollowed(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "victim.txt")
	content := "precious\n"
	if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, lockFileName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(root); err == nil {
		s.Close()
		t.Fatal("Open followed a symlinked .lock")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != content {
		t.Fatalf("the symlink target was rewritten: %q, %v", got, err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the .lock symlink was disturbed: %v", err)
	}
}

// 同一数据目录只能被一个打开着的 Store 使用；
// 第二个实例必须明确失败而不是共享写入；不同目录互不影响；Close 或
// 进程退出后可以重新打开（flock 由内核在 fd 关闭时释放，无永久残留）。
func TestExclusiveLockSameProcess(t *testing.T) {
	root := t.TempDir()
	a, err := Open(root)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if _, err := Open(root); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open error = %v, want ErrLocked", err)
	}

	// 不同数据目录仍允许分别运行。
	b, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("different root: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close second root: %v", err)
	}

	// 释放后可以重新打开。
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	c, err := Open(root)
	if err != nil {
		t.Fatalf("reopen after Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}

// 审查复现：第二个实例写入 B 后重开只剩 B。锁生效后第二个实例必须
// 打不开目录，第一个实例的历史保持完整。
func TestLockedSecondInstanceCannotOverwrite(t *testing.T) {
	root := t.TempDir()
	first, err := Open(root)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.AddText("A"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	if _, err := Open(root); !errors.Is(err, ErrLocked) {
		t.Fatalf("second Open error = %v, want ErrLocked", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	re, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer re.Close()
	if len(re.Items) != 1 || re.Items[0].Text != "A" {
		t.Fatalf("first instance history lost: %+v", re.Items)
	}
}

// lockHelperEnv marks the re-executed test binary that only opens the
// store to hold the directory lock for TestExclusiveLockAcrossProcesses.
const lockHelperEnv = "RAKKO_TEST_LOCK_HELPER_ROOT"

func TestStoreLockHelperProcess(t *testing.T) {
	root := os.Getenv(lockHelperEnv)
	if root == "" {
		t.Skip("helper process for TestExclusiveLockAcrossProcesses")
	}
	s, err := Open(root)
	if err != nil {
		fmt.Printf("HELPER_OPEN_FAILED %v\n", err)
		return
	}
	fmt.Println("LOCKED")
	// 持有锁，直到父进程关闭 stdin。
	_, _ = io.Copy(io.Discard, os.Stdin)
	s.Close()
	fmt.Println("RELEASED")
}

// 真实多进程验证：子进程持有锁期间父进程打不开同一目录；子进程退出
// （包括被杀死）后锁自动释放，可以重新打开。
func TestExclusiveLockAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestStoreLockHelperProcess$")
	cmd.Env = append(os.Environ(), lockHelperEnv+"="+root)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting helper: %v", err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			stdin.Close()
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	scan := bufio.NewScanner(stdout)
	sawLocked := false
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if line == "LOCKED" {
			sawLocked = true
			break
		}
		if strings.HasPrefix(line, "HELPER_OPEN_FAILED") {
			t.Fatalf("helper could not open the store: %s", line)
		}
	}
	if !sawLocked {
		t.Fatalf("helper never reported LOCKED (err=%v)", scan.Err())
	}

	if _, err := Open(root); !errors.Is(err, ErrLocked) {
		t.Fatalf("Open while helper holds the lock = %v, want ErrLocked", err)
	}

	// 让 helper 正常退出释放锁。
	stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper exit: %v", err)
	}
	waited = true
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open after helper exit: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

// 在某个工作目录下用相对 root 写入图片后，换
// 工作目录再用绝对路径打开同一数据目录，图片项必须还在；托管图片以
// 相对 images 目录的文件名落盘。
func TestRelativeRootPersistsImagesAcrossCWDChange(t *testing.T) {
	parent := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(parent); err != nil {
		t.Fatal(err)
	}

	s, err := Open("data")
	if err != nil {
		t.Fatalf("Open relative: %v", err)
	}
	if !filepath.IsAbs(s.Root) {
		t.Fatalf("Root not normalized: %q", s.Root)
	}
	pngBytes := makePNG(t, 2, 2, color.RGBA{R: 7, A: 255})
	if err := s.AddImage(pngBytes); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	imgPath := s.Items[0].Path
	if !filepath.IsAbs(imgPath) {
		t.Fatalf("in-memory image path not absolute: %q", imgPath)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 落盘格式：图片路径相对 images 目录（仅文件名，不含工作目录）。
	data, err := os.ReadFile(filepath.Join(parent, "data", stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatal(err)
	}
	if len(sf.Items) != 1 || sf.Items[0].Kind != KindImage {
		t.Fatalf("persisted items: %+v", sf.Items)
	}
	if got := sf.Items[0].Path; got == "" || filepath.IsAbs(got) || got != filepath.Base(got) {
		t.Fatalf("image path not relative to images dir: %q", got)
	}

	// 换工作目录，用绝对路径重开同一数据目录：图片项必须恢复。
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(filepath.Join(parent, "data"))
	if err != nil {
		t.Fatalf("Open absolute: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 1 || s2.Items[0].Kind != KindImage || s2.Items[0].Missing {
		t.Fatalf("image item lost after cwd change: %+v", s2.Items)
	}
	got, err := os.ReadFile(s2.Items[0].Path)
	if err != nil || !bytes.Equal(got, pngBytes) {
		t.Fatalf("resolved image unreadable: err=%v", err)
	}
}

// 旧版 state.json 兼容：绝对路径与“按当时数据根拼出的相对路径”都必须
// 解析到当前根下的同一受管文件；迁移前保留 state.json.bak，下一次
// 保存改写为相对 images 目录的新格式，且不丢项。
func TestLegacyImagePathsResolveAndBackUp(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	pngA := makePNG(t, 2, 2, color.RGBA{G: 9, A: 255})
	pngB := makePNG(t, 2, 2, color.RGBA{B: 11, A: 255})
	sumA := sha256.Sum256(pngA)
	sumB := sha256.Sum256(pngB)
	nameA := fmt.Sprintf("%x.png", sumA)
	nameB := fmt.Sprintf("%x.png", sumB)
	if err := os.WriteFile(filepath.Join(root, imagesDirName, nameA), pngA, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, imagesDirName, nameB), pngB, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := stateFile{Version: stateVersion, Items: []Item{
		{
			ID: "legacy-relative", Kind: KindImage,
			// 旧版本在某工作目录下用相对 root 写入的路径。
			Path: filepath.Join("that-cwd", "data", imagesDirName, nameA),
		},
		{
			ID: "legacy-absolute", Kind: KindImage,
			// 现有数据里的绝对路径也要继续可用。
			Path: filepath.Join(root, imagesDirName, nameB),
		},
	}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if len(s.Items) != 2 {
		t.Fatalf("legacy items lost: %+v", s.Items)
	}
	byID := map[string]Item{}
	for _, it := range s.Items {
		byID[it.ID] = it
	}
	canonical := realRoot(t, root)
	if it := byID["legacy-relative"]; it.Missing || it.Path != filepath.Join(canonical, imagesDirName, nameA) {
		t.Fatalf("relative legacy path not resolved: %+v", it)
	}
	if it := byID["legacy-absolute"]; it.Missing || it.Path != filepath.Join(canonical, imagesDirName, nameB) {
		t.Fatalf("absolute legacy path not kept: %+v", it)
	}

	// 迁移前保留可恢复原件。
	bak, err := os.ReadFile(filepath.Join(root, stateFileName+".bak"))
	if err != nil || !bytes.Equal(bak, data) {
		t.Fatalf("legacy state backup missing/changed: err=%v", err)
	}

	// 下一次保存写入新格式（仅文件名），不丢任何一项。
	if err := s.AddText("migrate"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var sf stateFile
	if err := json.Unmarshal(reloaded, &sf); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range sf.Items {
		if it.Kind == KindImage {
			got[it.ID] = it.Path
		}
	}
	if got["legacy-relative"] != nameA || got["legacy-absolute"] != nameB {
		t.Fatalf("migrated image paths wrong: %+v", got)
	}
}

// 同一数据目录经软链接路径打开时，删除图片项
// 不得误删仍被文件引用指着的托管 PNG。
func TestSymlinkedRootRemoveKeepsReferencedPng(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pngBytes := makePNG(t, 3, 3, color.RGBA{R: 21, A: 255})
	if err := s.AddImage(pngBytes); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	imgID := s.Items[0].ID
	// 把托管 PNG 再加为普通文件引用。
	if err := s.AddFiles([]string{managed}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 经指向同一数据目录的软链接重新打开。
	link := symlinkTo(t, root)
	s2, err := Open(link)
	if err != nil {
		t.Fatalf("Open via symlink: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 2 {
		t.Fatalf("items after symlinked reopen: %+v", s2.Items)
	}

	if err := s2.Remove(imgID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// PNG 必须仍在（剩余文件引用仍指向它）。
	if got, err := os.ReadFile(managed); err != nil || !bytes.Equal(got, pngBytes) {
		t.Fatalf("referenced png deleted via symlinked root: err=%v", err)
	}
	s2.Refresh()
	if len(s2.Items) != 1 || s2.Items[0].Kind != KindFile || s2.Items[0].Missing {
		t.Fatalf("remaining file reference broken: %+v", s2.Items)
	}
}

// 旧版 state.json 存绝对图片路径：经目录软链接打开后再次 AddImage 同一张
// 图片，必须只保留一条图片项且 ID 唯一（旧行为会因路径拼写不同重复添加）。
func TestSymlinkedRootLegacyAbsoluteImageDedups(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	pngBytes := makePNG(t, 2, 2, color.RGBA{B: 33, A: 255})
	sum := sha256.Sum256(pngBytes)
	name := fmt.Sprintf("%x.png", sum)
	if err := os.WriteFile(filepath.Join(root, imagesDirName, name), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := stateFile{Version: stateVersion, Items: []Item{{
		ID: "legacy-img", Kind: KindImage,
		Path: filepath.Join(root, imagesDirName, name),
	}}}
	data, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	link := symlinkTo(t, root)
	s, err := Open(link)
	if err != nil {
		t.Fatalf("Open via symlink: %v", err)
	}
	defer s.Close()
	if len(s.Items) != 1 || s.Items[0].Missing {
		t.Fatalf("legacy image not resolved: %+v", s.Items)
	}

	if err := s.AddImage(pngBytes); err != nil {
		t.Fatalf("AddImage: %v", err)
	}

	ids := map[string]bool{}
	imgCount := 0
	for _, it := range s.Items {
		if ids[it.ID] {
			t.Fatalf("duplicate item id %q: %+v", it.ID, s.Items)
		}
		ids[it.ID] = true
		if it.Kind == KindImage {
			imgCount++
		}
	}
	if imgCount != 1 || len(s.Items) != 1 {
		t.Fatalf("image not deduplicated across symlinked root: %+v", s.Items)
	}
	ents, err := os.ReadDir(filepath.Join(root, imagesDirName))
	if err != nil || len(ents) != 1 {
		t.Fatalf("images dir has %d files (err=%v)", len(ents), err)
	}
}

// 确实无人引用的托管图片，经软链接打开时仍应被正常清理。
func TestSymlinkedRootRemoveUnreferencedStillCleans(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{G: 44, A: 255})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	imgID := s.Items[0].ID
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	link := symlinkTo(t, root)
	s2, err := Open(link)
	if err != nil {
		t.Fatalf("Open via symlink: %v", err)
	}
	defer s2.Close()
	if err := s2.Remove(imgID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(managed); !os.IsNotExist(err) {
		t.Fatalf("unreferenced managed png not cleaned: %v", err)
	}
}

// 删除保护按实际文件身份判断：文件引用用软链接拼写指向同一张 PNG 时也
// 不得删除（不能只比较路径字符串）。
func TestRemoveKeepsPngReferencedViaSymlinkSpelling(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 55, G: 5, A: 255})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	imgID := s.Items[0].ID

	link := symlinkTo(t, root)
	spelled := filepath.Join(link, imagesDirName, filepath.Base(managed))
	if err := s.AddFiles([]string{spelled}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if err := s.Remove(imgID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("png referenced via symlink spelling deleted: %v", err)
	}
	s.Refresh()
	found := false
	for _, it := range s.Items {
		if it.Kind == KindFile {
			found = true
			if it.Missing {
				t.Fatalf("file reference broken: %+v", it)
			}
		}
	}
	if !found {
		t.Fatalf("file reference lost: %+v", s.Items)
	}
}

// 删除保护缺口（本轮）：检查剩余项目时，引用项一侧的 Stat 因权限/IO
// 错误失败，属于“无法确认是否仍被引用”，必须保守保留目标 PNG，不能
// 进入删除分支；正常清理行为不受影响。
func TestRemoveKeepsPngWhenReferenceUnreadable(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	pngKeep := makePNG(t, 3, 3, color.RGBA{R: 91, A: 255})
	if err := s.AddImage(pngKeep); err != nil {
		t.Fatalf("AddImage keep: %v", err)
	}
	keepPath := s.Items[0].Path
	keepID := s.Items[0].ID
	pngClean := makePNG(t, 2, 2, color.RGBA{G: 92, A: 255})
	if err := s.AddImage(pngClean); err != nil {
		t.Fatalf("AddImage clean: %v", err)
	}
	cleanPath := s.Items[0].Path
	cleanID := s.Items[0].ID

	// 经软链接拼写把 keep PNG 加入为普通文件引用。
	linkParent := t.TempDir()
	link := filepath.Join(linkParent, "data-link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	spelled := filepath.Join(link, imagesDirName, filepath.Base(keepPath))
	if err := s.AddFiles([]string{spelled}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}

	// 让引用路径的 Stat 报权限错误：父目录 000，托管 PNG 原路径仍可读。
	if err := os.Chmod(linkParent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(linkParent, 0o700) })
	if _, err := os.Stat(spelled); !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("precondition: stat(%q) = %v, want permission error", spelled, err)
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("precondition: managed png unreadable: %v", err)
	}

	if err := s.Remove(keepID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// PNG 字节必须完整，文件引用必须仍在。
	got, err := os.ReadFile(keepPath)
	if err != nil || !bytes.Equal(got, pngKeep) {
		t.Fatalf("png deleted or damaged while a reference was unreadable: err=%v", err)
	}
	fileRefs := 0
	for _, it := range s.Items {
		if it.Kind == KindFile {
			fileRefs++
		}
	}
	if fileRefs != 1 {
		t.Fatalf("file reference lost: %+v", s.Items)
	}

	// 恢复权限后引用应有效（Refresh 不再 Missing）。
	if err := os.Chmod(linkParent, 0o700); err != nil {
		t.Fatal(err)
	}
	s.Refresh()
	for _, it := range s.Items {
		if it.Kind == KindFile && it.Missing {
			t.Fatalf("reference still missing after permissions restored: %+v", it)
		}
	}

	// 正常清理不受影响：无引用的图片项删除后文件应被清理。
	if err := s.Remove(cleanID); err != nil {
		t.Fatalf("Remove clean: %v", err)
	}
	if _, err := os.Stat(cleanPath); !os.IsNotExist(err) {
		t.Fatalf("unreferenced managed png not cleaned: %v", err)
	}
	if _, err := os.Stat(keepPath); err != nil {
		t.Fatalf("kept png vanished: %v", err)
	}
}

// writeLegacyStateWithImage prepares a root whose state.json uses the old
// absolute image path format (triggering migration on Open); it returns
// the original state.json bytes and the managed image file name.
func writeLegacyStateWithImage(t *testing.T, root string, seed color.RGBA) ([]byte, string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	pngBytes := makePNG(t, 2, 2, seed)
	sum := sha256.Sum256(pngBytes)
	name := fmt.Sprintf("%x.png", sum)
	if err := os.WriteFile(filepath.Join(root, imagesDirName, name), pngBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	sf := stateFile{Version: stateVersion, Items: []Item{
		{ID: "legacy-img", Kind: KindImage, Path: filepath.Join(root, imagesDirName, name)},
		{ID: "t-keep", Kind: KindText, Text: "保留的老文字"},
	}}
	data, err := json.Marshal(sf)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data, name
}

// 常规备份名被目录占用时，应改用独立的新
// 备份文件保留本次迁移前的原件，再继续迁移；绝不能在无恢复点的情况下
// 覆盖原件，也不得覆盖占位者。
func TestMigrationBackupUsesAlternateWhenBakOccupied(t *testing.T) {
	root := t.TempDir()
	original, _ := writeLegacyStateWithImage(t, root, color.RGBA{R: 61, A: 255})
	if err := os.Mkdir(filepath.Join(root, stateFileName+".bak"), 0o700); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if s.MigrationError() != nil {
		t.Fatalf("alternate backup should be a usable recovery point: %v", s.MigrationError())
	}
	if err := s.AddText("新文字"); err != nil {
		t.Fatalf("AddText with alternate backup should succeed: %v", err)
	}

	alt := filepath.Join(root, stateFileName+".bak-2")
	if got, err := os.ReadFile(alt); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("alternate backup missing or changed: err=%v", err)
	}
	if fi, err := os.Stat(filepath.Join(root, stateFileName+".bak")); err != nil || !fi.IsDir() {
		t.Fatalf("occupied path overwritten: err=%v", err)
	}
	// 迁移不丢项：老文字与图片项都还在，新文字在最前。
	if len(s.Items) != 3 || s.Items[0].Kind != KindText || s.Items[0].Text != "新文字" {
		t.Fatalf("items after migration: %+v", s.Items)
	}
	for _, it := range s.Items {
		if it.ID == "legacy-img" && (it.Missing || !filepath.IsAbs(it.Path)) {
			t.Fatalf("legacy image lost in migration: %+v", it)
		}
	}
}

// 备份创建彻底失败（全部候选名不可用）时：Open 不报致命错误但停止写入，
// 原件字节不变、不伪装成功；恢复正常条件后重新打开可以安全继续，
// 备份内容完整且迁移不丢项。
func TestMigrationWithoutAnyBackupBlocksWrites(t *testing.T) {
	root := t.TempDir()
	original, _ := writeLegacyStateWithImage(t, root, color.RGBA{G: 71, A: 255})
	for _, name := range migrationBackupNames {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s.MigrationError() == nil {
		t.Fatal("expected migration blocked without any backup")
	}
	if err := s.AddText("写不进去"); err == nil {
		t.Fatal("AddText must fail without a recovery point")
	}
	for _, it := range s.Items {
		if it.Kind == KindText && it.Text == "写不进去" {
			t.Fatalf("failed write leaked into memory: %+v", s.Items)
		}
	}
	got, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("original state.json changed without recovery point: err=%v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 恢复正常条件后可以安全继续。
	for _, name := range migrationBackupNames {
		if err := os.RemoveAll(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen after recovery: %v", err)
	}
	defer s2.Close()
	if s2.MigrationError() != nil {
		t.Fatalf("still blocked after recovery: %v", s2.MigrationError())
	}
	if err := s2.AddText("恢复后写入"); err != nil {
		t.Fatalf("AddText after recovery: %v", err)
	}
	bak, err := os.ReadFile(filepath.Join(root, stateFileName+".bak"))
	if err != nil || !bytes.Equal(bak, original) {
		t.Fatalf("backup after recovery missing/changed: err=%v", err)
	}
	// 无半成品后备文件残留。
	if _, err := os.Stat(filepath.Join(root, stateFileName+".bak-2")); !os.IsNotExist(err) {
		t.Fatalf("leftover alternate backup: %v", err)
	}
	reloaded, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var sf stateFile
	if err := json.Unmarshal(reloaded, &sf); err != nil {
		t.Fatal(err)
	}
	if len(sf.Items) != 3 {
		t.Fatalf("items after recovered migration: %+v", sf.Items)
	}
}

// 已有的 .bak 与本次待迁移原件不一致（不完整/损坏）时不视为有效恢复点：
// 不覆盖它、也不复用它，另建独立备份保存本次原件。
func TestMigrationKeepsExistingIncompleteBackup(t *testing.T) {
	root := t.TempDir()
	original, _ := writeLegacyStateWithImage(t, root, color.RGBA{B: 81, A: 255})
	incomplete := bytes.Clone(original[:len(original)/2])
	if err := os.WriteFile(filepath.Join(root, stateFileName+".bak"), incomplete, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if err := s.AddText("继续迁移"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, stateFileName+".bak")); err != nil || !bytes.Equal(got, incomplete) {
		t.Fatalf("existing backup overwritten: err=%v", err)
	}
	if got, err := os.ReadFile(filepath.Join(root, stateFileName+".bak-2")); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("independent backup missing/changed: err=%v", err)
	}
	if len(s.Items) != 3 {
		t.Fatalf("items after migration: %+v", s.Items)
	}
}

// 受管 PNG 被改写为其他有效 PNG 或被截断后，内容寻址文件名不变但字节
// 已损坏：再次添加同一张图片（命中去重分支）必须核对现有文件字节，发现
// 不一致时原子恢复为本次验证过的 PNG，而不是因“文件存在”直接成功。
// 恢复后去重仍只有一条图片项、文件持久化字节与可解码内容均与原图一致。
func TestAddImageRestoresTamperedManagedFile(t *testing.T) {
	s, root := newStore(t)
	pngA := makePNG(t, 4, 4, color.RGBA{R: 9, B: 7, A: 255})
	if err := s.AddImage(pngA); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	imgID := s.Items[0].ID
	managed := s.Items[0].Path

	// 去重命中会把既有项移回最前：插入一个文本项来确认顺序与去重。
	if err := s.AddText("marker"); err != nil {
		t.Fatalf("AddText: %v", err)
	}

	cases := map[string][]byte{
		"different-valid-png": makePNG(t, 4, 4, color.RGBA{G: 111, A: 255}),
		"truncated":           pngA[:len(pngA)/2],
	}
	for name, wrong := range cases {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(managed, wrong, 0o600); err != nil {
				t.Fatalf("tamper managed file: %v", err)
			}
			if err := s.AddImage(pngA); err != nil {
				t.Fatalf("AddImage over tampered file: %v", err)
			}
			if len(s.Items) != 2 || s.Items[0].ID != imgID || s.Items[0].Kind != KindImage ||
				s.Items[0].Path != managed || s.Items[0].Missing {
				t.Fatalf("dedup/order broken after restore: %+v", s.Items)
			}
			got, err := os.ReadFile(managed)
			if err != nil {
				t.Fatalf("read restored png: %v", err)
			}
			if !bytes.Equal(got, pngA) {
				t.Fatalf("restored bytes mismatch: got %d bytes, want %d", len(got), len(pngA))
			}
			dec, err := png.Decode(bytes.NewReader(got))
			if err != nil {
				t.Fatalf("restored file not decodable: %v", err)
			}
			if b := dec.Bounds(); b.Dx() != 4 || b.Dy() != 4 {
				t.Fatalf("restored png bounds %v, want 4x4", b)
			}
		})
	}

	// 原子恢复不得留下临时文件，images 目录仍只有这一个受管文件。
	ents, err := os.ReadDir(filepath.Join(root, imagesDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 || ents[0].Name() != filepath.Base(managed) {
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("stray files in images dir: %v", names)
	}
}

// Clear 清空全部项目并持久化：内存立即为空，重开 state.json 也是空的；
// 受管 PNG 随清空删除，文件引用指向的原文件与 images/ 里的外来文件都
// 原样保留（KindFile 的路径绝不删除）。
func TestClearRemovesEverythingKeepsUserFiles(t *testing.T) {
	s, root := newStore(t)
	dir := t.TempDir()
	original := filepath.Join(dir, "precious.txt")
	if err := os.WriteFile(original, []byte("keep me"), 0o600); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(realRoot(t, root), imagesDirName, "stray.txt")
	if err := os.WriteFile(stray, []byte("not a managed image"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{original, stray}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if err := s.AddText("some text"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{A: 9})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	if err := s.AddImage(makePNG(t, 3, 3, color.RGBA{B: 7, A: 9})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	if len(s.Items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(s.Items))
	}
	managed := []string{}
	for _, it := range s.Items {
		if it.Kind == KindImage {
			managed = append(managed, it.Path)
		}
	}
	if len(managed) != 2 {
		t.Fatalf("expected 2 managed images, got %d", len(managed))
	}
	// 同一受管路径被两条图片项引用时只删一次（去重路径），结果同样完整。
	dup := s.Items[0]
	dup.ID = newID()
	s.Items = append(s.Items, dup)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if len(s.Items) != 0 {
		t.Fatalf("items not cleared: %+v", s.Items)
	}
	for _, p := range managed {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("managed png still exists %s: %v", p, err)
		}
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("original user file deleted: %v", err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("file item inside images dir deleted: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 0 {
		t.Fatalf("clear not persisted: %+v", s2.Items)
	}
}

// Save 失败（根目录只读）时 Clear 回滚内存、不动磁盘：项目原样恢复，
// state.json 字节不变，受管 PNG 一个都不删。
func TestClearSaveFailureRollsBack(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("first"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{A: 9})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(s.Items))
	for _, it := range s.Items {
		ids = append(ids, it.ID)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	if err := s.Clear(); err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if len(s.Items) != len(ids) {
		t.Fatalf("memory not rolled back: %+v", s.Items)
	}
	for i, it := range s.Items {
		if it.ID != ids[i] {
			t.Fatalf("item order changed on rollback: %+v", s.Items)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("previous state.json lost/corrupted on failed clear")
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png deleted on failed clear: %v", err)
	}
}

// 空托盘上 Clear 直接成功返回，不产生任何写入，失败渠道（如迁移阻断）
// 也不被触发。
func TestClearEmptyIsNoOp(t *testing.T) {
	s, root := newStore(t)
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear on empty store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, stateFileName)); !os.IsNotExist(err) {
		t.Fatalf("empty Clear wrote state.json: %v", err)
	}
}

// RemoveOlderThan keeps items at the cutoff or newer and drops the rest:
// managed images that survive are only the ones a kept item still
// references, user files (including KindFile items inside images/) are
// never touched, and the surviving order is persisted.
func TestRemoveOlderThanMixedKeepsCutoffAndReferences(t *testing.T) {
	s, root := newStore(t)
	dir := t.TempDir()
	oldUser := filepath.Join(dir, "old-user.txt")
	if err := os.WriteFile(oldUser, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	newUser := filepath.Join(dir, "new-user.txt")
	if err := os.WriteFile(newUser, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 1, A: 255})); err != nil {
		t.Fatalf("AddImage A: %v", err)
	}
	managedA, imgAID := s.Items[0].Path, s.Items[0].ID
	// A file-kind item ends up referencing the managed file (user dropped
	// it back in): the image item may go, the file must stay.
	if err := s.AddFiles([]string{managedA}); err != nil {
		t.Fatalf("AddFiles refA: %v", err)
	}
	refAID := s.Items[0].ID
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 2, A: 255})); err != nil {
		t.Fatalf("AddImage B: %v", err)
	}
	managedB, imgBID := s.Items[0].Path, s.Items[0].ID
	// References among removed items do not count: this image goes with
	// its own file-kind reference.
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 3, A: 255})); err != nil {
		t.Fatalf("AddImage C: %v", err)
	}
	managedC, imgCID := s.Items[0].Path, s.Items[0].ID
	if err := s.AddFiles([]string{managedC}); err != nil {
		t.Fatalf("AddFiles refC: %v", err)
	}
	refCID := s.Items[0].ID
	// An image item whose file is already gone: removal must not report
	// IsNotExist as a cleanup failure.
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 4, A: 255})); err != nil {
		t.Fatalf("AddImage D: %v", err)
	}
	imgDID := s.Items[0].ID
	if err := os.Remove(s.Items[0].Path); err != nil {
		t.Fatalf("setup remove: %v", err)
	}
	s.Items[0].Missing = true
	if err := s.AddFiles([]string{oldUser, newUser}); err != nil {
		t.Fatalf("AddFiles users: %v", err)
	}
	newUserID, oldUserID := s.Items[0].ID, s.Items[1].ID
	if err := s.AddText("old text"); err != nil {
		t.Fatalf("AddText old: %v", err)
	}
	oldTextID := s.Items[0].ID
	if err := s.AddText("new text"); err != nil {
		t.Fatalf("AddText new: %v", err)
	}
	newTextID := s.Items[0].ID
	// A KindFile item pointing inside images/ is never a deletion target.
	stray := filepath.Join(realRoot(t, root), imagesDirName, "stray.txt")
	if err := os.WriteFile(stray, []byte("stray"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{stray}); err != nil {
		t.Fatalf("AddFiles stray: %v", err)
	}
	strayID := s.Items[0].ID
	if len(s.Items) != 11 {
		t.Fatalf("setup items: %d", len(s.Items))
	}

	cutoff := time.Now()
	old := cutoff.Add(-time.Hour)
	newish := cutoff.Add(time.Hour)
	setAge(t, s, oldUserID, old)    // removed; its file must stay
	setAge(t, s, newUserID, cutoff) // exactly at cutoff → kept
	setAge(t, s, oldTextID, old)
	setAge(t, s, newTextID, newish)
	setAge(t, s, imgAID, old)    // removed; file kept via refA
	setAge(t, s, refAID, newish) // kept → managedA survives
	setAge(t, s, imgBID, newish) // kept with its file
	setAge(t, s, imgCID, old)    // removed; refC gone too → file deleted
	setAge(t, s, refCID, old)
	setAge(t, s, imgDID, old)
	setAge(t, s, strayID, old) // removed; stray file must stay

	n, err := s.RemoveOlderThan(cutoff)
	if err != nil {
		t.Fatalf("RemoveOlderThan: %v", err)
	}
	if n != 7 {
		t.Fatalf("removed %d, want 7 (items: %+v)", n, s.Items)
	}
	// Kept items preserve their relative order.
	if len(s.Items) != 4 {
		t.Fatalf("kept %d items: %+v", len(s.Items), s.Items)
	}
	wantIDs := []string{newTextID, newUserID, imgBID, refAID}
	for i, id := range wantIDs {
		if s.Items[i].ID != id {
			t.Fatalf("kept[%d] = %+v, want id %s", i, s.Items[i], id)
		}
	}
	if _, err := os.Stat(managedA); err != nil {
		t.Fatalf("referenced managed image deleted: %v", err)
	}
	if _, err := os.Stat(managedB); err != nil {
		t.Fatalf("kept item's image deleted: %v", err)
	}
	if _, err := os.Stat(managedC); !os.IsNotExist(err) {
		t.Fatalf("unreferenced managed image kept: %v", err)
	}
	if _, err := os.Stat(oldUser); err != nil {
		t.Fatalf("removed item's user file deleted: %v", err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("file item inside images dir deleted: %v", err)
	}
	// Persisted: reopen shows exactly the kept items.
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 4 {
		t.Fatalf("removal not persisted: %+v", s2.Items)
	}
}

// With no items older than the cutoff RemoveOlderThan is a no-op: no
// write to state.json (bytes and mtime unchanged — it even succeeds on a
// read-only root), and (0, nil) both ways.
func TestRemoveOlderThanNoMatchNoWrite(t *testing.T) {
	es, emptyRoot := newStore(t)
	n, err := es.RemoveOlderThan(time.Now().Add(time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("empty store: n=%d err=%v", n, err)
	}
	if _, err := os.Stat(filepath.Join(emptyRoot, stateFileName)); !os.IsNotExist(err) {
		t.Fatalf("empty no-op wrote state.json: %v", err)
	}

	s, root := newStore(t)
	if err := s.AddText("recent"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	statePath := filepath.Join(root, stateFileName)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	fiBefore, err := os.Stat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	n, err = s.RemoveOlderThan(time.Now().Add(-time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("no-match: n=%d err=%v", n, err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("state.json bytes changed on no-op: %v", err)
	}
	fiAfter, err := os.Stat(statePath)
	if err != nil || !fiAfter.ModTime().Equal(fiBefore.ModTime()) {
		t.Fatalf("state.json rewritten on no-op")
	}
	// A write here would fail on the read-only root; (0, nil) proves none.
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	n, err = s.RemoveOlderThan(time.Now().Add(-time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("no-match on read-only root: n=%d err=%v", n, err)
	}
	if len(s.Items) != 1 {
		t.Fatalf("items changed: %+v", s.Items)
	}
}

// Save failure (read-only root) rolls memory back exactly and deletes
// nothing: state.json bytes and the managed PNG survive untouched.
func TestRemoveOlderThanSaveFailureRollsBack(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("old text"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{A: 9})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	ids := itemIDs(s)
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	n, err := s.RemoveOlderThan(time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if n != 0 {
		t.Fatalf("save failed but reported %d removed", n)
	}
	got := itemIDs(s)
	if len(got) != len(ids) {
		t.Fatalf("memory not rolled back: %+v", s.Items)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("item order changed on rollback: %+v", s.Items)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json changed on failed save")
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png deleted on failed save: %v", err)
	}
}

// A store whose migration has no recovery point rejects the removal
// before touching anything: error, memory and state.json unchanged.
func TestRemoveOlderThanMigrationBlocked(t *testing.T) {
	root := t.TempDir()
	original, name := writeLegacyStateWithImage(t, root, color.RGBA{B: 41, A: 255})
	for _, n := range migrationBackupNames {
		if err := os.Mkdir(filepath.Join(root, n), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if s.MigrationError() == nil {
		t.Fatal("expected migration blocked")
	}
	idsBefore := itemIDs(s)
	n, err := s.RemoveOlderThan(time.Now().Add(24 * time.Hour))
	if err == nil {
		t.Fatal("RemoveOlderThan must fail without a recovery point")
	}
	if n != 0 {
		t.Fatalf("failed removal reported %d removed", n)
	}
	got := itemIDs(s)
	if len(got) != len(idsBefore) {
		t.Fatalf("items changed on blocked store: %+v", s.Items)
	}
	for i := range got {
		if got[i] != idsBefore[i] {
			t.Fatalf("items changed on blocked store: %+v", s.Items)
		}
	}
	state, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(state, original) {
		t.Fatalf("original state.json changed without recovery point: %v", err)
	}
	img := filepath.Join(realRoot(t, root), imagesDirName, name)
	if _, err := os.Stat(img); err != nil {
		t.Fatalf("managed image deleted on blocked store: %v", err)
	}
}

// A cleanup error is reported together with the removal count: items are
// gone and persisted, the failing file still exists.
func TestRemoveOlderThanImageCleanupError(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 99, A: 255})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	if err := os.Chmod(filepath.Join(root, imagesDirName), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, imagesDirName), 0o755) })

	n, err := s.RemoveOlderThan(time.Now().Add(time.Hour))
	if err == nil {
		t.Fatal("expected cleanup error with read-only images dir")
	}
	if n != 1 {
		t.Fatalf("removed count = %d, want 1", n)
	}
	if !strings.Contains(err.Error(), "deleting managed file") {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(s.Items) != 0 {
		t.Fatalf("items not removed: %+v", s.Items)
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("file gone despite failed deletion: %v", err)
	}
}

// RemoveMany dedupes and ignores unknown IDs: a managed image referenced
// by a kept file item survives, an unreferenced one is deleted once.
func TestRemoveMany(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 7, A: 255})); err != nil {
		t.Fatalf("AddImage 1: %v", err)
	}
	img1Path, img1ID := s.Items[0].Path, s.Items[0].ID
	if err := s.AddFiles([]string{img1Path}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{R: 8, A: 255})); err != nil {
		t.Fatalf("AddImage 2: %v", err)
	}
	img2Path, img2ID := s.Items[0].Path, s.Items[0].ID
	if err := s.AddText("keep"); err != nil {
		t.Fatalf("AddText: %v", err)
	}

	// No matching IDs → no write, even on a read-only root.
	statePath := filepath.Join(root, stateFileName)
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	n, err := s.RemoveMany([]string{"nope", ""})
	if err != nil || n != 0 {
		t.Fatalf("no-match: n=%d err=%v", n, err)
	}
	n, err = s.RemoveMany(nil)
	if err != nil || n != 0 {
		t.Fatalf("nil ids: n=%d err=%v", n, err)
	}
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json written for a no-op")
	}
	if len(s.Items) != 4 {
		t.Fatalf("items changed by no-op: %+v", s.Items)
	}

	// Duplicate IDs count once; unknown ones are ignored.
	n, err = s.RemoveMany([]string{img1ID, img1ID, "no-such-id", img2ID})
	if err != nil {
		t.Fatalf("RemoveMany: %v", err)
	}
	if n != 2 {
		t.Fatalf("removed %d, want 2", n)
	}
	if len(s.Items) != 2 {
		t.Fatalf("items after removal: %+v", s.Items)
	}
	for _, it := range s.Items {
		if it.ID == img1ID || it.ID == img2ID {
			t.Fatalf("removed id still present: %+v", it)
		}
	}
	if _, err := os.Stat(img1Path); err != nil {
		t.Fatalf("still-referenced managed image deleted: %v", err)
	}
	if _, err := os.Stat(img2Path); !os.IsNotExist(err) {
		t.Fatalf("unreferenced managed image kept: %v", err)
	}
}

// Save failure (read-only root) rolls RemoveMany back with nothing
// deleted: all items and the managed PNG survive.
func TestRemoveManySaveFailureRollsBack(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("one"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{B: 5, A: 255})); err != nil {
		t.Fatalf("AddImage: %v", err)
	}
	managed := s.Items[0].Path
	ids := itemIDs(s)
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	n, err := s.RemoveMany(ids)
	if err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if n != 0 {
		t.Fatalf("save failed but reported %d removed", n)
	}
	got := itemIDs(s)
	if len(got) != len(ids) {
		t.Fatalf("memory not rolled back: %+v", s.Items)
	}
	for i := range ids {
		if got[i] != ids[i] {
			t.Fatalf("item order changed on rollback: %+v", s.Items)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json changed on failed save")
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png deleted on failed save: %v", err)
	}
}

// findItem locates an item by its text; fails the test when absent.
func findItem(t *testing.T, s *Store, text string) Item {
	t.Helper()
	for _, it := range s.Items {
		if it.Kind == KindText && it.Text == text {
			return it
		}
	}
	t.Fatalf("no text item %q in %+v", text, s.Items)
	return Item{}
}

// hasText reports whether a text item with the given content exists.
func hasText(s *Store, text string) bool {
	for _, it := range s.Items {
		if it.Kind == KindText && it.Text == text {
			return true
		}
	}
	return false
}

// 旧版 state.json 没有 pinned/source/sourceApp 字段：加载原样生效，
// 新增字段取零值（未置顶、无来源）。
func TestLegacyStateWithoutNewFieldsLoads(t *testing.T) {
	root := t.TempDir()
	legacy := `{"version":1,"items":[{"id":"a1","kind":"text","text":"old entry","createdAt":"2024-01-02T03:04:05Z"}]}`
	if err := os.WriteFile(filepath.Join(root, stateFileName), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if len(s.Items) != 1 || s.Items[0].Text != "old entry" || s.Items[0].ID != "a1" {
		t.Fatalf("legacy item not loaded: %+v", s.Items)
	}
	if s.Items[0].Pinned || s.Items[0].Source != "" || s.Items[0].SourceApp != "" {
		t.Fatalf("new fields not zero-valued on legacy item: %+v", s.Items[0])
	}
	// New fields must survive a save/reopen round trip too.
	if err := s.SetPinned("a1", true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 1 || !s2.Items[0].Pinned {
		t.Fatalf("pinned not persisted: %+v", s2.Items)
	}
}

// 置顶文字不计入也不被上限驱逐：35 条文字里 1 条置顶，
// 最终保留该置顶项 + 30 条最新未置顶文字。
func TestPinnedTextSurvivesLimitEviction(t *testing.T) {
	s, _ := newStore(t)
	for i := range 30 {
		if err := s.AddText(fmt.Sprintf("t%02d", i)); err != nil {
			t.Fatalf("AddText %d: %v", i, err)
		}
	}
	if err := s.SetPinned(findItem(t, s, "t00").ID, true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	for i := 30; i < 35; i++ {
		if err := s.AddText(fmt.Sprintf("t%02d", i)); err != nil {
			t.Fatalf("AddText %d: %v", i, err)
		}
	}
	if len(s.Items) != 31 {
		t.Fatalf("want 31 items (30 unpinned + 1 pinned), got %d", len(s.Items))
	}
	pinned := findItem(t, s, "t00")
	if !pinned.Pinned {
		t.Fatal("t00 lost its pin")
	}
	if hasText(s, "t04") {
		t.Fatal("t04 should have been evicted")
	}
	for i := 5; i < 35; i++ {
		if !hasText(s, fmt.Sprintf("t%02d", i)) {
			t.Fatalf("t%02d missing", i)
		}
	}
}

// SetTextLimit 改变下一次添加时保留的未置顶文字数。
func TestSetTextLimit(t *testing.T) {
	s, _ := newStore(t)
	s.SetTextLimit(100)
	for i := range 105 {
		if err := s.AddText(fmt.Sprintf("u%d", i)); err != nil {
			t.Fatalf("AddText %d: %v", i, err)
		}
	}
	if len(s.Items) != 100 {
		t.Fatalf("limit 100 kept %d items", len(s.Items))
	}
	// n < 1 restores the default of 30.
	s.SetTextLimit(0)
	for i := range 5 {
		if err := s.AddText(fmt.Sprintf("v%d", i)); err != nil {
			t.Fatalf("AddText v%d: %v", i, err)
		}
	}
	texts := 0
	for _, it := range s.Items {
		if it.Kind == KindText {
			texts++
		}
	}
	if texts != 30 {
		t.Fatalf("SetTextLimit(0) kept %d texts, want 30", texts)
	}
	// 不改变既有条目：仅在下一次 add 时应用。上面已覆盖；再验证
	// SetTextLimit 本身不裁掉已超限的旧条目。
	s2, _ := newStore(t)
	for i := range 35 {
		if err := s2.AddText(fmt.Sprintf("w%d", i)); err != nil {
			t.Fatalf("AddText w%d: %v", i, err)
		}
	}
	s2.SetTextLimit(10)
	if len(s2.Items) != 30 {
		t.Fatalf("SetTextLimit trimmed existing items: %d", len(s2.Items))
	}
	if err := s2.AddText("fresh"); err != nil {
		t.Fatal(err)
	}
	texts = 0
	for _, it := range s2.Items {
		if it.Kind == KindText {
			texts++
		}
	}
	if texts != 10 {
		t.Fatalf("after next add want 10 texts, got %d", texts)
	}
}

// 重新复制已置顶的文字：去重后仍在原位（移到最前）、保留置顶标记与 ID。
func TestDedupKeepsPinnedAndMovesToFront(t *testing.T) {
	s, _ := newStore(t)
	if err := s.AddText("keep me"); err != nil {
		t.Fatal(err)
	}
	id := s.Items[0].ID
	if err := s.AddText("other"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(id, true); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("keep me"); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 2 {
		t.Fatalf("dedup duplicated: %+v", s.Items)
	}
	if s.Items[0].ID != id || !s.Items[0].Pinned {
		t.Fatalf("pinned dedup lost id/pin: %+v", s.Items[0])
	}
}

// RemoveOlderThan 跳过置顶项：同一过期时间下，置顶文字与置顶文件保留，
// 未置顶项移除。
func TestRemoveOlderThanSkipsPinned(t *testing.T) {
	s, _ := newStore(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{f}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("old text"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("old pinned"); err != nil {
		t.Fatal(err)
	}
	pinnedText := findItem(t, s, "old pinned")
	if err := s.SetPinned(pinnedText.ID, true); err != nil {
		t.Fatal(err)
	}
	fileID := ""
	for _, it := range s.Items {
		if it.Kind == KindFile && it.Path == f {
			fileID = it.ID
		}
	}
	if fileID == "" {
		t.Fatal("file item missing")
	}
	if err := s.SetPinned(fileID, true); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for _, it := range s.Items {
		setAge(t, s, it.ID, old)
	}
	n, err := s.RemoveOlderThan(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if len(s.Items) != 2 {
		t.Fatalf("kept %d items, want 2 pinned", len(s.Items))
	}
	for _, it := range s.Items {
		if !it.Pinned {
			t.Fatalf("unpinned item survived: %+v", it)
		}
	}
}

// Clear 保留置顶项及其引用的受管图片，移除其余全部。
func TestClearKeepsPinned(t *testing.T) {
	s, _ := newStore(t)
	if err := s.AddText("plain"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("pinned text"); err != nil {
		t.Fatal(err)
	}
	pinTextID := findItem(t, s, "pinned text").ID
	if err := s.SetPinned(pinTextID, true); err != nil {
		t.Fatal(err)
	}
	pngPinned := makePNG(t, 2, 2, color.RGBA{R: 1, A: 255})
	pngLoose := makePNG(t, 2, 2, color.RGBA{G: 2, A: 255})
	if err := s.AddImage(pngLoose); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(pngPinned); err != nil {
		t.Fatal(err)
	}
	pinImgID := s.Items[0].ID
	pinImgPath := s.Items[0].Path
	if err := s.SetPinned(pinImgID, true); err != nil {
		t.Fatal(err)
	}
	var looseImgPath string
	for _, it := range s.Items {
		if it.Kind == KindImage && !it.Pinned {
			looseImgPath = it.Path
		}
	}
	dir := t.TempDir()
	userFile := filepath.Join(dir, "keep.txt")
	if err := os.WriteFile(userFile, []byte("y"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{userFile}); err != nil {
		t.Fatal(err)
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if len(s.Items) != 2 {
		t.Fatalf("Clear kept %d items, want 2 pinned: %+v", len(s.Items), s.Items)
	}
	for _, it := range s.Items {
		if !it.Pinned {
			t.Fatalf("unpinned item survived Clear: %+v", it)
		}
	}
	if _, err := os.Stat(pinImgPath); err != nil {
		t.Fatalf("pinned managed image deleted: %v", err)
	}
	if _, err := os.Stat(looseImgPath); !os.IsNotExist(err) {
		t.Fatalf("unpinned managed image kept: %v", err)
	}
	if _, err := os.Stat(userFile); err != nil {
		t.Fatalf("user file deleted: %v", err)
	}
}

// 全部置顶时 Clear 是空操作：不写盘（在只读根上也成功）。
func TestClearAllPinnedIsNoOp(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("only pinned"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPinned(s.Items[0].ID, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear with only pinned items: %v", err)
	}
	if len(s.Items) != 1 {
		t.Fatalf("pinned item removed: %+v", s.Items)
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("all-pinned Clear wrote state.json")
	}
}

// SetPinned 在 Save 失败时回滚内存且不写盘；未知 id 报错且不动列表。
func TestSetPinnedRollbackOnSaveFailure(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("target"); err != nil {
		t.Fatal(err)
	}
	id := s.Items[0].ID
	if err := s.SetPinned("no-such-id", true); err == nil {
		t.Fatal("SetPinned on unknown id must fail")
	}
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	if err := s.SetPinned(id, true); err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if s.Items[0].Pinned {
		t.Fatal("Pinned not rolled back on save failure")
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json changed on failed SetPinned")
	}
}

// AddClipText 记录来源：新项目带 Source/SourceApp；已存在文本去重时
// 刷新 SourceApp 到本次复制的应用。普通 AddText 不写来源、也不抹掉
// 已有剪贴板项的来源。
func TestAddClipTextProvenance(t *testing.T) {
	s, _ := newStore(t)
	if err := s.AddClipText("hello", "com.example.Editor"); err != nil {
		t.Fatal(err)
	}
	it := s.Items[0]
	if it.Source != SourceClipboard || it.SourceApp != "com.example.Editor" {
		t.Fatalf("provenance not recorded: %+v", it)
	}
	if err := s.AddText("filler"); err != nil {
		t.Fatal(err)
	}
	// Dedup via clip add: same item, refreshed SourceApp, moved to front.
	if err := s.AddClipText("hello", "com.example.Browser"); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 2 {
		t.Fatalf("clip dedup duplicated: %+v", s.Items)
	}
	it = s.Items[0]
	if it.Text != "hello" || it.ID == "" {
		t.Fatalf("dedup did not move to front: %+v", it)
	}
	if it.Source != SourceClipboard || it.SourceApp != "com.example.Browser" {
		t.Fatalf("SourceApp not refreshed: %+v", it)
	}
	// Plain AddText dedup keeps provenance and never records a source.
	if err := s.AddText("plain text"); err != nil {
		t.Fatal(err)
	}
	if s.Items[0].Source != "" || s.Items[0].SourceApp != "" {
		t.Fatalf("plain AddText recorded provenance: %+v", s.Items[0])
	}
	if err := s.AddText("hello"); err != nil {
		t.Fatal(err)
	}
	if s.Items[0].Source != SourceClipboard || s.Items[0].SourceApp != "com.example.Browser" {
		t.Fatalf("plain AddText wiped provenance: %+v", s.Items[0])
	}
}

// AddClipImage 与 AddImage 共享校验/去重/受管文件保证，并记录来源。
func TestAddClipImageProvenance(t *testing.T) {
	s, _ := newStore(t)
	pngA := makePNG(t, 3, 3, color.RGBA{R: 9, A: 255})
	if err := s.AddClipImage(pngA, "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	it := s.Items[0]
	if it.Kind != KindImage || it.Source != SourceClipboard || it.SourceApp != "com.example.Shot" {
		t.Fatalf("clip image provenance not recorded: %+v", it)
	}
	// Dedup via clip add refreshes SourceApp and keeps one item.
	if err := s.AddClipImage(pngA, "com.example.Other"); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 1 {
		t.Fatalf("clip image dedup duplicated: %+v", s.Items)
	}
	if s.Items[0].SourceApp != "com.example.Other" {
		t.Fatalf("clip image SourceApp not refreshed: %+v", s.Items[0])
	}
	if err := s.AddClipImage(nil, "x"); err == nil {
		t.Fatal("empty clip image accepted")
	}
}

// HideFromTray 只把项目移出托盘视野：项目留在内存与 state.json 里
// （剪贴板页仍列出），受管图片与一切文件都不动；重开后标记仍在。
func TestHideFromTrayPersistsAcrossReopen(t *testing.T) {
	s, root := newStore(t)
	pngA := makePNG(t, 3, 3, color.RGBA{R: 31, A: 255})
	if err := s.AddClipImage(pngA, "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipText("复制的文字", "com.example.Editor"); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{f}); err != nil {
		t.Fatal(err)
	}
	// s.Items newest-first: file, text, image.
	img := s.Items[2]
	txt := s.Items[1]
	file := s.Items[0]
	managed := img.Path

	n, err := s.HideFromTray([]string{img.ID, txt.ID})
	if err != nil || n != 2 {
		t.Fatalf("HideFromTray = (%d, %v), want (2, nil)", n, err)
	}
	if len(s.Items) != 3 {
		t.Fatalf("hiding removed items: %+v", s.Items)
	}
	for _, it := range s.Items {
		wantOff := it.ID == img.ID || it.ID == txt.ID
		if it.OffTray != wantOff {
			t.Fatalf("OffTray wrong after hide: %+v", it)
		}
	}
	// 不删任何文件：受管图片与用户文件都必须还在。
	for _, p := range []string{managed, f} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("file deleted by hide: %s: %v", p, err)
		}
	}
	// 已隐藏的项目再次隐藏、未知 ID、空输入都是空操作，不写盘。
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{img.ID}, {"no-such-id"}, {}} {
		if n, err := s.HideFromTray(ids); err != nil || n != 0 {
			t.Fatalf("HideFromTray(%v) = (%d, %v), want (0, nil)", ids, n, err)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("no-op HideFromTray wrote state.json")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got := map[string]bool{}
	for _, it := range s2.Items {
		got[it.ID] = it.OffTray
	}
	if !got[img.ID] || !got[txt.ID] || got[file.ID] {
		t.Fatalf("OffTray not persisted: %+v", s2.Items)
	}
}

// Save 失败（根目录只读）时 HideFromTray 回滚内存、state.json 字节不变。
func TestHideFromTraySaveFailureRollsBack(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddClipText("复制的文字", "com.example.Editor"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipImage(makePNG(t, 2, 2, color.RGBA{G: 17, A: 255}), "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	managed := s.Items[0].Path
	ids := itemIDs(s)
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	n, err := s.HideFromTray(ids)
	if err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if n != 0 {
		t.Fatalf("save failed but reported %d hidden", n)
	}
	for _, it := range s.Items {
		if it.OffTray {
			t.Fatalf("OffTray not rolled back: %+v", s.Items)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json changed on failed HideFromTray")
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png gone on failed hide: %v", err)
	}
}

// HideFromClipboard 是 HideFromTray 的镜像：项目只从剪贴板页消失，仍留在
// 托盘、state.json 与内存里，受管图片与一切文件都不动；重开后标记仍在。
func TestHideFromClipboardPersistsAcrossReopen(t *testing.T) {
	s, root := newStore(t)
	pngA := makePNG(t, 3, 3, color.RGBA{R: 61, A: 255})
	if err := s.AddClipImage(pngA, "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipText("复制的文字", "com.example.Editor"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(makePNG(t, 2, 2, color.RGBA{G: 62, A: 255})); err != nil {
		t.Fatal(err)
	}
	// s.Items newest-first: image, text, image.
	clipImg := s.Items[2]
	txt := s.Items[1]
	other := s.Items[0]
	managed := clipImg.Path

	n, err := s.HideFromClipboard([]string{clipImg.ID, txt.ID})
	if err != nil || n != 2 {
		t.Fatalf("HideFromClipboard = (%d, %v), want (2, nil)", n, err)
	}
	if len(s.Items) != 3 {
		t.Fatalf("hiding removed items: %+v", s.Items)
	}
	for _, it := range s.Items {
		wantOff := it.ID == clipImg.ID || it.ID == txt.ID
		if it.OffClip != wantOff {
			t.Fatalf("OffClip wrong after hide: %+v", it)
		}
		if it.OffTray {
			t.Fatalf("HideFromClipboard touched OffTray: %+v", it)
		}
	}
	// 不删任何文件：受管图片必须还在。
	for _, p := range []string{managed, other.Path} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("file deleted by hide: %s: %v", p, err)
		}
	}
	// 已隐藏的项目再次隐藏、未知 ID、空输入都是空操作，不写盘。
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{clipImg.ID}, {"no-such-id"}, {}} {
		if n, err := s.HideFromClipboard(ids); err != nil || n != 0 {
			t.Fatalf("HideFromClipboard(%v) = (%d, %v), want (0, nil)", ids, n, err)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("no-op HideFromClipboard wrote state.json")
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got := map[string]bool{}
	for _, it := range s2.Items {
		got[it.ID] = it.OffClip
	}
	if !got[clipImg.ID] || !got[txt.ID] || got[other.ID] {
		t.Fatalf("OffClip not persisted: %+v", s2.Items)
	}
}

// Save 失败（根目录只读）时 HideFromClipboard 回滚内存、state.json 字节不变。
func TestHideFromClipboardSaveFailureRollsBack(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddClipText("复制的文字", "com.example.Editor"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipImage(makePNG(t, 2, 2, color.RGBA{G: 71, A: 255}), "com.example.Shot"); err != nil {
		t.Fatal(err)
	}
	managed := s.Items[0].Path
	ids := itemIDs(s)
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	n, err := s.HideFromClipboard(ids)
	if err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if n != 0 {
		t.Fatalf("save failed but reported %d hidden", n)
	}
	for _, it := range s.Items {
		if it.OffClip {
			t.Fatalf("OffClip not rolled back: %+v", s.Items)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json changed on failed HideFromClipboard")
	}
	if _, err := os.Stat(managed); err != nil {
		t.Fatalf("managed png gone on failed hide: %v", err)
	}
}

// 再次复制同一内容（四条 add 路径的去重分支）会清掉 OffClip，让被移出剪贴板
// 页的项目回到两页，ID 不变。
func TestReAddClearsOffClip(t *testing.T) {
	s, _ := newStore(t)
	pngA := makePNG(t, 3, 3, color.RGBA{R: 81, A: 255})
	pngB := makePNG(t, 3, 3, color.RGBA{B: 82, A: 255})
	if err := s.AddClipImage(pngA, "app"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipText("clipped", "app"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(pngB); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("dropped"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.HideFromClipboard(itemIDs(s)); err != nil || n != 4 {
		t.Fatalf("HideFromClipboard all = (%d, %v), want (4, nil)", n, err)
	}

	if err := s.AddClipText("clipped", "app2"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("dropped"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipImage(pngA, "app3"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(pngB); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 4 {
		t.Fatalf("re-add duplicated: %+v", s.Items)
	}
	for _, it := range s.Items {
		if it.OffClip {
			t.Fatalf("re-add did not clear OffClip: %+v", it)
		}
	}
}

// 置顶被移出剪贴板页的项目把它带回剪贴板页首：SetPinned(id, true) 清掉
// OffClip，已置顶的项目也不例外（否则它永远只能在托盘上看到）。
func TestPinClearsOffClip(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddClipImage(makePNG(t, 2, 2, color.RGBA{G: 91, A: 255}), "app"); err != nil {
		t.Fatal(err)
	}
	id := s.Items[0].ID
	if err := s.SetPinned(id, true); err != nil {
		t.Fatal(err)
	}
	if n, err := s.HideFromClipboard([]string{id}); err != nil || n != 1 {
		t.Fatalf("HideFromClipboard = (%d, %v), want (1, nil)", n, err)
	}
	if !s.Items[0].OffClip || !s.Items[0].Pinned {
		t.Fatalf("hide clobbered pin state: %+v", s.Items[0])
	}
	// 已置顶的项目再次“置顶”不是空操作：还要清掉 OffClip 并落盘。
	if err := s.SetPinned(id, true); err != nil {
		t.Fatal(err)
	}
	if s.Items[0].OffClip || !s.Items[0].Pinned {
		t.Fatalf("pin did not unhide from the clipboard page: %+v", s.Items[0])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.Items[0].OffClip || !s2.Items[0].Pinned {
		t.Fatalf("unhide not persisted: %+v", s2.Items[0])
	}
}

// 同一内容再次收录（四种 add 路径的去重分支）都会清掉 OffTray：
// 项目移回最前并重新出现在托盘上，保持原 ID 不变。
func TestReAddClearsOffTray(t *testing.T) {
	s, _ := newStore(t)
	pngA := makePNG(t, 3, 3, color.RGBA{R: 51, A: 255})
	pngB := makePNG(t, 3, 3, color.RGBA{B: 52, A: 255})

	if err := s.AddClipImage(pngA, "app"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipText("clipped", "app"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(pngB); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("dropped"); err != nil {
		t.Fatal(err)
	}
	// newest-first: text(dropped), image(pngB), text(clipped), image(pngA).
	n, err := s.HideFromTray(itemIDs(s))
	if err != nil || n != 4 {
		t.Fatalf("HideFromTray all = (%d, %v), want (4, nil)", n, err)
	}
	for _, it := range s.Items {
		if !it.OffTray {
			t.Fatalf("item not hidden: %+v", s.Items)
		}
	}

	// 剪贴板与普通路径各覆盖一次文字与图片的去重分支。
	if err := s.AddClipText("clipped", "app2"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddText("dropped"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddClipImage(pngA, "app3"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddImage(pngB); err != nil {
		t.Fatal(err)
	}
	if len(s.Items) != 4 {
		t.Fatalf("re-add duplicated: %+v", s.Items)
	}
	for _, it := range s.Items {
		if it.OffTray {
			t.Fatalf("re-add did not clear OffTray: %+v", it)
		}
	}
	// 去重把项目移到最前：最后是 pngB 的 AddImage。
	if s.Items[0].Kind != KindImage || s.Items[0].Text != "" {
		t.Fatalf("dedup did not move last re-add to front: %+v", s.Items[0])
	}
}

// 置顶被移出托盘的项目把它带回来：SetPinned(id, true) 清掉 OffTray，
// 即使项目已经置顶；Save 失败时两个字段一起回滚。
func TestPinClearsOffTray(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddClipImage(makePNG(t, 2, 2, color.RGBA{G: 77, A: 255}), "app"); err != nil {
		t.Fatal(err)
	}
	imgID := s.Items[0].ID
	// 先置顶再从托盘隐藏（置顶项也可以被移出托盘，例如剪贴板页的移除）。
	if err := s.SetPinned(imgID, true); err != nil {
		t.Fatal(err)
	}
	if n, err := s.HideFromTray([]string{imgID}); err != nil || n != 1 {
		t.Fatalf("HideFromTray = (%d, %v), want (1, nil)", n, err)
	}
	if !s.Items[0].OffTray || !s.Items[0].Pinned {
		t.Fatalf("hide clobbered pin state: %+v", s.Items[0])
	}
	// 已置顶的项目再次“置顶”不是空操作：还要把 OffTray 清掉并落盘。
	if err := s.SetPinned(imgID, true); err != nil {
		t.Fatal(err)
	}
	if s.Items[0].OffTray || !s.Items[0].Pinned {
		t.Fatalf("pin did not unhide: %+v", s.Items[0])
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Items[0].OffTray || !s2.Items[0].Pinned {
		t.Fatalf("unhide not persisted: %+v", s2.Items[0])
	}

	// 取消置顶不重新隐藏项目。
	if err := s2.SetPinned(imgID, false); err != nil {
		t.Fatal(err)
	}
	if s2.Items[0].Pinned || s2.Items[0].OffTray {
		t.Fatalf("unpin touched OffTray: %+v", s2.Items[0])
	}
}

// 隐藏状态下的置顶项 Save 失败时：Pinned 与 OffTray 都必须恢复原值。
func TestSetPinnedUnhideRollbackOnSaveFailure(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddClipText("hidden pinned", "app"); err != nil {
		t.Fatal(err)
	}
	id := s.Items[0].ID
	if err := s.SetPinned(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HideFromTray([]string{id}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	if err := s.SetPinned(id, true); err == nil {
		t.Fatal("expected save error with read-only root")
	}
	it := s.Items[0]
	if !it.Pinned || !it.OffTray {
		t.Fatalf("pin/unhide not rolled back: %+v", it)
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json changed on failed SetPinned")
	}
}

// convertedFile writes a file inside the store's converted directory and
// returns its path — the shape AddConverted expects from the converter.
func convertedFile(t *testing.T, s *Store, name, content string) string {
	t.Helper()
	p := filepath.Join(s.ConvertedDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write converted fixture: %v", err)
	}
	return p
}

// convertedFolder creates <converted>/name holding files (relative name →
// content) and registers it as a Converted folder item, the shape an
// extracted archive takes.
func convertedFolder(t *testing.T, s *Store, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(s.ConvertedDir(), name)
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir fixture: %v", err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	if err := s.AddFiles([]string{dir}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	for i := range s.Items {
		if s.Items[i].Path == dir {
			s.Items[i].Converted = true
		}
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return dir
}

// AddConverted 把 converted 目录里既有的文件逐个登记为 KindFile +
// Converted 项，按给定顺序排到最前（paths[0] 成为 Items[0]），返回同序 ID；
// 重开后顺序、Converted 标记与 Missing 状态都持久化。
func TestAddConvertedOrderFlagsPersist(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("pre-existing"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	p1 := convertedFile(t, s, "a.pdf", "pdf-a")
	p2 := convertedFile(t, s, "b.png", "png-b")

	ids, err := s.AddConverted([]string{p1, p2})
	if err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	if len(ids) != 2 || ids[0] == "" || ids[1] == "" || ids[0] == ids[1] {
		t.Fatalf("bad ids: %v", ids)
	}
	if len(s.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(s.Items))
	}
	for i, want := range []string{p1, p2} {
		it := s.Items[i]
		if it.ID != ids[i] || it.Kind != KindFile || !it.Converted || it.Missing {
			t.Fatalf("item %d = %+v, want converted file item", i, it)
		}
		if it.Path != want {
			t.Fatalf("item %d path = %q, want %q", i, it.Path, want)
		}
	}
	if s.Items[2].Text != "pre-existing" {
		t.Fatalf("pre-existing item not pushed back: %+v", s.Items[2])
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 3 || !s2.Items[0].Converted || !s2.Items[1].Converted {
		t.Fatalf("converted items not persisted: %+v", s2.Items)
	}
	if s2.Items[0].ID != ids[0] || s2.Items[0].Path != p1 || s2.Items[0].Missing {
		t.Fatalf("first converted item after reopen: %+v", s2.Items[0])
	}
}

// Open 按 0700 创建 converted 目录，ConvertedDir 指向它。
func TestConvertedDirCreated(t *testing.T) {
	s, root := newStore(t)
	want := filepath.Join(realRoot(t, root), convertedDirName)
	if s.ConvertedDir() != want {
		t.Fatalf("ConvertedDir = %q, want %q", s.ConvertedDir(), want)
	}
	if p := filePerm(t, s.ConvertedDir()); p != 0o700 {
		t.Fatalf("converted dir perm = %o, want 700", p)
	}
}

// 不在 converted 目录内的路径——包括不存在的文件、普通用户文件、软链接
// 指向目录外的文件、以及目录本身——一律整体拒绝：返回错误且 Items 不变。
func TestAddConvertedRejectsOutsideAndMissing(t *testing.T) {
	s, _ := newStore(t)
	if err := s.AddText("keep"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "user.jpg")
	if err := os.WriteFile(outside, []byte("u"), 0o600); err != nil {
		t.Fatal(err)
	}
	// converted 目录里指向外部文件的软链接同样必须被拒（真实目标在外）。
	linkOut := filepath.Join(s.ConvertedDir(), "escape.pdf")
	if err := os.Symlink(outside, linkOut); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(s.ConvertedDir(), "ghost.pdf")
	imagesFile := filepath.Join(realRoot(t, s.Root), imagesDirName, "x.png")
	if err := os.WriteFile(imagesFile, []byte("i"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, paths := range map[string][]string{
		"outside":     {outside},
		"missing":     {missing},
		"symlink out": {linkOut},
		"dir itself":  {s.ConvertedDir()},
		"images dir":  {imagesFile},
		"empty":       {""},
		"mixed bad":   {convertedFile(t, s, "ok.pdf", "ok"), outside},
	} {
		if ids, err := s.AddConverted(paths); err == nil || ids != nil {
			t.Fatalf("%s: AddConverted(%v) = ids %v, err %v", name, paths, ids, err)
		}
		if len(s.Items) != 1 || s.Items[0].Text != "keep" {
			t.Fatalf("%s: Items changed on rejected add: %+v", name, s.Items)
		}
	}
}

// Save 失败（根目录只读）时 AddConverted 回滚内存：不新增项、不写盘，
// 文件本身留给调用方处理（不替它删除）。
func TestAddConvertedSaveFailureRollsBack(t *testing.T) {
	s, root := newStore(t)
	if err := s.AddText("keep"); err != nil {
		t.Fatalf("AddText: %v", err)
	}
	p := convertedFile(t, s, "out.pdf", "pdf")
	before, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })

	if ids, err := s.AddConverted([]string{p}); err == nil || ids != nil {
		t.Fatalf("expected save failure, got ids %v err %v", ids, err)
	}
	if len(s.Items) != 1 || s.Items[0].Text != "keep" {
		t.Fatalf("memory not rolled back: %+v", s.Items)
	}
	after, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("state.json changed on failed AddConverted")
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("converted file wrongly deleted on failed add: %v", err)
	}
}

// Remove 删除被移除转换项的文件；其余条目与用户文件不受影响。
func TestRemoveDeletesConvertedFile(t *testing.T) {
	s, _ := newStore(t)
	user := filepath.Join(t.TempDir(), "keep.txt")
	if err := os.WriteFile(user, []byte("u"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{user}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	p := convertedFile(t, s, "c.pdf", "pdf")
	ids, err := s.AddConverted([]string{p})
	if err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	if err := s.Remove(ids[0]); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("converted file still exists: %v", err)
	}
	if _, err := os.Stat(user); err != nil {
		t.Fatalf("user file deleted: %v", err)
	}
	if len(s.Items) != 1 || s.Items[0].Path != user {
		t.Fatalf("wrong items left: %+v", s.Items)
	}
}

// 其他条目（如把同一文件当普通文件拖进来的 KindFile 项）仍引用转换
// 产物时，移除转换项不删文件；引用消失后的下一次清理才删除。
func TestRemoveConvertedKeepsReferencedFile(t *testing.T) {
	s, _ := newStore(t)
	p := convertedFile(t, s, "shared.pdf", "pdf")
	ids, err := s.AddConverted([]string{p})
	if err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	// 另一个条目引用同一文件（例如用户把产物拖回来形成的普通文件项；
	// AddFiles 会同路径去重，所以直接注入一条独立引用项）。
	ref := Item{ID: newID(), Kind: KindFile, Path: p, CreatedAt: time.Now()}
	s.Items = append([]Item{ref}, s.Items...)
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s.Remove(ids[0]); err != nil {
		t.Fatalf("Remove converted: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("still-referenced converted file deleted: %v", err)
	}
	if err := s.Remove(s.Items[0].ID); err != nil {
		t.Fatalf("Remove file ref: %v", err)
	}
	// 文件引用本身不拥有文件：没人再拥有它时文件保留到 Open 的孤儿清理。
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("file ref wrongly deleted the file: %v", err)
	}
}

// RemoveMany、RemoveOlderThan 与 Clear 走同一条 managed-file 清理路径：
// 转换项的文件被删除，普通 KindFile 指向的文件（无论在哪）绝不删除，
// pinned 的转换项在 Clear/RemoveOlderThan 下保留。
func TestRemovalPathsDeleteConvertedFiles(t *testing.T) {
	s, _ := newStore(t)
	user := filepath.Join(t.TempDir(), "user.doc")
	if err := os.WriteFile(user, []byte("u"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{user}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	p1 := convertedFile(t, s, "m1.pdf", "1")
	p2 := convertedFile(t, s, "m2.pdf", "2")
	ids, err := s.AddConverted([]string{p1, p2})
	if err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	if n, err := s.RemoveMany([]string{ids[0], "no-such-id"}); err != nil || n != 1 {
		t.Fatalf("RemoveMany = %d, %v", n, err)
	}
	if _, err := os.Stat(p1); !os.IsNotExist(err) {
		t.Fatalf("RemoveMany kept converted file: %v", err)
	}

	// RemoveOlderThan: 早于 cutoff 的未 pin 转换项被删文件。
	old := convertedFile(t, s, "old.pdf", "old")
	pin := convertedFile(t, s, "pin.pdf", "pin")
	ids2, err := s.AddConverted([]string{old, pin})
	if err != nil {
		t.Fatalf("AddConverted 2: %v", err)
	}
	setAge(t, s, ids2[0], time.Now().Add(-time.Hour)) // old.pdf
	setAge(t, s, ids2[1], time.Now().Add(-time.Hour)) // pin.pdf
	setAge(t, s, ids[1], time.Now().Add(-time.Hour))  // m2.pdf
	if err := s.SetPinned(ids2[1], true); err != nil {
		t.Fatalf("SetPinned: %v", err)
	}
	if n, err := s.RemoveOlderThan(time.Now()); err != nil || n != 3 {
		t.Fatalf("RemoveOlderThan = %d, %v", n, err)
	}
	// 未 pin 的用户文件项同样早于 cutoff：项被移除，文件绝不删除。
	if _, err := os.Stat(user); err != nil {
		t.Fatalf("RemoveOlderThan deleted user file: %v", err)
	}
	for _, p := range []string{p2, old} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("RemoveOlderThan kept converted file %s: %v", p, err)
		}
	}
	if _, err := os.Stat(pin); err != nil {
		t.Fatalf("pinned converted file deleted: %v", err)
	}

	// Clear: 删除剩下的未 pin 转换文件，pinned 的留下，用户文件留下。
	p3 := convertedFile(t, s, "clear.pdf", "3")
	if _, err := s.AddConverted([]string{p3}); err != nil {
		t.Fatalf("AddConverted 3: %v", err)
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if _, err := os.Stat(p3); !os.IsNotExist(err) {
		t.Fatalf("Clear kept converted file: %v", err)
	}
	if _, err := os.Stat(pin); err != nil {
		t.Fatalf("Clear deleted pinned converted file: %v", err)
	}
	if _, err := os.Stat(user); err != nil {
		t.Fatalf("Clear deleted user file: %v", err)
	}
	if len(s.Items) != 1 || !s.Items[0].Pinned || !s.Items[0].Converted {
		t.Fatalf("items after Clear: %+v", s.Items)
	}
}

// 普通 KindFile 项即使指向 converted 目录内部（用户手工拖入），移除时也
// 绝不删文件——只有 Converted 标记的项才拥有文件。
func TestRemoveFileItemInsideConvertedKeepsFile(t *testing.T) {
	s, _ := newStore(t)
	p := convertedFile(t, s, "dropped.pdf", "pdf")
	if err := s.AddFiles([]string{p}); err != nil {
		t.Fatalf("AddFiles: %v", err)
	}
	if err := s.Remove(s.Items[0].ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("plain file item's file inside converted was deleted: %v", err)
	}
}

// 审查复现：state.json 里的转换项仍指向旧的绝对路径、该路径已不存在，
// 但当前 converted 目录里有同名产物时，必须解析到它——既不标 Missing，
// 也不在 Open 的孤儿清理里把它当孤儿删掉。
func TestConvertedStaleAbsolutePathFindsSameName(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, convertedDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	// 旧数据目录下的路径，那条数据目录已经搬走。
	stale := filepath.Join(t.TempDir(), "moved-away", "report.pdf")
	real := filepath.Join(realRoot(t, root), convertedDirName, filepath.Base(stale))
	if err := os.WriteFile(real, []byte("out"), 0o600); err != nil {
		t.Fatal(err)
	}
	sf := stateFile{Version: stateVersion, Items: []Item{{
		ID: "cv", Kind: KindFile, Path: stale, Converted: true,
	}}}
	data, _ := json.Marshal(sf)
	if err := os.WriteFile(filepath.Join(root, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if len(s.Items) != 1 || s.Items[0].Missing {
		t.Fatalf("the same-name output was not adopted: %+v", s.Items)
	}
	if want := filepath.Join(s.ConvertedDir(), filepath.Base(stale)); s.Items[0].Path != want {
		t.Fatalf("resolved path = %q, want %q", s.Items[0].Path, want)
	}
	if _, err := os.Stat(real); err != nil {
		t.Fatalf("the adopted output was cleaned as an orphan: %v", err)
	}
}

// 审查复现：state.json 不存在时无从判断引用关系，Open 不清理 converted——
// 否则 state 丢失（或刚装好）的目录里，唯一的转换产物会被当孤儿删掉。
func TestOpenWithoutStateKeepsConverted(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, convertedDirName)
	if err := os.MkdirAll(filepath.Join(dir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "output.pdf")
	if err := os.WriteFile(keep, []byte("o"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, stateFileName)); !os.IsNotExist(err) {
		t.Fatalf("the fixture must not carry state.json: %v", err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("a converted output was cleaned without state: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "folder")); err != nil {
		t.Fatalf("a converted folder was cleaned without state: %v", err)
	}
}

// Open 清理 converted 目录里无人引用的文件与文件夹（转换中断的残留），
// 被引用的文件、文件夹及目录之外的一切都原样保留。
func TestOpenCleansOrphanedConverted(t *testing.T) {
	s, root := newStore(t)
	kept := convertedFile(t, s, "kept.pdf", "k")
	if _, err := s.AddConverted([]string{kept}); err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	keptDir := convertedFolder(t, s, "kept-folder", map[string]string{"inner.txt": "i"})
	orphan := filepath.Join(s.ConvertedDir(), "orphan.tmp")
	if err := os.WriteFile(orphan, []byte("o"), 0o600); err != nil {
		t.Fatal(err)
	}
	orphanDir := filepath.Join(s.ConvertedDir(), "orphan-folder")
	if err := os.MkdirAll(filepath.Join(orphanDir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphanDir, "nested", "stale.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 目录之外的同名文件夹绝不被清理。
	outside := filepath.Join(t.TempDir(), "orphan-folder")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphaned converted file kept: %v", err)
	}
	if _, err := os.Stat(orphanDir); !os.IsNotExist(err) {
		t.Fatalf("orphaned converted folder kept: %v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("referenced converted file cleaned: %v", err)
	}
	if _, err := os.Stat(filepath.Join(keptDir, "inner.txt")); err != nil {
		t.Fatalf("referenced converted folder cleaned: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("folder outside converted cleaned: %v", err)
	}
}

// Remove 删除解压结果这类转换文件夹：整棵树消失且不报错，文件夹里的
// 符号链接不被跟随（目标原样保留），converted 之外的同名文件夹不受影响。
func TestRemoveDeletesConvertedFolder(t *testing.T) {
	s, _ := newStore(t)
	outside := filepath.Join(t.TempDir(), "precious")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	treasure := filepath.Join(outside, "keep.txt")
	if err := os.WriteFile(treasure, []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := convertedFolder(t, s, "包", map[string]string{"a.txt": "a", "sub/b.txt": "b"})
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	// 同一根下普通文件项也不能被误解为拥有整个目录。
	user := filepath.Join(t.TempDir(), "user.txt")
	if err := os.WriteFile(user, []byte("u"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{user}); err != nil {
		t.Fatal(err)
	}
	var id string
	for _, it := range s.Items {
		if it.Converted && it.Path == dir {
			id = it.ID
		}
	}
	if id == "" {
		t.Fatal("converted folder item not found")
	}
	if err := s.Remove(id); err != nil {
		t.Fatalf("Remove converted folder: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("converted folder kept: %v", err)
	}
	if _, err := os.Stat(treasure); err != nil {
		t.Fatalf("symlink target deleted with the folder: %v", err)
	}
	if _, err := os.Stat(user); err != nil {
		t.Fatalf("plain user file deleted: %v", err)
	}
	if len(s.Items) != 1 || s.Items[0].Path != user {
		t.Fatalf("items after remove: %+v", s.Items)
	}
}

// 标记为 Converted、路径却在 converted 之外的文件夹绝不删除。
func TestRemoveConvertedFolderOutsideUntouched(t *testing.T) {
	s, _ := newStore(t)
	outside := filepath.Join(t.TempDir(), "包")
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outside, "a.txt")
	if err := os.WriteFile(inner, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Items = append([]Item{{
		ID: newID(), Kind: KindFile, Path: outside, CreatedAt: time.Now(), Converted: true,
	}}, s.Items...)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(s.Items[0].ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(inner); err != nil {
		t.Fatalf("folder outside converted deleted: %v", err)
	}
}

// converted 目录里的软链接即使名字像文件夹也只是被 unlink，绝不跟随
// 到目标目录里删除内容。
func TestRemoveConvertedSymlinkNotFollowed(t *testing.T) {
	s, _ := newStore(t)
	target := t.TempDir()
	treasure := filepath.Join(target, "keep.txt")
	if err := os.WriteFile(treasure, []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(s.ConvertedDir(), "out")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	s.Items = append([]Item{{
		ID: newID(), Kind: KindFile, Path: link, CreatedAt: time.Now(), Converted: true,
	}}, s.Items...)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(s.Items[0].ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("converted symlink kept: %v", err)
	}
	if _, err := os.Stat(treasure); err != nil {
		t.Fatalf("symlink target deleted: %v", err)
	}
}

// 重开时文件已消失的转换项与其他 KindFile 一样保留为 Missing，不被丢弃。
func TestConvertedMissingOnReopen(t *testing.T) {
	s, root := newStore(t)
	p := convertedFile(t, s, "gone.pdf", "g")
	if _, err := s.AddConverted([]string{p}); err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 1 || !s2.Items[0].Missing || !s2.Items[0].Converted {
		t.Fatalf("missing converted item not kept: %+v", s2.Items)
	}
}

// 转换产物与受管图片一样只把文件名落盘：整个数据目录搬走（或经软链接
// 打开）后重开仍解析到 moved-root 下的同一文件，而不是写死旧绝对路径。
func TestConvertedPathsPersistRelative(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "data")
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p := convertedFile(t, s, "rel.pdf", "rel")
	if _, err := s.AddConverted([]string{p}); err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(realRoot(t, root), stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	var sf stateFile
	if err := json.Unmarshal(data, &sf); err != nil {
		t.Fatal(err)
	}
	if len(sf.Items) != 1 || !sf.Items[0].Converted {
		t.Fatalf("persisted items: %+v", sf.Items)
	}
	if got := sf.Items[0].Path; got != "rel.pdf" || filepath.IsAbs(got) || got != filepath.Base(got) {
		t.Fatalf("converted path not persisted relative: %q", got)
	}

	moved := filepath.Join(parent, "moved")
	if err := os.Rename(realRoot(t, root), moved); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(moved)
	if err != nil {
		t.Fatalf("Open moved root: %v", err)
	}
	defer s2.Close()
	if len(s2.Items) != 1 || s2.Items[0].Missing || !s2.Items[0].Converted {
		t.Fatalf("converted item lost after moving root: %+v", s2.Items)
	}
	want := filepath.Join(realRoot(t, moved), convertedDirName, "rel.pdf")
	if s2.Items[0].Path != want {
		t.Fatalf("resolved path = %q, want %q", s2.Items[0].Path, want)
	}
	got, err := os.ReadFile(s2.Items[0].Path)
	if err != nil || string(got) != "rel" {
		t.Fatalf("resolved converted file unreadable: err=%v", err)
	}
}

// 存量旧 state.json（converted 项的绝对路径写法）重开时统一到当前根的
// converted 目录；指向外部的 converted 路径不会被删除。
func TestConvertedAbsolutePathResolvesIntoCurrentRoot(t *testing.T) {
	root := t.TempDir()
	cdir := filepath.Join(realRoot(t, root), convertedDirName)
	if err := os.MkdirAll(cdir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(cdir, "abs.pdf")
	if err := os.WriteFile(p, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "else.pdf")
	if err := os.WriteFile(outside, []byte("e"), 0o600); err != nil {
		t.Fatal(err)
	}
	sf := stateFile{Version: stateVersion, Items: []Item{
		{ID: "c1", Kind: KindFile, Path: p, Converted: true},
		{ID: "c2", Kind: KindFile, Path: outside, Converted: true},
	}}
	data, _ := json.Marshal(sf)
	if err := os.WriteFile(filepath.Join(root, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if len(s.Items) != 2 {
		t.Fatalf("items: %+v", s.Items)
	}
	if s.Items[0].Path != p {
		t.Fatalf("absolute converted path not unified: %q", s.Items[0].Path)
	}
	// Converted 标记但文件在目录外：不得删除。
	if err := s.Remove("c2"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file deleted for converted item: %v", err)
	}
}

// converted 目录只读导致文件删不掉时：项已移除并落盘，错误如实上报，
// 删不掉的文件原样保留。
func TestRemoveConvertedCleanupError(t *testing.T) {
	s, _ := newStore(t)
	p1 := convertedFile(t, s, "e1.pdf", "1")
	p2 := convertedFile(t, s, "e2.pdf", "2")
	ids, err := s.AddConverted([]string{p1, p2})
	if err != nil {
		t.Fatalf("AddConverted: %v", err)
	}
	if err := os.Chmod(s.ConvertedDir(), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(s.ConvertedDir(), 0o755) })

	if err := s.Remove(ids[0]); err == nil ||
		!strings.Contains(err.Error(), "deleting managed file") {
		t.Fatalf("expected cleanup error, got %v", err)
	}
	if len(s.Items) != 1 || s.Items[0].ID != ids[1] {
		t.Fatalf("items: %+v", s.Items)
	}
	if _, err := os.Stat(p1); err != nil {
		t.Fatalf("file gone despite failed deletion: %v", err)
	}
}

// converted 目录本身是软链接（或普通占位文件）时，绝不跟随它：
// Open 不在其目标里建目录、不清理目标目录内容，AddConverted 拒绝
// 通过该软链接的路径，移除项时也不把目标目录里的文件当受管文件删。
func TestConvertedDirSymlinkNeverManaged(t *testing.T) {
	target := t.TempDir()
	treasure := filepath.Join(target, "precious.pdf")
	if err := os.WriteFile(treasure, []byte("t"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 目标目录里再放一个“孤儿”，验证 Open 的孤儿清理绝不进入。
	stray := filepath.Join(target, "stray.bin")
	if err := os.WriteFile(stray, []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, convertedDirName)
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	// 一条声称产物在（软链接拼出的）converted 里的旧状态。
	throughLink := filepath.Join(link, "precious.pdf")
	sf := stateFile{Version: stateVersion, Items: []Item{{
		ID: "cv", Kind: KindFile, Path: throughLink, Converted: true,
	}}}
	data, _ := json.Marshal(sf)
	if err := os.WriteFile(filepath.Join(root, stateFileName), data, 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	// 孤儿清理未跟随软链接：目标目录一切原样。
	for _, p := range []string{treasure, stray} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("cleanup followed symlinked converted dir: %s: %v", p, err)
		}
	}
	// 软链接本身仍在。
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("converted symlink disturbed: %v", err)
	}
	// AddConverted 拒绝通过软链接目录的路径。
	if ids, err := s.AddConverted([]string{throughLink}); err == nil || ids != nil {
		t.Fatalf("AddConverted through symlinked dir = %v, %v", ids, err)
	}
	// 移除声称的转换项不删目标目录里的文件。
	if err := s.Remove("cv"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(treasure); err != nil {
		t.Fatalf("file inside symlinked dir treated as managed: %v", err)
	}
}

// converted 位置是普通文件（不是目录）时同样降级：Open 成功、占位文件
// 原样保留、AddConverted 拒绝、孤儿清理不运行。
func TestConvertedDirOccupiedByFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	occupier := filepath.Join(root, convertedDirName)
	if err := os.WriteFile(occupier, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if _, err := s.AddConverted([]string{filepath.Join(occupier, "x.pdf")}); err == nil {
		t.Fatal("AddConverted accepted path under non-directory converted")
	}
	got, err := os.ReadFile(occupier)
	if err != nil || string(got) != "mine" {
		t.Fatalf("occupying file disturbed: %v", err)
	}
}
