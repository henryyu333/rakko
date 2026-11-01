//go:build darwin

package note

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenCreatesOneNoteAndKeepsIt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.ID()) != idLen {
		t.Fatalf("id = %q, want %d hex digits", s.ID(), idLen)
	}
	if got := filepath.Base(s.Path()); got != s.ID()+FileExt {
		t.Fatalf("path = %q, want %s%s", s.Path(), s.ID(), FileExt)
	}
	if _, err := os.Stat(s.Path()); err != nil {
		t.Fatalf("note file missing: %v", err)
	}
	cur, err := os.ReadFile(filepath.Join(dir, DirName, currentFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(cur)) != s.ID() {
		t.Fatalf("current = %q, want %q", cur, s.ID())
	}
	// A second open edits the same note.
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID() != s.ID() {
		t.Fatalf("second open made a new note: %q, first %q", again.ID(), s.ID())
	}
	if err := again.Write("# 周五发版前\n– 改 README\n"); err != nil {
		t.Fatal(err)
	}
	text, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if text != "# 周五发版前\n– 改 README\n" {
		t.Fatalf("read = %q", text)
	}
}

func TestOpenStartsFreshWhenTheNoteIsGone(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := s.ID()
	if err := os.Remove(s.Path()); err != nil {
		t.Fatal(err)
	}
	again, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID() == first {
		t.Fatalf("stale id %q came back", first)
	}
	if _, err := os.Stat(again.Path()); err != nil {
		t.Fatalf("fresh note missing: %v", err)
	}
	if text, err := again.Read(); err != nil || text != "" {
		t.Fatalf("fresh note = %q, %v; want empty", text, err)
	}
}

// TestWriteReplacesTheFile proves the write is a rename, not a truncate:
// a reader holding the old file keeps the old bytes, and no temporary
// file is left behind.
func TestWriteReplacesTheFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write("old\n"); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(s.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := s.Write("new content\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	n, err := reader.ReadAt(buf, 0)
	if err != nil && n == 0 {
		t.Fatal(err)
	}
	if got := string(buf[:n]); got != "old\n" {
		t.Fatalf("the reader of the old file saw %q, so the write was not a rename", got)
	}
	text, err := s.Read()
	if err != nil {
		t.Fatal(err)
	}
	if text != "new content\n" {
		t.Fatalf("read = %q", text)
	}
	entries, err := os.ReadDir(filepath.Join(dir, DirName))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"\n\n  \n", ""},
		{"# 周五发版前\n– 改 README", "周五发版前"},
		{"###  只有标题  ", "只有标题"},
		{"普通第一行\n第二行", "普通第一行"},
		{"#\n正文", ""},
		{"\r\n# 换行\r\n– 一项\r\n", "换行"},
		{"#hashtag 不是标题前缀之外的东西", "hashtag 不是标题前缀之外的东西"},
	}
	for _, c := range cases {
		if got := Title(c.in); got != c.want {
			t.Errorf("Title(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestPreview(t *testing.T) {
	text := "# 周五发版前\n\n– 改 README 顶栏描述\n– 补 v1.13 截图\n\n– 问 Codex 额度\n– 第四行\n"
	got := Preview(text, 3)
	want := []string{"– 改 README 顶栏描述", "– 补 v1.13 截图", "– 问 Codex 额度"}
	if len(got) != len(want) {
		t.Fatalf("Preview = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Preview[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := Preview("只有一行", 3); got != nil {
		t.Fatalf("Preview of a title-only note = %q, want nil", got)
	}
	if got := Preview("", 3); got != nil {
		t.Fatalf("Preview of an empty note = %q, want nil", got)
	}
}

// TestWatchSeesSaves proves the watcher reports an atomic save (a rename
// into the folder) and an in-place rewrite of the file, and that it still
// does after the file was replaced.
func TestWatchSeesSaves(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	hits := make(chan string, 16)
	w, err := WatchFile(s.Path(), func() {
		text, _ := s.Read()
		select {
		case hits <- text:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	wait := func(want string, why string) {
		t.Helper()
		deadline := time.After(3 * time.Second)
		for {
			select {
			case got := <-hits:
				if got == want {
					return
				}
			case <-deadline:
				t.Fatalf("%s: no event carrying %q", why, want)
			}
		}
	}
	if err := s.Write("rename save\n"); err != nil {
		t.Fatal(err)
	}
	wait("rename save\n", "atomic save")

	// An editor that rewrites the file in place (no rename).
	if err := os.WriteFile(s.Path(), []byte("in place\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wait("in place\n", "in-place write")

	// The note goes away and comes back: the folder watch still reports it.
	if err := os.Remove(s.Path()); err != nil {
		t.Fatal(err)
	}
	if err := s.Write("back again\n"); err != nil {
		t.Fatal(err)
	}
	wait("back again\n", "after a delete")
}

// TestOpenKeepsANoteAnEarlierBuildWrote: the notes/ folder a previous
// version left behind — a current file naming a 32-digit id and that id's
// Markdown — opens as the same note, with its text intact. The card never
// starts a second note beside the one the user already has.
func TestOpenKeepsANoteAnEarlierBuildWrote(t *testing.T) {
	dir := t.TempDir()
	folder := filepath.Join(dir, DirName)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("ab", idLen/2)
	if err := os.WriteFile(filepath.Join(folder, currentFile), []byte(id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	text := "# 旧版写的\n– 一行\n"
	if err := os.WriteFile(filepath.Join(folder, id+FileExt), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.ID() != id {
		t.Fatalf("opened %q, want the note the file names %q", s.ID(), id)
	}
	got, err := s.Read()
	if err != nil || got != text {
		t.Fatalf("read %q, %v; want %q", got, err, text)
	}
}
