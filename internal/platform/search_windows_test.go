//go:build windows

package platform

// Search checks: the query and path rules as pure functions, the shell's
// Apps folder on the test machine, and one live index query.

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestSearchSQLScreensMetacharacters(t *testing.T) {
	for _, tt := range []struct {
		term       string
		wantOK     bool
		wantLike   bool
		wantPhrase bool
	}{
		{"rakko", true, true, true},
		{"记账", true, true, true},
		{"100%", true, false, true},
		{"my_file", true, false, true},
		{`a"b`, true, true, false},
		{`a\b`, true, true, false},
		{`a%%""\\`, false, false, false},
		{"   ", false, false, false},
	} {
		sql, ok := searchSQL(tt.term)
		if ok != tt.wantOK {
			t.Errorf("%q: ok = %v, want %v (%q)", tt.term, ok, tt.wantOK, sql)
			continue
		}
		if !ok {
			continue
		}
		if got := strings.Contains(sql, `LIKE '%`); got != tt.wantLike {
			t.Errorf("%q: LIKE clause present = %v, want %v (%q)", tt.term, got, tt.wantLike, sql)
		}
		if got := strings.Contains(sql, "CONTAINS(*"); got != tt.wantPhrase {
			t.Errorf("%q: CONTAINS clause present = %v, want %v (%q)", tt.term, got, tt.wantPhrase, sql)
		}
		if !strings.HasPrefix(sql, "SELECT TOP ") || !strings.Contains(sql, "SYSTEMINDEX") {
			t.Errorf("%q: query shape is wrong: %q", tt.term, sql)
		}
	}
}

func TestSearchPathFromURL(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"file:C:/Users/Administrator/Documents/a b.txt", `C:\Users\Administrator\Documents\a b.txt`},
		{"file:C:/Users/Administrator/Documents/%E4%B8%AD.txt", `C:\Users\Administrator\Documents\中.txt`},
		{"file:///D:/data/x.zip", `D:\data\x.zip`},
		{"http://example.com/a", ""},
		{"shell:AppsFolder\\Chrome", ""},
	} {
		if got := searchPathFromURL(tt.in); got != tt.want {
			t.Errorf("searchPathFromURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSearchPathWanted(t *testing.T) {
	for _, tt := range []struct {
		path string
		want bool
	}{
		{`C:\Users\Administrator\Documents\notes.txt`, true},
		{`C:\Windows\System32\drivers\etc\hosts`, false},
		{`C:\Users\Administrator\AppData\Local\Temp\x.txt`, false},
		{`C:\Users\Administrator\AppData\Roaming\app\data.json`, false},
		{`C:\Users\Administrator\proj\node_modules\x\index.js`, false},
		{`C:\Users\Administrator\.git\config`, false},
		{`C:\Users\Administrator\Downloads\installer.tmp`, false},
		{"relative\\path.txt", false},
		{"", false},
	} {
		if got := searchPathWanted(tt.path); got != tt.want {
			t.Errorf("searchPathWanted(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

// sortedAppNames is the list's names in order, a stable answer here.
func sortedAppNames() []string {
	names := make([]string, 0)
	for _, e := range appListFresh() {
		names = append(names, e.Name)
	}
	sort.Strings(names)
	return names
}

func TestAppsFolderListsEntries(t *testing.T) {
	entries := appListFresh()
	if len(entries) == 0 {
		t.Fatal("the Apps folder listed nothing")
	}
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Name == "" || e.Target == "" {
			t.Fatalf("an entry has no name or target: %+v", e)
		}
		if strings.HasPrefix(e.Target, "::{") {
			t.Fatalf("a shell namespace item leaked into the app list: %+v", e)
		}
		if seen[e.Target] {
			t.Fatalf("the app list repeats %q", e.Target)
		}
		seen[e.Target] = true
		if strings.Contains(e.Target, `:\`) {
			// A desktop entry opens through an executable, a shortcut or a
			// command the shell runs; a document never lands in this list.
			switch strings.ToLower(filepath.Ext(e.Target)) {
			case ".exe", ".lnk", ".bat", ".cmd", ".com", ".msc":
			default:
				t.Fatalf("a desktop entry points at %q", e.Target)
			}
		}
	}
	// The names the Start Menu shows are localized; the list must not be
	// only AUMIDs.
	named := 0
	for _, e := range entries {
		if e.Name != e.Target {
			named++
		}
	}
	if named == 0 {
		t.Fatal("no entry carries a display name")
	}
	names := sortedAppNames()
	if len(names) != len(entries) {
		t.Fatalf("sortedAppNames returned %d names for %d entries", len(names), len(entries))
	}
}

func TestAppsFolderIconDecodes(t *testing.T) {
	entries := appListFresh()
	if len(entries) == 0 {
		t.Skip("no Apps folder")
	}
	for _, e := range entries {
		data := FileIcon(e.Target, 32)
		if len(data) == 0 {
			continue // some packaged entries have no shell image
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Fatalf("icon of %q: %v", e.Target, err)
		}
		return
	}
	t.Fatal("no app entry produced an icon PNG")
}

func TestStartSearchAppsDeliversAndFinishes(t *testing.T) {
	entries := appListFresh()
	if len(entries) == 0 {
		t.Skip("no Apps folder")
	}
	term := strings.TrimSpace(entries[0].Name)
	if term == "" {
		t.Skip("the first app entry has no name")
	}
	hits := make(chan []SearchHit, 8)
	done := make(chan struct{})
	run := StartSearch(term, ScopeApps, 50, func(b []SearchHit) { hits <- b }, func() { close(done) })
	defer run.Cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the app search did not finish")
	}
	close(hits)
	total := 0
	for b := range hits {
		for _, h := range b {
			if !h.App || h.Path == "" {
				t.Fatalf("an app search emitted a non-app hit: %+v", h)
			}
			total++
		}
	}
	if total == 0 {
		t.Fatalf("searching for %q found no app", term)
	}
}

func TestSearchIndexAnswers(t *testing.T) {
	// "Microsoft" is in many indexed names and contents; the point is
	// that the provider answers, not what it ranks.
	w := newSearchSink(&SearchRun{}, func([]SearchHit) {}, searchLimit)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	winCOMInit()
	if err := searchIndex("Microsoft", ScopeAll, w); err != nil {
		t.Fatalf("the index did not answer: %v", err)
	}
	if w.sent+len(w.batch) == 0 {
		t.Fatal("the index answered with no rows")
	}
}

func TestSearchScanWalksCommonFolders(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "rakko-needle.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "rakko-needle"), 0o755); err != nil {
		t.Fatal(err)
	}
	// searchScan reads the real known folders, so walk the test tree with
	// the same rules directly.
	var hits []SearchHit
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && path != root && searchSkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if strings.Contains(strings.ToLower(d.Name()), "rakko-needle") && !hiddenOrSystem(path) {
			hits = append(hits, SearchHit{Path: path, Name: d.Name(), Folder: d.IsDir()})
		}
		return nil
	})
	if len(hits) != 1 || hits[0].Folder {
		t.Fatalf("the scan rules found %+v, want the one file", hits)
	}
	if !searchSkipDir("node_modules") || !searchSkipDir("AppData") || searchSkipDir("Documents") {
		t.Fatal("searchSkipDir does not name the heavy directories")
	}
}
