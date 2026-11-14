//go:build !windows

package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// symlinkTo creates a symlink pointing at dir and returns its path.
func symlinkTo(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "data-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatalf("Symlink: %v", err)
	}
	return link
}

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return fi.Mode().Perm()
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
// 常规备份名被目录占用时，应改用独立的新
// 备份文件保留本次迁移前的原件，再继续迁移；绝不能在无恢复点的情况下
// 覆盖原件，也不得覆盖占位者。
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

// 旧版 state.json 没有 pinned/source/sourceApp 字段：加载原样生效，
// 新增字段取零值（未置顶、无来源）。
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

// AddConverted 把 converted 目录里既有的文件逐个登记为 KindFile +
// Converted 项，按给定顺序排到最前（paths[0] 成为 Items[0]），返回同序 ID；
// 重开后顺序、Converted 标记与 Missing 状态都持久化。
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

// A failed save restores the in-memory paths and leaves state.json with
// the old names.
func TestUpdatePathsSaveFailureRollsBack(t *testing.T) {
	s, root := newStore(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.AddFiles([]string{f}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(root, 0o755) })
	if _, err := s.UpdatePaths(map[string]string{
		s.Items[0].ID: filepath.Join(dir, "b.txt"),
	}); err == nil {
		t.Fatal("expected save error with read-only root")
	}
	if s.Items[0].Path != f {
		t.Fatalf("path not rolled back: %q", s.Items[0].Path)
	}
}
