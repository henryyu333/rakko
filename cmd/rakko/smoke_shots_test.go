//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// shotDirs gives every test and segment its one folder for the whole test.
var shotDirs sync.Map

// smokeShotDir is a native smoke test's evidence folder: RAKKO_SHOTS/<seg>
// when a run asks for files, and an auto-cleaned temp folder otherwise, so
// a plain test run leaves nothing behind.
func smokeShotDir(t *testing.T, seg string) string {
	t.Helper()
	if root := os.Getenv("RAKKO_SHOTS"); root != "" {
		return filepath.Join(root, seg)
	}
	key := [2]any{t, seg}
	if dir, ok := shotDirs.Load(key); ok {
		return dir.(string)
	}
	dir := t.TempDir()
	shotDirs.Store(key, dir)
	t.Cleanup(func() { shotDirs.Delete(key) })
	return dir
}
