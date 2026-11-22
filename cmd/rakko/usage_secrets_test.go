package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"rakko/internal/aiusage"
)

type fakeSecretBackend struct {
	data    map[string][]byte
	gets    []string
	deletes []string
	getErr  error
	delErr  error
}

func (f *fakeSecretBackend) Get(key string) ([]byte, error) {
	f.gets = append(f.gets, key)
	if f.getErr != nil {
		return nil, f.getErr
	}
	v, ok := f.data[key]
	if !ok {
		return nil, errors.New("not found")
	}
	return v, nil
}

func (f *fakeSecretBackend) Delete(key string) error {
	f.deletes = append(f.deletes, key)
	if f.delErr != nil {
		return f.delErr
	}
	delete(f.data, key)
	return nil
}

func TestUsageKeyStoreReadWriteAndPermissions(t *testing.T) {
	dir := t.TempDir()
	leg := &fakeSecretBackend{data: map[string][]byte{}}
	ks := newUsageKeyStore(dir, leg)

	// Construction never touches the legacy keychain or creates files eagerly.
	if len(leg.gets) != 0 || len(leg.deletes) != 0 {
		t.Fatalf("newUsageKeyStore touched legacy backend at init: gets=%v dels=%v", leg.gets, leg.deletes)
	}
	if _, err := os.Stat(filepath.Join(dir, usageKeysFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file created eagerly at init: %v", err)
	}

	if err := ks.Set(aiusage.ToolDeepSeek, "  sk-deepseek-123  "); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	if got := ks.Get(aiusage.ToolDeepSeek); got != "sk-deepseek-123" {
		t.Fatalf("Get = %q, want %q", got, "sk-deepseek-123")
	}
	if len(leg.gets) != 0 {
		t.Fatalf("Get hit legacy keychain when key existed in file: %v", leg.gets)
	}

	// Verify file mode on Unix and no leftover temp files.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != usageKeysFileName {
		t.Fatalf("unexpected files in dataDir (stale tmp?): %v", entries)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, usageKeysFileName))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file permissions = %04o, want 0600", perm)
		}
	}
}

func TestUsageKeyStoreMigratesOnceFromLegacyKeychain(t *testing.T) {
	dir := t.TempDir()
	leg := &fakeSecretBackend{data: map[string][]byte{
		"aiusage-glm": []byte("glm-legacy-secret"),
	}}
	ks := newUsageKeyStore(dir, leg)

	// First lookup for GLM reads legacy once, writes 0600 file, deletes keychain item.
	if got := ks.Get(aiusage.ToolGLM); got != "glm-legacy-secret" {
		t.Fatalf("first Get(GLM) = %q, want glm-legacy-secret", got)
	}
	if len(leg.gets) != 1 || leg.gets[0] != "aiusage-glm" {
		t.Fatalf("legacy gets = %v, want [aiusage-glm]", leg.gets)
	}
	if len(leg.deletes) != 1 || leg.deletes[0] != "aiusage-glm" {
		t.Fatalf("legacy deletes = %v, want [aiusage-glm]", leg.deletes)
	}

	// Second lookup (even on a brand-new store instance) never touches keychain.
	ks2 := newUsageKeyStore(dir, leg)
	if got := ks2.Get(aiusage.ToolGLM); got != "glm-legacy-secret" {
		t.Fatalf("reload Get(GLM) = %q, want glm-legacy-secret", got)
	}
	if len(leg.gets) != 1 {
		t.Fatalf("subsequent lookup re-read legacy keychain: %v", leg.gets)
	}

	// Missing tool hits legacy at most once per session.
	if got := ks2.Get(aiusage.ToolKimi); got != "" {
		t.Fatalf("Get(Kimi) = %q, want empty", got)
	}
	if got := ks2.Get(aiusage.ToolKimi); got != "" {
		t.Fatalf("second Get(Kimi) = %q, want empty", got)
	}
	if len(leg.gets) != 2 {
		t.Fatalf("missing tool probed legacy multiple times in one session: %v", leg.gets)
	}
}

func TestUsageKeyStoreCorruptFileAndDelete(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, usageKeysFileName)
	leg := &fakeSecretBackend{data: map[string][]byte{
		"aiusage-cursor": []byte("cur-123"),
	}}
	ks := newUsageKeyStore(dir, leg)

	for _, garbage := range []string{"{not-json", "null", "[1,2,3]", ""} {
		if err := os.WriteFile(keyPath, []byte(garbage), 0o600); err != nil {
			t.Fatal(err)
		}
		ksNoLegacy := newUsageKeyStore(dir, nil)
		if got := ksNoLegacy.Get(aiusage.ToolDeepSeek); got != "" {
			t.Fatalf("corrupt %q returned %q instead of empty", garbage, got)
		}
	}

	// Saving overwrites corrupt JSON cleanly; deleting clears both file and legacy.
	if err := os.WriteFile(keyPath, []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ks.Set(aiusage.ToolCursor, "new-cur"); err != nil {
		t.Fatalf("Set after corrupt file failed: %v", err)
	}
	if got := ks.Get(aiusage.ToolCursor); got != "new-cur" {
		t.Fatalf("Get after Set = %q, want new-cur", got)
	}
	if err := ks.Set(aiusage.ToolCursor, ""); err != nil {
		t.Fatalf("Set empty failed: %v", err)
	}
	if _, stillInLegacy := leg.data["aiusage-cursor"]; stillInLegacy {
		t.Fatal("clearing key left old keychain entry intact (could resurrect on next launch)")
	}
	raw, err := os.ReadFile(keyPath)
	if err != nil || strings.Contains(string(raw), "new-cur") {
		t.Fatalf("key lingered in file after delete: raw=%q err=%v", raw, err)
	}
}

func TestUsageKeyStoreKeepsLegacyOnWriteError(t *testing.T) {
	// Point path under an existing regular file so directory/temp creation fails cleanly.
	blockingFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	leg := &fakeSecretBackend{data: map[string][]byte{
		"aiusage-kimi": []byte("kimi-secret"),
	}}
	ks := newUsageKeyStore(blockingFile, leg)
	if got := ks.Get(aiusage.ToolKimi); got != "kimi-secret" {
		t.Fatalf("Get = %q, want kimi-secret", got)
	}
	if len(leg.deletes) != 0 {
		t.Fatalf("deleted legacy keychain entry despite file write failure: %v", leg.deletes)
	}
}
