package main

// The downloads scanner is shared by the Mac and the Windows app: Chrome,
// Edge and Firefox leave the same `.crdownload`/`.part` files behind on
// both, so these tests drive it against a temporary folder on either
// platform. Safari's `.download` bundle is the darwin-only addition and is
// covered in live_test.go.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"rakko/internal/settings"
)

// writePartial creates a browser's temporary file holding n bytes.
func writePartial(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

// touchPartial stamps a temporary file as if the browser had written it at
// the given time, which is the only thing the staleness rule reads.
func touchPartial(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// partialNames indexes a scan by the final file name.
func partialNames(files []downloadFile) map[string]downloadFile {
	out := make(map[string]downloadFile, len(files))
	for _, f := range files {
		out[f.name] = f
	}
	return out
}

// A download that has just started is in flight before any byte lands:
// Chrome creates the temporary file with its first write, and a slow server
// can leave it empty for several seconds, so the wings must show it.
func TestDownloadScanShowsAPartialThatJustStarted(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	chrome := filepath.Join(dir, "未确认 545995.crdownload")
	writePartial(t, chrome, 0)
	touchPartial(t, chrome, now.Add(-2*time.Second))
	firefox := filepath.Join(dir, "linux.iso.part")
	writePartial(t, firefox, 0)
	touchPartial(t, firefox, now)

	files, err := scanDownloadDir(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	got := partialNames(files)
	if len(got) != 2 {
		t.Fatalf("scan found %d downloads: %+v", len(got), files)
	}
	for name, f := range got {
		if f.done != 0 {
			t.Fatalf("%q holds %d bytes, want the empty temporary file", name, f.done)
		}
	}
	if f, ok := got["未确认 545995"]; !ok || downloadMegabytes(f.done) != "0 B" {
		t.Fatalf("a running download with no bytes yet reads %q: %+v",
			downloadMegabytes(f.done), got)
	}
}

// A browser that is killed mid-download leaves its temporary file behind:
// the 0-byte `.crdownload` from months ago must not hold the wings on "0 B",
// while the file a browser writes right now stays in flight.
func TestDownloadScanDropsTheAbandonedZeroBytePartial(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	stale := now.Add(-120 * 24 * time.Hour)
	abandoned := filepath.Join(dir, "未确认 545995.crdownload")
	writePartial(t, abandoned, 0)
	touchPartial(t, abandoned, stale)
	stalled := filepath.Join(dir, "linux.iso.part")
	writePartial(t, stalled, 512)
	touchPartial(t, stalled, stale)
	running := filepath.Join(dir, "movie.mp4.crdownload")
	writePartial(t, running, 700)
	touchPartial(t, running, now.Add(-time.Second))

	files, err := scanDownloadDir(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].name != "movie.mp4" {
		t.Fatalf("scan kept %+v, want only the running download", files)
	}

	// 继续 in Chrome writes to the same file again: the modification time
	// moves and the download is in flight again.
	touchPartial(t, abandoned, now)
	files, err = scanDownloadDir(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	got := partialNames(files)
	if len(got) != 2 {
		t.Fatalf("the resumed download did not come back: %+v", files)
	}
	if _, ok := got["未确认 545995"]; !ok {
		t.Fatalf("the resumed download is missing: %+v", files)
	}
	if _, ok := got["linux.iso"]; ok {
		t.Fatalf("the untouched Firefox partial came back: %+v", files)
	}
}

// partialInFlight is the whole staleness rule, so its edges are its own
// test: a fresh write, an abandoned one, a timestamp the platform could not
// report, and a stamp ahead of the scan's clock.
func TestPartialInFlightEdges(t *testing.T) {
	now := time.Date(2026, time.November, 17, 20, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		mod  time.Time
		want bool
	}{
		{"written now", now, true},
		{"written a second from now", now.Add(time.Second), true},
		{"written four months ago", now.Add(-120 * 24 * time.Hour), false},
		{"written just inside the window", now.Add(-downloadStaleAfter + time.Minute), true},
		{"written just outside the window", now.Add(-downloadStaleAfter - time.Minute), false},
		{"no timestamp", time.Time{}, true},
		{"stamped two days ahead", now.Add(48 * time.Hour), false},
	}
	for _, tc := range cases {
		if got := partialInFlight(tc.mod, now); got != tc.want {
			t.Errorf("%s: partialInFlight = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The wings, not just the scan: an abandoned temporary file leaves the
// download activity off, and a download that has just started turns it on,
// reading "0 B" until the first bytes land.
func TestDownloadsModuleIgnoresAnAbandonedZeroBytePartial(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	abandoned := filepath.Join(dir, "未确认 545995.crdownload")
	writePartial(t, abandoned, 0)
	touchPartial(t, abandoned, now.Add(-120*24*time.Hour))

	a := &shelf{settings: settings.Default()}
	m := &downloadsModule{
		host: a, dir: func() (string, error) { return dir, nil }, scan: scanDownloadDir,
		allowed: func() bool { return true },
		now:     time.Now, prev: map[string]downloadFile{},
	}
	m.tick(now)
	if m.active() {
		t.Fatalf("the abandoned partial holds the wings: %+v", m.items)
	}
	if got := m.progress(); got.file.name != "" || got.count != 0 {
		t.Fatalf("progress on an abandoned partial = %+v", got)
	}

	// The user starts a download and Chrome creates the temporary file at
	// once; the scan after it appears owns the wings although it is empty.
	fresh := filepath.Join(dir, "notes.txt.crdownload")
	writePartial(t, fresh, 0)
	touchPartial(t, fresh, now)
	m.next = time.Time{}
	m.tick(now.Add(2 * time.Second))
	if !m.active() {
		t.Fatalf("the new download is not active: %+v", m.items)
	}
	f := m.progress().file
	if f.name != "notes.txt" || f.done != 0 {
		t.Fatalf("progress = %+v", f)
	}
	if got := downloadMegabytes(f.done); got != "0 B" {
		t.Fatalf("the chip reads %q, want 0 B", got)
	}
}
