//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestExtractWithSystemTool exercises the real tar.exe: a listing, a
// successful extraction into a staging directory, and a failure whose
// message comes from stderr rather than the stdout listing.
func TestExtractWithSystemTool(t *testing.T) {
	if _, err := os.Stat(systemTar()); err != nil {
		t.Skipf("tar.exe not at %s: %v", systemTar(), err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "a.tar")
	if out, err := ExtractWithSystemTool([]string{"-cf", archive, "-C", dir, "hello.txt"}, nil); err != nil {
		t.Fatalf("create: %v (%s)", err, out)
	}
	out, err := ExtractWithSystemTool([]string{"-tf", archive}, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if strings.TrimSpace(string(out)) != "hello.txt" {
		t.Errorf("list = %q", out)
	}
	stage := filepath.Join(dir, "stage")
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractWithSystemTool([]string{"-xf", archive, "-C", stage}, nil); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(stage, "hello.txt"))
	if err != nil || string(got) != "hello" {
		t.Errorf("extracted = %q, %v", got, err)
	}

	// A corrupted archive must fail with the tool's stderr, never with the
	// member names a listing would have printed.
	bad := filepath.Join(dir, "bad.tar")
	if err := os.WriteFile(bad, []byte("not an archive at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = ExtractWithSystemTool([]string{"-tf", bad}, nil)
	if err == nil {
		t.Fatal("listing a corrupted archive succeeded")
	}
	if strings.Contains(err.Error(), "not an archive at all") {
		t.Errorf("error echoed the archive bytes: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("stdout carried bytes on failure: %q", out)
	}
}
