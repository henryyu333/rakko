package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 一个无法解析的 state.json 在 Open 里报 ErrStateUnreadable；旁边能解析的
// 备份构成恢复菜单，最新的一份排最前。
func TestRecoveryBackupsNewestParseableFirst(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string, mod time.Duration) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-mod)
		if err := os.Chtimes(filepath.Join(root, name), past, past); err != nil {
			t.Fatal(err)
		}
	}
	bak1 := mustState(t, []Item{{ID: "a", Kind: KindText, Text: "old"}})
	bak2 := mustState(t, []Item{{ID: "b", Kind: KindText, Text: "new"}})
	write(stateFileName+".bak", bak1, 2*time.Hour)
	write(stateFileName+".bak-2", bak2, time.Hour)
	write(stateFileName+".bak-3", "{ not json", 30*time.Minute)
	if err := os.Mkdir(filepath.Join(root, stateFileName+".bak-4"), 0o700); err != nil {
		t.Fatal(err)
	}

	got := RecoveryBackups(root)
	if len(got) != 2 || got[0] != stateFileName+".bak-2" || got[1] != stateFileName+".bak" {
		t.Fatalf("backups = %v, want bak-2 (newest) then bak", got)
	}
}

// 选「重新开始」：坏原件改名保留，条目清空，坏文件一个字节都不丢。
func TestRecoverStateStartsOverKeepingOriginal(t *testing.T) {
	root, bad := corruptStateDir(t)
	if got := RecoveryBackups(root); len(got) != 0 {
		t.Fatalf("no backup expected, got %v", got)
	}
	if err := RecoverState(root, ""); err != nil {
		t.Fatalf("RecoverState: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, stateFileName)); !os.IsNotExist(err) {
		t.Fatalf("state.json should be gone (fresh start reads empty): %v", err)
	}
	kept := corruptCopy(t, root)
	if string(kept) != bad {
		t.Fatalf("the corrupt original changed: %q", kept)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatalf("reopen after recovery: %v", err)
	}
	defer s.Close()
	if len(s.Items) != 0 {
		t.Fatalf("a fresh start must begin empty: %+v", s.Items)
	}
}

// 选「用最近的备份恢复」：坏原件同样改名保留，备份内容成为新 state.json，
// 重开后条目来自备份。
func TestRecoverStateRestoresNewestBackup(t *testing.T) {
	root, _ := corruptStateDir(t)
	bak := mustState(t, []Item{{ID: "b", Kind: KindText, Text: "from backup"}})
	if err := os.WriteFile(filepath.Join(root, stateFileName+".bak"), []byte(bak), 0o600); err != nil {
		t.Fatal(err)
	}
	backups := RecoveryBackups(root)
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}
	if err := RecoverState(root, backups[0]); err != nil {
		t.Fatalf("RecoverState: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, stateFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != bak {
		t.Fatalf("restored state = %q, want the backup verbatim", data)
	}
	kept := corruptCopy(t, root)
	if string(kept) != "{ this is not json !!!" {
		t.Fatalf("the corrupt original changed: %q", kept)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatalf("reopen after recovery: %v", err)
	}
	defer s.Close()
	if len(s.Items) != 1 || s.Items[0].Text != "from backup" {
		t.Fatalf("recovered items = %+v", s.Items)
	}
}

// Open 清理的是自己命名约定里的崩溃残留（.*.tmp-* 的普通文件），
// 别的任何文件都不动。
func TestOpenRemovesCrashedTempLeftovers(t *testing.T) {
	root := t.TempDir()
	leftovers := []string{
		".state.json.tmp-123456",
		filepath.Join(imagesDirName, ".a1b2c3.png.tmp-789"),
		filepath.Join(convertedDirName, ".out.pdf.tmp-42"),
	}
	for _, name := range leftovers {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	keep := []string{
		".state.json.draft",
		".hidden-config",
		"state.json.bak",
		filepath.Join(imagesDirName, "real.png"),
	}
	for _, name := range keep {
		if err := os.WriteFile(filepath.Join(root, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, name := range leftovers {
		if _, err := os.Stat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("leftover %s survived Open: %v", name, err)
		}
	}
	for _, name := range keep {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("unrelated %s touched: %v", name, err)
		}
	}
}

// corruptStateDir seeds a data directory whose state.json cannot parse and
// returns it with the corrupt bytes for comparison.
func corruptStateDir(t *testing.T) (root, bad string) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, imagesDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	bad = "{ this is not json !!!"
	if err := os.WriteFile(filepath.Join(root, stateFileName), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil || !errors.Is(err, ErrStateUnreadable) {
		t.Fatalf("Open on corrupt state = %v, want ErrStateUnreadable", err)
	}
	return root, bad
}

// corruptCopy finds the moved-aside original and returns its bytes.
func corruptCopy(t *testing.T, root string) []byte {
	t.Helper()
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range ents {
		match, err := filepath.Match(stateFileName+".corrupt-*", ent.Name())
		if err != nil {
			t.Fatal(err)
		}
		if match {
			data, err := os.ReadFile(filepath.Join(root, ent.Name()))
			if err != nil {
				t.Fatal(err)
			}
			return data
		}
	}
	t.Fatalf("no %s.corrupt-* in %s", stateFileName, root)
	return nil
}

// mustState encodes items the way Save does.
func mustState(t *testing.T, items []Item) string {
	t.Helper()
	data, err := json.MarshalIndent(stateFile{Version: stateVersion, Items: items}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
