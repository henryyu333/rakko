//go:build darwin

package mac

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestSpotlightQueriesSearchesNamesAndText: a plain term asks the name
// index and the text index, case- and diacritic-insensitive.
func TestSpotlightQueriesSearchesNamesAndText(t *testing.T) {
	queries := SpotlightQueries("  Rakko ")
	if len(queries) != 2 {
		t.Fatalf("queries = %q, want a name and a text query", queries)
	}
	for _, want := range []string{
		`kMDItemFSName == "*Rakko*"cd`,
		`kMDItemDisplayName == "*Rakko*"cd`,
	} {
		if !strings.Contains(queries[0], want) {
			t.Fatalf("name query %q lacks %q", queries[0], want)
		}
	}
	if !strings.Contains(queries[1], `kMDItemTextContent == "*Rakko*"cd`) {
		t.Fatalf("text query = %q", queries[1])
	}
	if got := SpotlightQueries("   "); len(got) != 0 {
		t.Fatalf("a blank term ran %q", got)
	}
}

// TestSpotlightQueryEscapesLiterals: quotes, backslashes and wildcards
// stay literal in the name clauses. The text matcher ignores those
// escapes and would match everything, so such a term searches names only.
func TestSpotlightQueryEscapesLiterals(t *testing.T) {
	cases := []struct{ term, escaped string }{
		{`a"b`, `a\"b`},
		{`a\b`, `a\\b`},
		{"a*b", `a\*b`},
		{"a?b", `a\?b`},
	}
	for _, c := range cases {
		queries := SpotlightQueries(c.term)
		if len(queries) != 1 {
			t.Fatalf("term %q ran %q, want names only", c.term, queries)
		}
		if !strings.Contains(queries[0], `"*`+c.escaped+`*"cd`) {
			t.Errorf("term %q produced %q, want the literal %q", c.term, queries[0], c.escaped)
		}
	}
}

// TestSpotlightPathWantedKeepsTheUserTree: hidden files, ~/Library, caches
// and package internals never show; the home, /Applications and system
// applications do.
func TestSpotlightPathWantedKeepsTheUserTree(t *testing.T) {
	home := spotHome()
	if home == "" {
		t.Skip("no home directory")
	}
	keep := []string{
		"/Applications/Safari.app",
		"/System/Applications/Calculator.app",
		filepath.Join(home, "Documents/report.pdf"),
		filepath.Join(home, "Desktop/notes.txt"),
		"/Users/Shared/plan.pages",
	}
	for _, path := range keep {
		if !SpotlightPathWanted(path) {
			t.Errorf("%s should be a result", path)
		}
	}
	drop := []string{
		"",
		"relative/path",
		filepath.Join(home, "Library/Preferences/com.apple.finder.plist"),
		filepath.Join(home, "Documents/.hidden.txt"),
		filepath.Join(home, "Library/Caches/com.example/cache.db"),
		"/Applications/Safari.app/Contents/Info.plist",
		"/System/Library/Frameworks/AppKit.framework/Versions/C/AppKit",
		"/private/var/folders/xy/T/tmpfile",
	}
	for _, path := range drop {
		if SpotlightPathWanted(path) {
			t.Errorf("%s should be filtered out", path)
		}
	}
}

// TestSpotlightCancelIsSafe: real queries are read with their updates
// frozen and are stopped and drained before release; cancelling while the
// index is still delivering must neither crash nor wedge the query queue.
func TestSpotlightCancelIsSafe(t *testing.T) {
	if testing.Short() {
		t.Skip("real Spotlight queries")
	}
	for i := 0; i < 10; i++ {
		run := StartSpotlight("a", 50, func([]SpotlightHit) {}, nil)
		if i%2 == 1 {
			time.Sleep(5 * time.Millisecond) // land mid-delivery
		}
		run.Cancel()
	}
	// The shared queue must still answer after those cancels.
	done := make(chan struct{})
	StartSpotlight("zzz-rakko-cancel-probe", 5, func([]SpotlightHit) {}, func() { close(done) })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a query after the cancels never finished")
	}
}

// TestWarmSpotlightCancelsWhenTheIndexIsSlow: a warmup still gathering at
// its deadline gives the queue back instead of holding the user's query.
func TestWarmSpotlightCancelsWhenTheIndexIsSlow(t *testing.T) {
	oldStart, oldTimeout := spotWarmStart, spotWarmTimeout
	t.Cleanup(func() { spotWarmStart, spotWarmTimeout = oldStart, oldTimeout })
	run := &SpotlightRun{}
	spotWarmStart = func(string, int, func([]SpotlightHit), func()) *SpotlightRun { return run }
	spotWarmTimeout = time.Millisecond
	WarmSpotlight()
	deadline := time.Now().Add(2 * time.Second)
	for !run.stopped.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the slow warmup was never cancelled")
		}
		time.Sleep(time.Millisecond)
	}
}
