package note

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreSaveAssetDeduplicatesAndValidatesPath(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("fake-png-bytes-1")
	p1, err := s.SaveAsset(payload, ".PNG")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.SaveAsset(payload, "png")
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Fatalf("dedup mismatch: %q != %q", p1, p2)
	}
	if !strings.HasPrefix(p1, AssetsDirName+"/") || !strings.HasSuffix(p1, ".png") {
		t.Fatalf("unexpected relative path: %q", p1)
	}
	entries, err := os.ReadDir(s.AssetsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated asset file, got %d", len(entries))
	}
	full, err := s.AssetPath(p1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("asset bytes = %q, want %q", got, payload)
	}
	// Traversal rejection.
	for _, bad := range []string{"", "../secret.md", "/etc/passwd", "assets/../outside.md", "other/file.png"} {
		if _, err := s.AssetPath(bad); err == nil {
			t.Fatalf("AssetPath(%q) should fail", bad)
		}
	}
}

func TestStoreSaveAssetFile(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "shot.jpg")
	if err := os.WriteFile(src, []byte("jpeg-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	rel, err := s.SaveAssetFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(rel, ".jpg") {
		t.Fatalf("rel = %q, want .jpg", rel)
	}
	bad := filepath.Join(t.TempDir(), "notes.txt")
	_ = os.WriteFile(bad, []byte("text"), 0o600)
	if _, err := s.SaveAssetFile(bad); err == nil {
		t.Fatal("expected error for non-image file")
	}
}

func TestFirstImageAndImageLineSplit(t *testing.T) {
	md := "# Title\n\n![](assets/0123456789abcdef.png)\n\n– body line 1\n"
	if got := FirstImage(md); got != "assets/0123456789abcdef.png" {
		t.Fatalf("FirstImage = %q", got)
	}
	if got := FirstImage("![alt](<assets/with space.png> \"cap\")"); got != "assets/with space.png" {
		t.Fatalf("FirstImage angled = %q", got)
	}
	if got := FirstImage("plain text [link](https://example.com)"); got != "" {
		t.Fatalf("FirstImage non-image link = %q", got)
	}
	if got := Title(md); got != "Title" {
		t.Fatalf("Title = %q, want Title", got)
	}
	lines := Preview(md, 3)
	if len(lines) != 1 || lines[0] != "– body line 1" {
		t.Fatalf("Preview = %v, want [– body line 1]", lines)
	}
	onlyImg := "![](assets/0123456789abcdef.png)\n"
	if got := Title(onlyImg); got != "" {
		t.Fatalf("Title of image-only note = %q, want empty", got)
	}
}
