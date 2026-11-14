//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScreenshotFolderIsTheSystemsOwn checks the folder Ctrl+S writes into:
// either Windows' Screenshots folder (and it exists) or "" for the desktop
// default, never a path that cannot be written.
func TestScreenshotFolderIsTheSystemsOwn(t *testing.T) {
	dir := ScreenshotFolder()
	if dir == "" {
		return
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("ScreenshotFolder returned %q, want an absolute path", dir)
	}
	if !strings.EqualFold(filepath.Base(dir), "Screenshots") {
		t.Errorf("ScreenshotFolder returned %q, want the Screenshots folder", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("ScreenshotFolder returned %q, which is not a directory", dir)
	}
}
