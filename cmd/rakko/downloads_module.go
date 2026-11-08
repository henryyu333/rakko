//go:build darwin

package main

// Browser downloads in ~/Downloads: Safari's `.download` bundles (their
// Info.plist carries the bytes received and the total), and Chrome/Edge/
// Brave `.crdownload` and Firefox `.part` files (their size is what has
// arrived; the total is not published). A running download holds the notch
// wings; when the temporary file disappears and the final file exists, the
// completion capsule names it and a click puts it into the tray.
//
// Reading the folder needs Full Disk Access. The probe runs before the
// first directory read, so an ungranted app never touches ~/Downloads and
// therefore never triggers the system's folder prompt: downloads simply
// do nothing.

import (
	"encoding/xml"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

const (
	downloadPollInterval = time.Second
	// fullDiskProbeInterval is how often the Full Disk Access probe (which
	// opens the TCC database) is repeated. Granting it in System Settings
	// starts the watcher within half a minute.
	fullDiskProbeInterval = 30 * time.Second
	// downloadScanFailureLimit is how many consecutive read errors of an
	// unknown kind are tolerated before the wings are paused. A missing or
	// non-directory Downloads folder pauses at once.
	downloadScanFailureLimit = 3
	// downloadPauseMax bounds how long a pause may keep its baseline: a
	// folder that was unreadable that long may have seen dozens of
	// unrelated completions, so the first scan re-baselines in silence
	// instead of naming files the user finished minutes ago.
	downloadPauseMax = 2 * time.Minute
)

// downloadsDir is where browsers leave their temporary files. It is a seam
// so tests read their own directory and the app never touches the user's.
var downloadsDir = func() (string, error) {
	home, err := osUserHomeDir()
	if err != nil {
		return "", err
	}
	return filepathJoin(home, "Downloads"), nil
}

// Indirections kept tiny so the module can be tested without the home
// directory.
var (
	osUserHomeDir = func() (string, error) { return os.UserHomeDir() }
	filepathJoin  = filepath.Join
)

// downloadsModule watches one folder and projects it onto the notch.
type downloadsModule struct {
	host *shelf
	dir  func() (string, error)
	// scan, allowed and plist are seams: tests feed a temporary folder and
	// never probe TCC.
	scan    func(string) ([]downloadFile, error)
	allowed func() bool
	plist   func(string) (map[string]mac.PlistValue, bool)
	now     func() time.Time
	next    time.Time

	probeAt time.Time
	canRead bool

	items    []downloadFile
	prev     map[string]downloadFile
	seeded   bool
	failures int
	// pausedAt is when the current string of failed scans began. The wings
	// are cleared but the baseline is kept, so a completion that happened
	// while the folder was unreadable still gets its notice.
	pausedAt time.Time
}

func newDownloadsModule(a *shelf) *downloadsModule {
	return &downloadsModule{
		host: a, dir: downloadsDir, scan: scanDownloadDir,
		allowed: mac.FullDiskAccessGranted, plist: readPlist,
		now: time.Now, prev: map[string]downloadFile{},
	}
}

func (m *downloadsModule) tick(now time.Time) {
	if now.Before(m.next) {
		return
	}
	m.next = now.Add(downloadPollInterval)
	if !m.host.settings.LiveActivities.Downloads {
		m.reset()
		return
	}
	// Probe first, read second: a missing grant must stop every directory
	// access, not merely suppress what it finds.
	if m.probeAt.IsZero() || now.Sub(m.probeAt) >= fullDiskProbeInterval {
		m.probeAt = now
		m.canRead = m.allowed == nil || m.allowed()
	}
	if !m.canRead {
		m.pause(now)
		return
	}
	dir, err := m.dir()
	if err != nil || dir == "" {
		m.pause(now)
		return
	}
	files, err := m.scan(dir)
	if err != nil {
		// One transient error may pass; a folder that stays unreadable must
		// not leave the wings frozen on an old download forever.
		if m.pausedAt.IsZero() {
			m.pausedAt = now
		}
		m.failures++
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) ||
			m.failures >= downloadScanFailureLimit {
			m.pause(now)
		}
		return // never diff against a failed scan: no false 已下载
	}
	m.failures = 0
	files = sortDownloads(files)
	cur := make(map[string]downloadFile, len(files))
	for _, f := range files {
		cur[f.temp] = f
	}
	if m.seeded {
		// The pause's first good scan diffs against the kept baseline:
		// files that finished while the folder was unreadable are results
		// the user has not seen yet. A pause longer than downloadPauseMax
		// re-baselines silently instead — those completions are old news.
		if paused := m.pausedAt; paused.IsZero() || now.Sub(paused) <= downloadPauseMax {
			// Collected first and ordered by completion time (then target),
			// so one scan's completions form one notice in a stable order.
			var done []downloadFile
			for temp, old := range m.prev {
				if _, still := cur[temp]; still {
					continue
				}
				if old.target == "" || !fileExists(old.target) {
					continue
				}
				done = append(done, old)
			}
			sort.SliceStable(done, func(i, j int) bool {
				if !done[i].mod.Equal(done[j].mod) {
					return done[i].mod.Before(done[j].mod)
				}
				return done[i].target < done[j].target
			})
			if len(done) > 0 {
				paths := make([]string, 0, len(done))
				for _, old := range done {
					paths = append(paths, old.target)
				}
				m.host.pushDownloaded(paths, now)
			}
		}
	}
	m.pausedAt = time.Time{}
	m.seeded = true
	m.prev = cur
	changed := !sameDownloads(m.items, files)
	m.items = files
	if changed && m.host.win != nil {
		m.host.win.Invalidate()
	}
}

// pause stops showing the in-flight downloads but keeps what the next scan
// must be compared with: the folder may be unreadable for a while, and a
// download that finished inside that window is still a result worth
// announcing. Only the switch going off forgets the baseline (reset).
func (m *downloadsModule) pause(now time.Time) {
	if m.pausedAt.IsZero() {
		m.pausedAt = now
	}
	if len(m.items) == 0 {
		return
	}
	m.items = nil
	if m.host.win != nil {
		m.host.win.Invalidate()
	}
}

// reset forgets everything in flight: used when the switch is off, so
// re-enabling starts from a fresh baseline without firing completions for
// downloads that ran meanwhile.
func (m *downloadsModule) reset() {
	if len(m.items) > 0 && m.host.win != nil {
		m.host.win.Invalidate()
	}
	m.items, m.prev, m.seeded = nil, map[string]downloadFile{}, false
	m.failures = 0
	m.pausedAt = time.Time{}
}

// active reports whether a download currently owns the wings.
func (m *downloadsModule) active() bool {
	return len(m.items) > 0 && m.host.settings.LiveActivities.Downloads
}

// progress is the newest download and how many others run behind it.
func (m *downloadsModule) progress() downloadProgress {
	if len(m.items) == 0 {
		return downloadProgress{}
	}
	return downloadProgress{file: m.items[0], count: len(m.items) - 1}
}

// live is the panel's left-ear chip while a download runs.
func (m *downloadsModule) live() bool { return m.active() }

// chip draws the same 22pt capsule as a conversion job: the progress ring
// and its number.
func (m *downloadsModule) chip(c *ui.Context) ui.Element {
	dl := m.progress()
	f := dl.file
	text := downloadPercent(f)
	if f.total <= 0 {
		text = downloadMegabytes(f.done)
	}
	label := loc(c).DownloadChip(f.name, text)
	chip := ui.Row(c.Key("dl-chip")).Height(chipH).Radius(chipH/2).Background(fill1).
		Padding(0, 3, 0, 7).Gap(4).AlignItems(ui.Center).Label(label).Tooltip(label)
	chip.Children(func() {
		ring := ui.Box(c.Key("dl-chip-ring")).Size(16, 16)
		spin := float32(0)
		if f.total <= 0 {
			spin = ring.Loop("dl-chip-spin", time.Second, ui.Linear)
		}
		ring.Draw(func(p *ui.Painter, r ui.Rect) {
			cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, float32(6)
			var track ui.Path
			track.Circle(cx, cy, rad)
			p.StrokePath(&track, 2, fill3)
			if f.total > 0 {
				frac := min(max(float32(f.done)/float32(f.total), 0), 1)
				liveArc(p, cx, cy, rad, 0.75, frac, 2, accent)
			} else {
				liveArc(p, cx, cy, rad, spin, 0.28, 2, accent)
			}
		})
		ui.Text(c, text).SingleLine().FontSize(9.5).FontWeight(600).
			FontFeatures("tnum").TextColor(ink)
	})
	return chip
}

// sortDownloads puts the newest first; the ties follow the name so the
// choice never flickers between frames.
func sortDownloads(files []downloadFile) []downloadFile {
	sort.SliceStable(files, func(i, j int) bool {
		if !files[i].mod.Equal(files[j].mod) {
			return files[i].mod.After(files[j].mod)
		}
		return files[i].name < files[j].name
	})
	return files
}

func sameDownloads(a, b []downloadFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].temp != b[i].temp || a[i].done != b[i].done || a[i].total != b[i].total {
			return false
		}
	}
	return true
}

var fileExists = func(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// scanDownloadDir lists the downloads in flight.
func scanDownloadDir(dir string) ([]downloadFile, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []downloadFile
	for _, e := range ents {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		switch {
		case strings.HasSuffix(name, ".download") && e.IsDir():
			if f, ok := safariDownload(path); ok {
				out = append(out, f)
			}
		case strings.HasSuffix(name, ".crdownload") || strings.HasSuffix(name, ".part"):
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			suffix := ".crdownload"
			if strings.HasSuffix(name, ".part") {
				suffix = ".part"
			}
			target := filepath.Join(dir, strings.TrimSuffix(name, suffix))
			out = append(out, downloadFile{
				temp: path, name: filepath.Base(target), target: target,
				done: info.Size(), mod: info.ModTime(),
			})
		}
	}
	return out, nil
}

// safariDownload reads one Safari `.download` bundle. Its Info.plist names
// the bytes so far, the total to load and the final path; when the keys are
// missing the bundle name is still a usable target.
func safariDownload(bundle string) (downloadFile, bool) {
	vals, ok := readPlist(filepath.Join(bundle, "Info.plist"))
	if !ok {
		return downloadFile{}, false
	}
	num := func(key string) int64 {
		if v, ok := vals[key]; ok && v.IsNumber {
			return v.Number
		}
		return 0
	}
	target := ""
	if v, ok := vals["DownloadEntryPath"]; ok {
		target = v.Text
	}
	if target == "" {
		base := strings.TrimSuffix(bundle, ".download")
		target = base
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(bundle), target)
	}
	var mod time.Time
	if st, err := os.Stat(filepath.Join(bundle, "Info.plist")); err == nil {
		mod = st.ModTime()
	} else if st, err := os.Stat(bundle); err == nil {
		mod = st.ModTime()
	}
	return downloadFile{
		temp: bundle, name: filepath.Base(target), target: target,
		done:  num("DownloadEntryProgressBytesSoFar"),
		total: num("DownloadEntryProgressTotalToLoad"),
		mod:   mod,
	}, true
}

// readPlist reads a property list as the platform dictionary reader does,
// with a first pass in the standard library: writing a temporary XML
// plist is how tests exercise the download path without ObjC.
var readPlist = func(path string) (map[string]mac.PlistValue, bool) {
	if vals, ok := readXMLPlist(path); ok {
		return vals, true
	}
	return mac.PlistValues(path)
}

// readXMLPlist is a minimal reader for the top-level dictionary of an XML
// property list (integers, strings and booleans). Everything else yields
// ok == false so the platform reader can try binary plists.
func readXMLPlist(path string) (map[string]mac.PlistValue, bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	out := map[string]mac.PlistValue{}
	key := ""
	depth := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "key":
				var s string
				if dec.DecodeElement(&s, &t) != nil {
					return nil, false
				}
				key = s
			case "dict":
				depth++
			case "string", "integer", "real", "true", "false":
				if depth != 1 || key == "" {
					continue
				}
				switch t.Name.Local {
				case "string":
					var s string
					if dec.DecodeElement(&s, &t) != nil {
						return nil, false
					}
					out[key] = mac.PlistValue{Text: s}
				case "integer":
					var s string
					if dec.DecodeElement(&s, &t) != nil {
						return nil, false
					}
					n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
					if err != nil {
						return nil, false
					}
					out[key] = mac.PlistValue{Number: n, IsNumber: true}
				case "real":
					var s string
					if dec.DecodeElement(&s, &t) != nil {
						return nil, false
					}
					n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
					if err != nil {
						return nil, false
					}
					out[key] = mac.PlistValue{Number: int64(n), IsNumber: true}
				case "true", "false":
					v := int64(0)
					if t.Name.Local == "true" {
						v = 1
					}
					out[key] = mac.PlistValue{Number: v, IsNumber: true}
				}
				key = ""
			}
		case xml.EndElement:
			if t.Name.Local == "dict" {
				depth--
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// nextTick is the next folder scan.
func (m *downloadsModule) nextTick(now time.Time) time.Time {
	if m.next.IsZero() {
		return now
	}
	return m.next
}
