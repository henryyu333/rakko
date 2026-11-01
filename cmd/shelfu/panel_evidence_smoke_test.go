//go:build darwin

package main

// Evidence for the P shell: real-window screenshots of both levels and the
// three animated transitions, a per-frame size CSV, and side-by-side
// comparisons with the B prototype at the same size. Run with
// SHELF_BASKET_SMOKE=1; everything lands in /tmp/shelf-panelb.

import (
	"context"
	"encoding/csv"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

const panelEvidenceDir = "/tmp/shelf-panelb"

func TestSmokePanelEvidence(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to capture the panel evidence")
	}
	if err := os.MkdirAll(panelEvidenceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := redesignShelf(t)
	files := t.TempDir()
	for i := range 4 {
		p := writeFile(t, files, fmt.Sprintf("截图 %d.png", i), testPNGBytes(t))
		if err := a.store.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 8 {
		if err := a.store.AddText(fmt.Sprintf("文件 %d · 一段用于托盘的说明文字", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 10 {
		if err := a.store.AddClipText(fmt.Sprintf("剪贴板内容 %d · 一段足够长的文字用于检查省略", i), "com.apple.Safari"); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	var f *usageFeature
	onMain(func() {
		a.settings = settings.Default()
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.modules = []module{a.np}
		f = newUsageFeature(a, usageDeps{
			fetchCodex:  func(context.Context) aiusage.Quota { return okQuota(aiusage.ToolCodex, "Plus", now) },
			fetchClaude: func(context.Context) aiusage.Quota { return okQuota(aiusage.ToolClaude, "Pro", now) },
			pace:        aiusage.Pace,
			newScanner: func() usageScanner {
				return &fakeUsageScanner{summaries: evidenceSummaries(now)}
			},
			now: func() time.Time { return now },
		})
		a.settings.AIUsage = settings.AIUsageSettings{Enabled: true, QuotaCodex: true, QuotaClaude: true}
		a.registerPage(f)
		a.setTab(pageTray)
	})

	// --- real-window shots -------------------------------------------------
	onMain(func() {
		a.place(mygo.Screen.CursorScreenPoint())
		a.peeking = true
		a.peekProgress, a.peekFrom = 1, 1
		a.show(false)
		a.level = 1
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		a.syncWindowFrame()
		a.win.Invalidate()
	})
	time.Sleep(500 * time.Millisecond)
	keepShot(t, shotNotch(t, a, "island-music"), "island-music")

	onMain(func() {
		a.settings.IslandNowPlaying = false
		a.np = nil
		a.modules = nil
		a.win.Invalidate()
	})
	time.Sleep(250 * time.Millisecond)
	keepShot(t, shotNotch(t, a, "island-counts"), "island-counts")

	onMain(func() {
		a.settings.IslandNowPlaying = true
		fakeTrack(a, true)
		a.modules = []module{a.np}
		a.setLevel(2)
	})
	time.Sleep(600 * time.Millisecond)
	keepShot(t, shotNotch(t, a, "sheet-tray"), "sheet-tray")

	onMain(func() { a.setTab(pageClipboard) })
	time.Sleep(400 * time.Millisecond)
	keepShot(t, shotNotch(t, a, "sheet-clipboard"), "sheet-clipboard")

	// AI 用量: today and 30 days, with both quotas and a local summary.
	onMain(func() { a.setTab(pageUsage) })
	time.Sleep(600 * time.Millisecond)
	onMain(func() { f.tick(now) })
	time.Sleep(400 * time.Millisecond)
	keepShot(t, shotNotch(t, a, "sheet-usage-today"), "sheet-usage-today")

	onMain(func() {
		f.rangeSel = aiusage.Range30d
		f.tick(now)
		a.win.Invalidate()
	})
	time.Sleep(500 * time.Millisecond)
	keepShot(t, shotNotch(t, a, "sheet-usage-30d"), "sheet-usage-30d")

	// A file drag over the panel: the drop zone inside the fixed box.
	t.Setenv("SHELF_DEBUG_DROP_FILES", filepath.Join(files, "截图 0.png"))
	onMain(func() {
		a.setTab(pageTray)
		a.dragging = true
		a.win.Invalidate()
	})
	time.Sleep(400 * time.Millisecond)
	keepShot(t, shotNotch(t, a, "sheet-drop"), "sheet-drop")
	onMain(func() {
		a.dragging = false
		a.drop = dropZoneState{}
		a.win.Invalidate()
	})
	t.Setenv("SHELF_DEBUG_DROP_FILES", "")

	// --- transition CSV ---------------------------------------------------
	var rows [][]string
	rows = append(rows, []string{"phase", "ms", "shell_w", "shell_h", "frame_w", "frame_h", "open_k", "level_k"})
	sample := func(phase string, start time.Time) {
		var w, h, fw, fh float32
		var ok, lk int
		onMain(func() {
			w, h = a.fx.w, a.fx.h
			fw, fh = float32(a.frame.Width), float32(a.frame.Height)
			ok = int(a.fx.openK*1000 + 0.5)
			lk = int(a.fx.levelK*1000 + 0.5)
		})
		rows = append(rows, []string{
			phase, strconv.Itoa(int(time.Since(start).Milliseconds())),
			fmt.Sprintf("%.1f", w), fmt.Sprintf("%.1f", h),
			fmt.Sprintf("%.0f", fw), fmt.Sprintf("%.0f", fh),
			fmt.Sprintf("%.3f", float64(ok)/1000), fmt.Sprintf("%.3f", float64(lk)/1000),
		})
	}
	run := func(phase string, action func(), dur time.Duration) {
		start := time.Now()
		onMain(action)
		for time.Since(start) < dur {
			sample(phase, start)
			time.Sleep(8 * time.Millisecond)
		}
		// One sample at rest: the frame drops its spring room once the
		// shell has settled.
		time.Sleep(300 * time.Millisecond)
		sample(phase, start)
	}
	// Fold to idle first.
	onMain(func() { a.hide(hideExplicit) })
	time.Sleep(400 * time.Millisecond)
	run("open", func() { a.show(false) }, 500*time.Millisecond)
	run("island-to-sheet", func() { a.setLevel(2) }, 500*time.Millisecond)
	run("close", func() { a.hide(hideExplicit) }, 500*time.Millisecond)

	csvFile, err := os.Create(filepath.Join(panelEvidenceDir, "frames.csv"))
	if err != nil {
		t.Fatal(err)
	}
	if err := csv.NewWriter(csvFile).WriteAll(rows); err != nil {
		t.Fatal(err)
	}
	csvFile.Close()

	// --- headless renders + prototype comparisons ------------------------
	renderEvidence(t, now)
	onMain(func() {
		a.hide(hidePassive)
		a.settings.AIUsage.Enabled = false
		a.usage = nil
	})
}

// renderEvidence draws the same pages headless at their true size and
// composes each next to the prototype's B shell.
func renderEvidence(t *testing.T, now time.Time) {
	t.Helper()
	stubUsageSeams(t, false)
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	files := t.TempDir()
	for i := range 3 {
		p := writeFile(t, files, fmt.Sprintf("截图 %d.png", i), testPNGBytes(t))
		if err := s.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 8 {
		if err := s.AddText(fmt.Sprintf("文件 %d · 一段用于托盘的说明文字", i)); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 10 {
		if err := s.AddClipText(fmt.Sprintf("剪贴板内容 %d · 一段足够长的文字用于检查省略", i), "com.apple.Safari"); err != nil {
			t.Fatal(err)
		}
	}
	a := &shelf{store: s, dataDir: t.TempDir(), level: 1, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{}}
	a.settings.IslandNowPlaying = true
	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Playing: true, Title: "座右铭", Artist: "吴雨霏", App: "com.apple.Music", Duration: 243, Elapsed: 112, Rate: 1, Received: now}, artColor: ui.Hex("#C9A27E")}
	a.modules = []module{a.np}
	f := newUsageFeature(a, usageDeps{
		fetchCodex:  func(context.Context) aiusage.Quota { return okQuota(aiusage.ToolCodex, "Plus", now) },
		fetchClaude: func(context.Context) aiusage.Quota { return okQuota(aiusage.ToolClaude, "Pro", now) },
		pace: func(w aiusage.Window, n time.Time) aiusage.PaceInfo {
			return aiusage.PaceInfo{ExpectedLeftPct: 60, AheadPct: 5, LastsUntilReset: true}
		},
		newScanner: func() usageScanner { return &fakeUsageScanner{summaries: evidenceSummaries(now)} },
		now:        func() time.Time { return now },
	})
	a.registerPage(f)
	a.settings.AIUsage = settings.AIUsageSettings{Enabled: true, QuotaCodex: true, QuotaClaude: true}
	view := ui.NewTester(a.view, sheetW, panelTestH)
	view.Frame()
	savePNG(t, view.Image(), "render-island")

	a.np, a.modules = nil, nil
	view.Frame()
	savePNG(t, view.Image(), "render-island-counts")

	a.np = &nowPlayingModule{host: a, have: true, last: mac.NowPlayingState{Playing: true, Title: "座右铭", Artist: "吴雨霏", App: "com.apple.Music", Duration: 243, Elapsed: 112, Rate: 1, Received: now}, artColor: ui.Hex("#C9A27E")}
	a.modules = []module{a.np}
	a.level = 2
	view.Frame()
	savePNG(t, view.Image(), "render-tray")
	a.setTab(pageClipboard)
	view.Frame()
	savePNG(t, view.Image(), "render-clipboard")

	a.tab = pageUsage
	onMain(func() { f.tick(now) })
	view.Frame()
	savePNG(t, view.Image(), "render-usage-today")
	f.rangeSel = aiusage.Range30d
	onMain(func() { f.tick(now) })
	view.Frame()
	savePNG(t, view.Image(), "render-usage-30d")

	compareWithPrototype(t, "island", 340, 104, 40, 0)
	compareWithPrototype(t, "island-counts", 340, 104, 40, 0)
	compareWithPrototype(t, "tray", 420, 250, 0, 0)
	compareWithPrototype(t, "clipboard", 420, 250, 0, 0)
	compareWithPrototype(t, "usage-today", 420, 250, 0, 0)
	compareWithPrototype(t, "usage-30d", 420, 250, 0, 0)
}

// compareWithPrototype screenshots the B prototype open on page, crops its
// shell and lays it beside our headless render of the same size.
func compareWithPrototype(t *testing.T, page string, w, h, ox, oy int) {
	t.Helper()
	tab, extra := page, ""
	switch page {
	case "island":
		tab, extra = "tray", ""
	case "island-counts":
		tab, extra = "tray", ""
	case "clipboard":
		tab = "clip"
	case "usage-today":
		tab = "ai"
	case "usage-30d":
		tab, extra = "ai", "__proto.S.range=2;"
	}
	proto, err := prototypeShot(t, page, tab, page == "island" || page == "island-counts", page != "island-counts", extra)
	if err != nil {
		t.Logf("%s: prototype shot unavailable: %v", page, err)
		return
	}
	shellX := (1470 - w) / 2
	protoShell, err := cropPNG(proto, shellX, 0, w, h)
	if err != nil {
		t.Fatalf("%s: crop prototype: %v", page, err)
	}
	ours := filepath.Join(panelEvidenceDir, "render-"+page+".png")
	ourShell, err := cropPNG(ours, ox, oy, w, h)
	if err != nil {
		t.Fatalf("%s: crop render: %v", page, err)
	}
	canvas := image.NewRGBA(image.Rect(0, 0, 2*w+20, h))
	draw.Draw(canvas, image.Rect(0, 0, w, h), protoShell, image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(w+20, 0, 2*w+20, h), ourShell, image.Point{}, draw.Src)
	path := filepath.Join(panelEvidenceDir, "compare-"+page+".png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, canvas); err != nil {
		t.Fatal(err)
	}
	t.Logf("compare %s -> %s", page, path)
}

// prototypeShot opens a copy of the prototype with one state preloaded and
// captures the 1470 × 956 screen with headless Chrome.
func prototypeShot(t *testing.T, name, tab string, island, music bool, extra string) (string, error) {
	t.Helper()
	src := "/Users/y3u/AIProjects/test/y3/design/prototype-panel/index.html"
	body, err := os.ReadFile(src)
	if err != nil {
		return "", err
	}
	open := extra + fmt.Sprintf("__proto.open(%q);", tab)
	if island {
		open = "__proto.open('tray');__proto.S.level=1;"
		if !music {
			open += "__proto.S.music=false;"
		}
		open += "__proto.render();"
	}
	open += "document.querySelectorAll('#mount .layer').forEach(function(l){l.style.transition='none';l.style.opacity='1';l.style.transform='translateX(-50%) scale(1)';l.classList.add('on');});"
	prefix := "var st=document.createElement('style');st.textContent='*{animation:none!important;transition:none!important}';document.head.appendChild(st);document.getElementById('shell').style.transition='none';"
	injected := append(body, []byte("<script>setTimeout(function(){"+prefix+open+"},300);</script>")...)
	dir := t.TempDir()
	html := filepath.Join(dir, "proto.html")
	if err := os.WriteFile(html, injected, 0o644); err != nil {
		return "", err
	}
	// The wallpaper is referenced relatively; copy it next to the page.
	if wb, err := os.ReadFile(filepath.Dir(src) + "/wallpaper.jpg"); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "wallpaper.jpg"), wb, 0o644)
	}
	out := filepath.Join(dir, "shot.png")
	chrome := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(chrome); err != nil {
		return "", err
	}
	cmd := exec.Command(chrome, "--headless=new", "--disable-gpu", "--hide-scrollbars",
		"--no-first-run", "--user-data-dir="+filepath.Join(dir, "profile"),
		"--window-size=1470,956", "--force-device-scale-factor=1", "--virtual-time-budget=3000",
		"--screenshot="+out, "file://"+html+"?variant=B")
	if msg, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("%v: %s", err, msg)
	}
	if data, err := os.ReadFile(out); err == nil {
		_ = os.WriteFile(filepath.Join(panelEvidenceDir, "proto-"+name+".png"), data, 0o644)
	}
	return out, nil
}

func cropPNG(path string, x, y, w, h int) (image.Image, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		return nil, err
	}
	r := image.Rect(x, y, x+w, y+h)
	if !r.In(img.Bounds()) {
		return nil, fmt.Errorf("%s: %v outside %v", path, r, img.Bounds())
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), img, r.Min, draw.Src)
	return dst, nil
}

func savePNG(t *testing.T, img image.Image, name string) {
	t.Helper()
	file, err := os.Create(filepath.Join(panelEvidenceDir, name+".png"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		t.Fatal(err)
	}
}

// keepShot copies a screenshot into the evidence directory under name.
func keepShot(t *testing.T, from, name string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(panelEvidenceDir, name+".png"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// evidenceSummaries adds the mock's 30-day totals so the comparison's right
// column is not empty.
func evidenceSummaries(now time.Time) map[aiusage.Range]aiusage.Summary {
	m := usageShotSummaries(now)
	buckets := make([]aiusage.Bucket, 30)
	for i := range buckets {
		buckets[i] = aiusage.Bucket{Start: now.AddDate(0, 0, i-29), Tokens: map[aiusage.Tool]int64{
			aiusage.ToolOmp: 42_000_000, aiusage.ToolClaude: 11_000_000, aiusage.ToolCodex: 3_000_000,
		}}
	}
	m[aiusage.Range30d] = aiusage.Summary{
		Range:   aiusage.Range30d,
		Total:   aiusage.Totals{Input: 50_000_000, Output: 100_000_000, CacheRead: 1_450_000_000, Requests: 11_800, Cost: 238},
		ByTool:  map[aiusage.Tool]aiusage.Totals{aiusage.ToolOmp: {Requests: 9_000, Cost: 238}, aiusage.ToolClaude: {Requests: 2_200}, aiusage.ToolCodex: {Requests: 600}},
		Buckets: buckets,
	}
	return m
}
