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
	queries := SpotlightQueries("  Rakko ", ScopeAll)
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
	if got := SpotlightQueries("   ", ScopeAll); len(got) != 0 {
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
		queries := SpotlightQueries(c.term, ScopeAll)
		if len(queries) != 1 {
			t.Fatalf("term %q ran %q, want names only", c.term, queries)
		}
		if !strings.Contains(queries[0], `"*`+c.escaped+`*"cd`) {
			t.Errorf("term %q produced %q, want the literal %q", c.term, queries[0], c.escaped)
		}
	}
}

// TestSpotlightQueriesAppsAskOneBundleNameQuery: the app mode narrows the
// index to application bundles and asks their names, never their text.
func TestSpotlightQueriesAppsAskOneBundleNameQuery(t *testing.T) {
	queries := SpotlightQueries(" Rakko ", ScopeApps)
	if len(queries) != 1 {
		t.Fatalf("queries = %q, want one name query", queries)
	}
	q := queries[0]
	for _, want := range []string{
		`kMDItemContentTypeTree == "com.apple.application-bundle"c`,
		`kMDItemFSName == "*Rakko*"cd`,
		`kMDItemDisplayName == "*Rakko*"cd`,
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("app query %q lacks %q", q, want)
		}
	}
	if strings.Contains(q, "kMDItemTextContent") {
		t.Fatalf("app query asks the text index: %q", q)
	}
}

// TestSpotlightQueriesFilesExcludeBundles: the file mode keeps application
// bundles out of its name query and still asks the text index.
func TestSpotlightQueriesFilesExcludeBundles(t *testing.T) {
	queries := SpotlightQueries("Rakko", ScopeFiles)
	if len(queries) != 2 {
		t.Fatalf("queries = %q, want a name and a text query", queries)
	}
	name := queries[0]
	for _, want := range []string{
		`kMDItemContentType != "com.apple.application-bundle"`,
		`kMDItemFSName == "*Rakko*"cd`,
		`kMDItemDisplayName == "*Rakko*"cd`,
	} {
		if !strings.Contains(name, want) {
			t.Fatalf("file name query %q lacks %q", name, want)
		}
	}
	if !strings.Contains(queries[1], `kMDItemTextContent == "*Rakko*"cd`) {
		t.Fatalf("file text query = %q", queries[1])
	}
}

// TestSpotlightQueriesEscapeInEveryScope: a term carrying a quote,
// backslash or wildcard asks names only in every mode, and a blank term
// asks nothing.
func TestSpotlightQueriesEscapeInEveryScope(t *testing.T) {
	for _, scope := range []SearchScope{ScopeAll, ScopeApps, ScopeFiles} {
		queries := SpotlightQueries("a*b", scope)
		if len(queries) != 1 {
			t.Fatalf("scope %d: term a*b ran %q, want names only", scope, queries)
		}
		if !strings.Contains(queries[0], `"*a\*b*"cd`) {
			t.Errorf("scope %d: query %q lost the escaped literal", scope, queries[0])
		}
		if strings.Contains(queries[0], "kMDItemTextContent") {
			t.Errorf("scope %d: an escaped term asked the text index: %q", scope, queries[0])
		}
		if got := SpotlightQueries("   ", scope); len(got) != 0 {
			t.Errorf("scope %d: a blank term ran %q", scope, got)
		}
	}
}

// TestSearchScopeWantsItsOwnHits: the sheet keeps the query's own kind of
// hit, so an app never lands among files and a file never among apps.
func TestSearchScopeWantsItsOwnHits(t *testing.T) {
	app := SpotlightHit{Path: "/Applications/Rakko.app", Name: "Rakko", App: true}
	file := SpotlightHit{Path: "/Users/x/rakko.txt", Name: "rakko.txt"}
	folder := SpotlightHit{Path: "/Users/x/rakko", Name: "rakko", Folder: true}
	for _, c := range []struct {
		scope SearchScope
		hit   SpotlightHit
		want  bool
	}{
		{ScopeAll, app, true},
		{ScopeAll, file, true},
		{ScopeAll, folder, true},
		{ScopeApps, app, true},
		{ScopeApps, file, false},
		{ScopeApps, folder, false},
		{ScopeFiles, app, false},
		{ScopeFiles, file, true},
		{ScopeFiles, folder, true},
	} {
		if got := c.scope.wants(c.hit); got != c.want {
			t.Errorf("scope %d wants %+v = %v, want %v", c.scope, c.hit, got, c.want)
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
		run := StartSpotlight("a", ScopeAll, 50, func([]SpotlightHit) {}, nil)
		if i%2 == 1 {
			time.Sleep(5 * time.Millisecond) // land mid-delivery
		}
		run.Cancel()
	}
	// The shared queue must still answer after those cancels.
	done := make(chan struct{})
	StartSpotlight("zzz-rakko-cancel-probe", ScopeFiles, 5, func([]SpotlightHit) {}, func() { close(done) })
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
	spotWarmStart = func(string, SearchScope, int, func([]SpotlightHit), func()) *SpotlightRun { return run }
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

// TestSpotInstalledAppOnlyCountsTheApplicationsFolders: only bundles under
// the three install locations are 应用; the same .app elsewhere is a file.
func TestSpotInstalledAppOnlyCountsTheApplicationsFolders(t *testing.T) {
	home := spotHome()
	if home == "" {
		t.Skip("no home directory")
	}
	for _, path := range []string{
		"/Applications/Safari.app",
		"/System/Applications/Calculator.app",
		"/System/Applications/Utilities/Terminal.app",
		filepath.Join(home, "Applications/Local.app"),
	} {
		if !spotInstalledApp(path) {
			t.Errorf("%s should list as an application", path)
		}
	}
	for _, path := range []string{
		filepath.Join(home, "Downloads/Installer.app"),
		filepath.Join(home, "Developer/build/Debug/My.app"),
		"/Volumes/Install macOS/Install.app",
		"/Applications",
	} {
		if spotInstalledApp(path) {
			t.Errorf("%s should list as a file", path)
		}
	}
}
