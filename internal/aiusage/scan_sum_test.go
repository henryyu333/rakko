package aiusage

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func scanTotal(t *testing.T, home string) Totals {
	t.Helper()
	s := NewScanner(home, t.TempDir())
	if err := s.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return s.Summary(RangeToday, fixtureNow()).Total
}

// The page shows one number, so the combined total must be exactly the sum
// of what each reader produces on its own.
func TestTotalIsTheSumOfEveryReader(t *testing.T) {
	home := t.TempDir()
	copyTree(t, "testdata/readers/pi/home", home)
	copyTree(t, "testdata/readers/grok/home", home)
	opencodeTestDB(t, home)

	got := scanTotal(t, home)

	pi := scanTotal(t, "testdata/readers/pi/home")
	grok := scanTotal(t, "testdata/readers/grok/home")
	ocHome := t.TempDir()
	opencodeTestDB(t, ocHome)
	oc := scanTotal(t, ocHome)

	want := Totals{
		Input:      pi.Input + grok.Input + oc.Input,
		Output:     pi.Output + grok.Output + oc.Output,
		CacheRead:  pi.CacheRead + grok.CacheRead + oc.CacheRead,
		CacheWrite: pi.CacheWrite + grok.CacheWrite + oc.CacheWrite,
		Requests:   pi.Requests + grok.Requests + oc.Requests,
		Unpriced:   pi.Unpriced + grok.Unpriced + oc.Unpriced,
		Cost:       pi.Cost + grok.Cost + oc.Cost,
	}
	if got.Input != want.Input || got.Output != want.Output ||
		got.CacheRead != want.CacheRead || got.CacheWrite != want.CacheWrite ||
		got.Requests != want.Requests || got.Unpriced != want.Unpriced {
		t.Fatalf("combined total = %+v, want sum %+v", got, want)
	}
	if got.Cost < want.Cost-0.0001 || got.Cost > want.Cost+0.0001 {
		t.Fatalf("combined cost = %v, want %v", got.Cost, want.Cost)
	}
	if got.Requests != 7 {
		t.Fatalf("requests = %d, want 2 pi + 3 Grok + 2 opencode = 7", got.Requests)
	}
}
