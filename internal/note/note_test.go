//go:build darwin

package note

import (
	"errors"
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

// writeNote puts a note file in dir's folder by hand, as an earlier build
// or another editor would.
func writeNote(t *testing.T, dir, id, text string) string {
	t.Helper()
	folder := filepath.Join(dir, DirName)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, id+FileExt)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDocsListsTheFoldersNotes: every note comes back newest first, and a
// file that is not one of the package's notes is not a note.
func TestDocsListsTheFoldersNotes(t *testing.T) {
	dir := t.TempDir()
	old := strings.Repeat("a", idLen)
	recent := strings.Repeat("b", idLen)
	writeNote(t, dir, old, "# 早的\n")
	writeNote(t, dir, recent, "# 晚的\n")
	base := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(dir, DirName, old+FileExt), base, base); err != nil {
		t.Fatal(err)
	}
	// Not notes: the current name, a name that is not an id, a strange
	// extension and a subfolder.
	folder := filepath.Join(dir, DirName)
	for name, body := range map[string]string{
		currentFile:                         recent + "\n",
		"not-a-note" + FileExt:              "# 不认识的\n",
		strings.Repeat("c", idLen) + ".txt": "# 扩展名不对\n",
		"." + old + FileExt + ".tmp-123456": "# 临时文件\n",
	} {
		if err := os.WriteFile(filepath.Join(folder, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	docs, err := Docs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs = %+v, want the two notes", docs)
	}
	if docs[0].ID != recent || docs[1].ID != old {
		t.Fatalf("order = %q, %q; want the recently written note first", docs[0].ID, docs[1].ID)
	}
	if docs[1].Text != "# 早的\n" {
		t.Fatalf("text = %q", docs[1].Text)
	}
	// A data directory with no notes/ folder has no notes, and asking
	// never creates it.
	empty := t.TempDir()
	if got, err := Docs(empty); err != nil || len(got) != 0 {
		t.Fatalf("empty folder: %v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(empty, DirName)); !os.IsNotExist(err) {
		t.Fatalf("Docs created the notes folder: %v", err)
	}
}

// TestSearchRanksHeadingBeforeBodyThenNewest: a heading hit answers before
// a body hit whatever the dates, the body hit names the line, and neither
// depends on the case typed.
func TestSearchRanksHeadingBeforeBodyThenNewest(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	docs := []Doc{
		{ID: strings.Repeat("a", idLen), Text: "# 发版清单\n– 改 README\n", Mod: base.Add(2 * time.Second)},
		{ID: strings.Repeat("b", idLen), Text: "# 周五\n– 发版前跑测试\n", Mod: base.Add(3 * time.Second)},
		{ID: strings.Repeat("c", idLen), Text: "# 发版\n", Mod: base.Add(4 * time.Second)},
		{ID: strings.Repeat("d", idLen), Text: "# 无关\n", Mod: base.Add(time.Minute)},
	}
	hits := Search(docs, "发版")
	if len(hits) != 3 {
		t.Fatalf("hits = %+v, want the three notes that mention it", hits)
	}
	// The two headings first: the newest heading, then the older one.
	if hits[0].ID != docs[2].ID || hits[1].ID != docs[0].ID {
		t.Fatalf("order = %q, %q; want the heading hits first", hits[0].ID, hits[1].ID)
	}
	if hits[0].Line != "" || hits[1].Line != "" {
		t.Fatalf("a heading hit carried a second line: %+v", hits[:2])
	}
	// Then the body hit, with the line that matched.
	if hits[2].ID != docs[1].ID || hits[2].Line != "– 发版前跑测试" {
		t.Fatalf("body hit = %+v", hits[2])
	}
	if got := Search(docs, "README"); len(got) != 1 || got[0].Line != "– 改 README" {
		t.Fatalf("case-insensitive body search = %+v", got)
	}
	if got := Search(docs, "  "); len(got) != 0 {
		t.Fatalf("blank query returned %+v", got)
	}
}

// TestSelectSwitchesTheNote: the store edits another note, its path and
// the current file follow, and an id the folder does not hold changes
// nothing.
func TestSelectSwitchesTheNote(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	first := s.ID()
	other := strings.Repeat("d", idLen)
	writeNote(t, dir, other, "# 另一张\n")
	if err := s.Select(other); err != nil {
		t.Fatal(err)
	}
	if s.ID() != other || filepath.Base(s.Path()) != other+FileExt {
		t.Fatalf("store edits %q at %q", s.ID(), s.Path())
	}
	if got, err := s.Read(); err != nil || got != "# 另一张\n" {
		t.Fatalf("read %q, %v", got, err)
	}
	cur, err := os.ReadFile(filepath.Join(dir, DirName, currentFile))
	if err != nil || strings.TrimSpace(string(cur)) != other {
		t.Fatalf("current = %q, %v; want %q", cur, err, other)
	}
	// A missing note and a name that is not an id are refused, and the
	// store still edits the note it just switched to.
	for _, id := range []string{strings.Repeat("e", idLen), "nope"} {
		if err := s.Select(id); err == nil {
			t.Fatalf("Select(%q) succeeded", id)
		}
		if s.ID() != other {
			t.Fatalf("a refused Select left the store on %q", s.ID())
		}
	}
	// Back to the first note: both files are still there.
	if err := s.Select(first); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Read(); err != nil || got != "" {
		t.Fatalf("read %q, %v; want the first note's empty text", got, err)
	}
}

// TestWatchFileArmFailureClosesEveryFD: a kqueue registration that fails
// must not leak the folder, the stop pipe or the kqueue. The watch it
// would have returned never starts a goroutine, so WatchFile itself has to
// close them; a working kqueue will not fail, so the seam takes that path.
func TestWatchFileArmFailureClosesEveryFD(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	old := watchArm
	var got *Watch
	watchArm = func(w *Watch, _ uint64, _ uint32) error {
		got = w
		return errors.New("arm failed")
	}
	t.Cleanup(func() { watchArm = old })
	if _, err := WatchFile(path, func() {}); err == nil {
		t.Fatal("a failed registration must be an error")
	}
	if got == nil {
		t.Fatal("the test never saw the watch")
	}
	if got.dir != nil || got.quit != nil || got.stop != nil || got.kq != -1 {
		t.Fatalf("fds left open: dir %v quit %v stop %v kq %d", got.dir, got.quit, got.stop, got.kq)
	}
	if !got.done {
		t.Fatal("the abandoned watch is not marked closed")
	}
}
