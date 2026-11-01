//go:build darwin

package mac

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSpotlightQuerySearchesNamesAndText: a plain term asks for the name
// and the document text, case- and diacritic-insensitive.
func TestSpotlightQuerySearchesNamesAndText(t *testing.T) {
	q := SpotlightQuery("  Rakko ")
	for _, want := range []string{
		`kMDItemFSName == "*Rakko*"cd`,
		`kMDItemDisplayName == "*Rakko*"cd`,
		`kMDItemTextContent == "*Rakko*"cd`,
	} {
		if !strings.Contains(q, want) {
			t.Fatalf("query %q lacks %q", q, want)
		}
	}
	if SpotlightQuery("   ") != "" {
		t.Fatal("a blank term must not run a query")
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
		q := SpotlightQuery(c.term)
		if !strings.Contains(q, `"*`+c.escaped+`*"cd`) {
			t.Errorf("term %q produced %q, want the literal %q", c.term, q, c.escaped)
		}
		if strings.Contains(q, "kMDItemTextContent") {
			t.Errorf("term %q kept a text clause: %q", c.term, q)
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
