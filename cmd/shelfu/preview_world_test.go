//go:build darwin

package main

// The preview server's world: one real shelf, driven through ui.NewTester
// without a window, with a fake desktop's geometry, real temporary files,
// clipboard text, fake quotas and usage, and a fake Spotlight seam. Nothing
// here imitates a view: every pixel comes from a.view or settingsView.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/mac"
	"shelf/internal/settings"
	"shelf/internal/store"
)

// Preview desktop and window geometry: a 14-inch MacBook Pro's 1512 × 982
// point display with its 180 × 32 camera housing at the top centre.
const (
	previewDesktopW = 1512
	previewDesktopH = 982
	previewMenuBarH = 38
	previewNotchW   = 180
	previewNotchH   = 32

	// The headless surface is larger than any window frame the panel asks
	// for; each frame is cropped to desiredFrame before it is sent, so the
	// extra transparent margin never reaches the page.
	previewCanvasW = 720
	previewCanvasH = 520
	previewScale   = 2
)

// Preview states, in the order the page lists them.
const (
	previewCollapsed  = "collapsed"
	previewHover      = "hover"
	previewSearch     = "search"
	previewResults    = "results"
	previewTray       = "tray"
	previewClipboard  = "clipboard"
	previewTools      = "tools"
	previewUsage      = "usage"
	previewDrag       = "drag"
	previewCharge     = "live-charge"
	previewCharged    = "live-charged"
	previewLow        = "live-low"
	previewHeadset    = "live-headset"
	previewHeadsetOff = "live-headset-off"
	previewDownload   = "live-download"
	previewSettings   = "settings"
)

// previewLayer is one rendered window: its screen frame and pixels.
type previewLayer struct {
	Name   string
	X, Y   int
	W, H   int
	Img    *image.RGBA
	Scale  int
	Opaque bool // settings is an ordinary opaque window; the notch shell is not
}

// previewWorld owns all state the server renders. Only the server's event
// loop touches it after construction.
type previewWorld struct {
	t     testingT
	a     *shelf
	store *store.Store
	dir   string

	files      []string // the tray's files, for the drag state
	imagePaths []string
	spotHits   []mac.SpotlightHit
	usage      *usageFeature

	shelfView     *ui.Tester
	settingsView  *ui.Tester
	settingsShown bool

	pointer mygo.Point
	state   string

	// saved seams, restored when the test ends
	oldDisplay, oldNotch func()
}

// testingT is the slice of *testing.T the world needs; the native shot
// test can pass a *testing.T too.
type testingT interface {
	Helper()
	TempDir() string
	Cleanup(func())
	Fatalf(format string, args ...any)
}

// newPreviewWorld builds the shelf and its fake data.
func newPreviewWorld(t testingT) *previewWorld {
	t.Helper()
	w := &previewWorld{t: t, dir: t.TempDir(), state: previewCollapsed}
	// The test binary does not live next to the app's resources; the
	// repository's git-ignored otter pictures are two levels up.
	oldOtter := otterDir
	if _, err := os.Stat("../../resources/otter"); err == nil {
		otterDir = func() string { return "../../resources/otter" }
		resetOtters()
	}
	t.Cleanup(func() { otterDir = oldOtter; resetOtters() })
	// The headless surface must not park or read the machine's drag filter;
	// the native tests run in this same process.
	oldPark := parkNotchFilter
	parkNotchFilter = func(bool) {}
	t.Cleanup(func() { parkNotchFilter = oldPark })
	// The display and housing lookups are package seams already used by
	// the geometry tests; restoring them leaves the app untouched.
	builtin := mygo.Display{
		ID: 1, Primary: true,
		Bounds:   mygo.Rectangle{X: 0, Y: 0, Width: previewDesktopW, Height: previewDesktopH},
		WorkArea: mygo.Rectangle{X: 0, Y: previewMenuBarH, Width: previewDesktopW, Height: previewDesktopH - previewMenuBarH},
	}
	oldDisplay, oldNotch := displayNearest, notchRect
	displayNearest = func(mygo.Point) mygo.Display { return builtin }
	notchRect = func(id int64) (int, int, int, int, bool) {
		if id == 1 {
			return (previewDesktopW - previewNotchW) / 2, 0, previewNotchW, previewNotchH, true
		}
		return 0, 0, 0, 0, false
	}
	t.Cleanup(func() { displayNearest, notchRect = oldDisplay, oldNotch })

	s, err := store.Open(w.dir)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	w.store = s

	st := settings.Default()
	st.AIUsage.Enabled = true
	st.Language = ""
	a := &shelf{
		store: s, dataDir: w.dir, settings: st, preview: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{}, spotIcons: map[string]*ui.Bitmap{},
		spotPending: map[string]bool{},
	}
	a.locale = ""
	a.loginOn = false
	// Copies stay in a recorder: the preview never writes the machine's
	// clipboard (the test recorder lives in clipboard_ui_test.go).
	a.write = &recordClip{}
	w.a = a

	w.seedFiles()
	w.seedClipboard()
	w.seedSpotlight()
	w.seedUsage()
	// The bar's results are fake by default; RAKKO_PREVIEW_REAL_SPOTLIGHT=1
	// keeps mac.StartSpotlight so the page can search the machine's own
	// index (the typed query then costs a real index read).
	if os.Getenv("RAKKO_PREVIEW_REAL_SPOTLIGHT") != "1" {
		w.installSearchSeam()
	}
	a.place(mygo.Point{X: previewDesktopW / 2, Y: previewNotchH / 2})
	w.pointer = mygo.Point{X: previewDesktopW / 2, Y: previewNotchH / 2}
	oldCursor := cursorPoint
	cursorPoint = func() mygo.Point { return w.pointer }
	t.Cleanup(func() { cursorPoint = oldCursor })
	stubPreviewAppSeams(t)

	w.rebuild()
	w.applyState(previewCollapsed)
	return w
}

// previewClipTexts is the clipboard history both the preview and the
// consistency shots show.
var previewClipTexts = []string{
	"https://github.com/egoist/mygo",
	"Rakko 住在 Mac 刘海里的工具",
	"func (a *shelf) desiredFrame() mygo.Rectangle",
	"会议记录：预览服务 127.0.0.1:5199",
	"brew install --cask raycast",
	"设计原则：让布局自己解释",
	"sk-…（已打码）",
	"SELECT id, path FROM items ORDER BY created_at DESC;",
}

// previewFiles writes the tray's real temporary files into dir and returns
// them newest-first. The consistency shots write the same bytes, so the two
// thumbnails are identical.
func previewFiles(t testingT, dir string) []string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "旅行照片"), 0o755); err != nil {
		t.Fatalf("files: %v", err)
	}
	writePNG := func(name string, top, bottom color.RGBA) string {
		p := filepath.Join(dir, name)
		img := image.NewRGBA(image.Rect(0, 0, 640, 400))
		for y := range 400 {
			k := float64(y) / 399
			c := color.RGBA{
				R: uint8(float64(top.R)*(1-k) + float64(bottom.R)*k),
				G: uint8(float64(top.G)*(1-k) + float64(bottom.G)*k),
				B: uint8(float64(top.B)*(1-k) + float64(bottom.B)*k),
				A: 255,
			}
			for x := range 640 {
				img.Set(x, y, c)
			}
		}
		// A sun and a horizon give the pictures distinct thumbnails.
		for y := 120; y < 180; y++ {
			for x := 100; x < 160; x++ {
				if (x-130)*(x-130)+(y-150)*(y-150) < 900 {
					img.Set(x, y, color.RGBA{255, 236, 170, 255})
				}
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatalf("png: %v", err)
		}
		if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		return p
	}
	sunset := writePNG("海边日落.png", color.RGBA{255, 154, 92, 255}, color.RGBA{64, 62, 138, 255})
	screenshot := writePNG("产品截图.png", color.RGBA{60, 120, 220, 255}, color.RGBA{20, 30, 70, 255})
	kyoto := writePNG(filepath.Join("旅行照片", "京都.png"), color.RGBA{226, 96, 120, 255}, color.RGBA{70, 40, 90, 255})
	pdf := filepath.Join(dir, "Rakko 设计稿.pdf")
	if err := os.WriteFile(pdf, minimalPDF(), 0o644); err != nil {
		t.Fatalf("pdf: %v", err)
	}
	md := filepath.Join(dir, "项目说明.md")
	if err := os.WriteFile(md, []byte("# Rakko 预览\n\n本地预览服务的说明。\n"), 0o644); err != nil {
		t.Fatalf("md: %v", err)
	}
	docx := filepath.Join(dir, "季度报告.docx")
	if err := os.WriteFile(docx, []byte("PK\x03\x04 preview document"), 0o644); err != nil {
		t.Fatalf("docx: %v", err)
	}
	folder := filepath.Join(dir, "旅行照片")
	return []string{sunset, screenshot, pdf, md, docx, folder, kyoto}
}

// seedFiles puts the preview files on the tray and fills their thumbnails,
// as the live app's background decode would.
func (w *previewWorld) seedFiles() {
	w.files = previewFiles(w.t, filepath.Join(w.dir, "files"))
	if err := w.store.AddFiles(w.files); err != nil {
		w.t.Fatalf("add files: %v", err)
	}
	w.imagePaths = []string{w.files[0], w.files[1], w.files[6]}
	for _, it := range w.store.Items {
		if it.Kind == store.KindText {
			continue
		}
		switch strings.ToLower(filepath.Ext(it.Path)) {
		case ".png", ".jpg", ".jpeg":
			if img := decodeThumb(it.Path); img != nil {
				w.a.thumbnails[it.ID] = ui.NewBitmap(img)
			}
		}
	}
}

// seedClipboard fills the clipboard history with the kind of text the app
// captures.
func (w *previewWorld) seedClipboard() {
	for _, t := range previewClipTexts {
		if err := w.store.AddClipText(t, "com.apple.Safari"); err != nil {
			w.t.Fatalf("clip: %v", err)
		}
	}
}

// seedSpotlight builds fake index hits and their row icons.
func (w *previewWorld) seedSpotlight() {
	home, _ := os.UserHomeDir()
	now := time.Now()
	hits := []mac.SpotlightHit{
		{Path: "/Applications/Safari.app", Name: "Safari", App: true, LastUsed: now.Add(-2 * time.Hour)},
		{Path: "/Applications/Keynote.app", Name: "Keynote", App: true, LastUsed: now.Add(-26 * time.Hour)},
		{Path: "/System/Applications/Mail.app", Name: "Mail", App: true, LastUsed: now.Add(-72 * time.Hour)},
		{Path: filepath.Join(home, "Projects", "Rakko"), Name: "Rakko", Folder: true, LastUsed: now.Add(-time.Hour)},
		{Path: filepath.Join(home, "Projects", "Rakko", "设计稿-v4.html"), Name: "设计稿-v4.html", LastUsed: now.Add(-40 * time.Minute)},
		{Path: filepath.Join(home, "Documents", "旅行清单.md"), Name: "旅行清单.md", LastUsed: now.Add(-5 * time.Hour)},
		{Path: filepath.Join(home, "Downloads", "Rakko-1.12.1.dmg"), Name: "Rakko-1.12.1.dmg", LastUsed: now.Add(-96 * time.Hour)},
	}
	w.spotHits = hits
	appIcon := previewIcon(color.RGBA{58, 130, 246, 255}, func(x, y int) bool { return true })
	folderIcon := previewIcon(color.RGBA{86, 148, 236, 255}, func(x, y int) bool {
		return y > 10 || (x > 4 && x < 18 && y > 6)
	})
	fileIcon := previewIcon(color.RGBA{236, 238, 242, 255}, func(x, y int) bool {
		return x > 6 && x < 25 && y > 4 && y < 27
	})
	for _, h := range hits {
		switch {
		case h.App:
			w.a.spotIcons[h.Path] = appIcon
		case h.Folder:
			w.a.spotIcons[h.Path] = folderIcon
		default:
			w.a.spotIcons[h.Path] = fileIcon
		}
	}
}

// previewIcon draws one simple 32 × 32 result icon: a coloured field cut by
// inside.
func previewIcon(c color.RGBA, inside func(x, y int) bool) *ui.Bitmap {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			if inside(x, y) {
				img.Set(x, y, c)
			} else {
				img.Set(x, y, color.RGBA{0, 0, 0, 0})
			}
		}
	}
	return ui.NewBitmap(img)
}

// seedUsage wires the AI usage page with fake quota answers and summaries:
// no network, no log reads.
func (w *previewWorld) seedUsage() {
	now := time.Now()
	codex := &fakeQuotaFetch{q: okQuota(aiusage.ToolCodex, "Plus", now)}
	claude := &fakeQuotaFetch{q: okQuota(aiusage.ToolClaude, "Pro", now)}
	scan := &fakeUsageScanner{summaries: usageShotSummaries(now)}
	f := newUsageFeature(w.a, usageDeps{
		fetchCodex:  codex.fetch,
		fetchClaude: claude.fetch,
		pace:        fakePace,
		newScanner:  func() usageScanner { return scan },
		now:         time.Now,
	})
	for _, tool := range quotaTools() {
		q := okQuota(tool, map[aiusage.Tool]string{
			aiusage.ToolCodex: "Plus", aiusage.ToolClaude: "Pro",
		}[tool], now)
		st := &f.quota[quotaIndex(tool)]
		st.latest, st.good = q, q
	}
	f.rangeSel = aiusage.RangeToday
	f.summary = usageShotSummaries(now)[aiusage.RangeToday]
	f.hasSummary = true
	f.localAt = now
	f.scanDone.Store(100)
	f.scanTotal.Store(100)
	w.a.registerPage(f)
	w.usage = f
}

// installSearchSeam replaces the Spotlight seam with hits from seedSpotlight
// so typing in the preview searches fake data synchronously.
func (w *previewWorld) installSearchSeam() {
	oldStart, oldAfter := spotlightStart, homeAfterFunc
	spotlightStart = func(term string, limit int, emit func([]mac.SpotlightHit), done func()) func() {
		q := strings.ToLower(strings.TrimSpace(term))
		var out []mac.SpotlightHit
		for _, h := range w.spotHits {
			if q == "" || strings.Contains(strings.ToLower(h.Name), q) {
				out = append(out, h)
			}
		}
		emit(out)
		done()
		return func() {}
	}
	homeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		// The preview has one thread for its world: the debounce fires now.
		f()
		t := time.NewTimer(time.Hour)
		t.Stop()
		return t
	}
	w.t.Cleanup(func() { spotlightStart, homeAfterFunc = oldStart, oldAfter })
}

// rebuild recreates the testers, which resets every spring's history: a
// state change starts from a clean surface instead of the last state's
// animation values.
func (w *previewWorld) rebuild() {
	w.shelfView = ui.NewTester(w.a.view, previewCanvasW, previewCanvasH)
	w.shelfView.SetScale(previewScale)
	w.settingsView = nil
	w.settingsShown = false
}

// reset puts the shelf back to a closed, page-less state. The testers are
// recreated afterwards by the caller.
func (w *previewWorld) reset() {
	a := w.a
	a.visible, a.edgeOpened, a.received = false, false, false
	a.peeking = false
	a.progress, a.animationFrom = 0, 0
	a.peekProgress, a.peekFrom = 0, 0
	a.animationStart, a.peekAnimStart = time.Time{}, time.Time{}
	a.closeAt = time.Time{}
	a.focusMode, a.prevAppPID = false, 0
	a.level = 1
	a.tab, a.tabFrom = pageTray, pageTray
	a.tabAt = time.Time{}
	a.tabOutK, a.tabOutShift = 1, 0
	a.search, a.homeQuery, a.homeSel = "", "", 0
	a.homeSpot = homeSpotState{}
	a.dragging, a.suppressed, a.ownDrag, a.pointerDrag = false, false, false, false
	a.dropHot, a.inBand = false, false
	a.drop = dropZoneState{}
	a.dragOpenTools = false
	a.live = liveQueue{}
	a.fb = feedback{}
	a.fx = shellMotion{}
	a.jumpLevel = true
	a.settingsPage = settingsPageGeneral
	a.settingsScroll = ui.ScrollState{}
	a.loginOn, a.loginErr = false, ""
	a.spotIcons = map[string]*ui.Bitmap{
		// keep the seeded row icons
	}
	w.seedSpotIconsAgain()
	os.Unsetenv("SHELF_DEBUG_DROP_FILES")
	a.place(mygo.Point{X: previewDesktopW / 2, Y: previewNotchH / 2})
	a.frame = mygo.Rectangle{X: a.stripCenter() - previewCanvasW/2, Y: 0, Width: previewCanvasW, Height: previewCanvasH}
}

// seedSpotIconsAgain restores the fake rows' icons after reset cleared the
// map.
func (w *previewWorld) seedSpotIconsAgain() {
	appIcon := previewIcon(color.RGBA{58, 130, 246, 255}, func(x, y int) bool { return true })
	folderIcon := previewIcon(color.RGBA{86, 148, 236, 255}, func(x, y int) bool {
		return y > 10 || (x > 4 && x < 18 && y > 6)
	})
	fileIcon := previewIcon(color.RGBA{236, 238, 242, 255}, func(x, y int) bool {
		return x > 6 && x < 25 && y > 4 && y < 27
	})
	for _, h := range w.spotHits {
		switch {
		case h.App:
			w.a.spotIcons[h.Path] = appIcon
		case h.Folder:
			w.a.spotIcons[h.Path] = folderIcon
		default:
			w.a.spotIcons[h.Path] = fileIcon
		}
	}
}

// applyState puts the world in one named state at rest and rebuilds its
// testers. The caller then renders (or lets the live loop animate) frames.
func (w *previewWorld) applyState(name string) {
	a := w.a
	w.reset()
	w.rebuild()
	w.state = name
	switch name {
	case previewCollapsed:
		// nothing else
	case previewHover:
		a.setPeek(true)
		a.peekProgress, a.peekFrom = 1, 1
		a.peekAnimStart = time.Time{}
	case previewSearch, previewResults, previewTray, previewClipboard, previewTools, previewUsage, previewDrag:
		a.show(false)
		a.progress, a.animationFrom = 1, 1
		a.animationStart = time.Time{}
		switch name {
		case previewSearch:
			a.setLevel(1)
		case previewResults:
			a.setLevel(1)
			w.setResults("设计")
		case previewTray:
			a.setLevel(2)
			a.setTab(pageTray)
		case previewClipboard:
			a.setLevel(2)
			a.setTab(pageClipboard)
		case previewTools:
			a.setLevel(2)
			a.setTab(pageTools)
		case previewUsage:
			a.setLevel(2)
			a.setTab(pageUsage)
		case previewDrag:
			a.setLevel(2)
			a.setTab(pageTray)
			os.Setenv("SHELF_DEBUG_DROP_FILES", strings.Join(w.files, string(os.PathListSeparator)))
			a.dragging, a.dropHot = true, true
			a.dragSerial++
			a.dropZoneSnap(true)
		}
	case previewCharge, previewCharged, previewLow, previewHeadset, previewHeadsetOff, previewDownload:
		a.place(mygo.Point{X: previewDesktopW / 2, Y: previewNotchH / 2})
		now := time.Now()
		switch name {
		case previewCharge:
			a.pushLive(liveEvent{kind: liveCharging, label: "正在充电", pct: 80}, liveHoldCharge, now)
		case previewCharged:
			a.pushLive(liveEvent{kind: liveCharged, label: "已充满", pct: 100}, liveHoldFull, now)
		case previewLow:
			a.pushLive(liveEvent{kind: liveLow, label: "电量低", pct: 12}, liveHoldLow, now)
		case previewHeadset:
			a.pushLive(liveEvent{kind: liveHeadset, label: "AirPods Pro",
				levels: []string{"左 80%", "右 78%", "盒 50%"}}, liveHoldHeadset, now)
		case previewHeadsetOff:
			a.pushLive(liveEvent{kind: liveHeadsetOff, label: "AirPods Pro"}, liveHoldUnplug, now)
		case previewDownload:
			a.pushLive(liveEvent{kind: liveDownloadDone, label: "Rakko 设计稿.pdf",
				paths: []string{filepath.Join(w.dir, "files", "Rakko 设计稿.pdf")}}, liveHoldDownload, now)
		}
	case previewSettings:
		a.settingsPage = settingsPageGeneral
		w.showSettings()
	default:
		if strings.HasPrefix(name, previewSettings+"-") {
			w.showSettings()
			switch strings.TrimPrefix(name, previewSettings+"-") {
			case "general":
				a.settingsPage = settingsPageGeneral
			case "notch":
				a.settingsPage = settingsPageNotch
			case "tray":
				a.settingsPage = settingsPageTray
			case "clipboard":
				a.settingsPage = settingsPageClipboard
			case "usage":
				a.settingsPage = settingsPageAIUsage
			case "shortcuts":
				a.settingsPage = settingsPageShortcuts
			case "permissions":
				a.settingsPage = settingsPagePermissions
			case "about":
				a.settingsPage = settingsPageAbout
			default:
				w.t.Fatalf("unknown settings state %q", name)
			}
		} else {
			w.t.Fatalf("unknown preview state %q", name)
		}
	}
}

// showSettings makes the settings window visible in the preview world.
func (w *previewWorld) showSettings() {
	w.settingsShown = true
	w.settingsView = ui.NewTester(w.a.settingsView, settingsWidth, settingsHeight)
	w.settingsView.SetScale(previewScale)
}

// setResults puts the search bar on q with finished fake hits.
func (w *previewWorld) setResults(q string) {
	w.a.homeQuery = q
	w.a.homeSel = 0
	w.a.homeSpot = homeSpotState{query: q, items: w.spotHits, done: true, typedAt: time.Now()}
}

// hoverAt applies the hover the live app's mouse monitor would: inside the
// anchor or its peek pill opens the peek shape, leaving folds it.
func (w *previewWorld) hoverAt(p mygo.Point) {
	a := w.a
	if a.visible || w.settingsShown {
		return
	}
	hold := a.overAnchor(p) || (a.peeking && a.overPeekPill(p))
	if hold {
		a.setPeek(true)
	} else {
		a.setPeek(false)
	}
}

// clickAt applies a press at screen p: the notch itself opens the panel,
// anything inside the panel or its hung shapes goes to the Tester, and a
// click elsewhere dismisses an open panel like the live app's outside click.
func (w *previewWorld) clickAt(p mygo.Point) {
	a := w.a
	if !a.visible && !a.peeking && a.overAnchor(p) {
		a.show(false)
		return
	}
	canvasX, canvasY := w.canvasOrigin()
	if inFrame(p, a.desiredFrame()) {
		x, y := float32(p.X-canvasX), float32(p.Y-canvasY)
		w.shelfView.ClickAt(x, y)
		return
	}
	if a.visible {
		a.hide(hideExplicit)
		return
	}
	if a.overAnchor(p) {
		a.show(false)
	}
}

// canvasOrigin is where the headless surface's top-left sits on the fake
// desktop: centred on the strip, flush with the screen top.
func (w *previewWorld) canvasOrigin() (int, int) {
	return w.a.stripCenter() - previewCanvasW/2, 0
}

func inFrame(p mygo.Point, r mygo.Rectangle) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}

// windows renders the current state once and returns its layers cropped to
// their real frames.
func (w *previewWorld) windows() []previewLayer {
	var out []previewLayer
	ax, ay := w.canvasOrigin()
	f := w.a.desiredFrame()
	out = append(out, previewLayer{
		Name: w.frameState(),
		X:    f.X, Y: f.Y, W: f.Width, H: f.Height,
		Img:   cropRGBA(w.shelfView.Image(), f.X-ax, f.Y-ay, f.Width, f.Height, previewScale),
		Scale: previewScale,
	})
	if w.settingsShown && w.settingsView != nil {
		r := settingsFrame(w.a.dispWork)
		out = append(out, previewLayer{
			Name: w.state, X: r.X, Y: r.Y, W: r.Width, H: r.Height,
			Img:   cropRGBA(w.settingsView.Image(), 0, 0, r.Width, r.Height, previewScale),
			Scale: previewScale, Opaque: true,
		})
	}
	return out
}

// cropRGBA cuts r out of a scaled image and copies it: a fresh Pix makes
// the frame hash exact and keeps the parent surface free for the next frame.
func cropRGBA(img *image.RGBA, x, y, w, h, scale int) *image.RGBA {
	if img == nil || w <= 0 || h <= 0 {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	r := image.Rect(x*scale, y*scale, (x+w)*scale, (y+h)*scale)
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 0, 0))
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for yy := 0; yy < r.Dy(); yy++ {
		copy(out.Pix[yy*out.Stride:yy*out.Stride+r.Dx()*4], img.Pix[(yy+r.Min.Y-img.Rect.Min.Y)*img.Stride+(r.Min.X-img.Rect.Min.X)*4:])
	}
	return out
}

// frameState names what the last frame showed, for recordings whose state
// changes mid-gesture (a page switch, an opening search bar).
func (w *previewWorld) frameState() string {
	a := w.a
	switch {
	case w.settingsShown:
		return w.state
	case a.visible && a.currentLevel() == 1 && strings.TrimSpace(a.homeQuery) != "":
		return previewResults
	case a.visible && a.currentLevel() == 1:
		return previewSearch
	case a.visible && a.tab == pageClipboard:
		return previewClipboard
	case a.visible && a.tab == pageTools:
		return previewTools
	case a.visible && a.tab == pageUsage:
		return previewUsage
	case a.visible:
		return previewTray
	case a.dragging && !a.ownDrag:
		return previewDrag
	case a.live.shown:
		switch a.live.cur.kind {
		case liveCharging:
			return previewCharge
		case liveCharged:
			return previewCharged
		case liveLow:
			return previewLow
		case liveHeadset:
			return previewHeadset
		case liveHeadsetOff:
			return previewHeadsetOff
		case liveDownloadDone:
			return previewDownload
		}
	case a.peeking:
		return previewHover
	}
	return previewCollapsed
}

// settle renders frames with a virtual clock until the springs rest, used
// for still states and the consistency shots. It leaves the Tester clock
// ahead of the wall clock, so a caller that keeps interacting must rebuild.
func (w *previewWorld) settle() {
	const step = time.Second / 60
	now := time.Now()
	for i := 0; i < 120; i++ {
		now = now.Add(step)
		w.shelfView.Advance(step)
		if w.settingsView != nil {
			w.settingsView.Advance(step)
		}
		w.a.animate(now)
		w.shelfView.Frame()
		if w.settingsView != nil {
			w.settingsView.Frame()
		}
		if i > 10 && !w.shelfView.FramePending() {
			break
		}
	}
	// The frame rectangle the drawing ended at (the preview loop reads the
	// same value after every frame).
	w.a.frame = w.a.desiredFrame()
}

// pngBytes encodes a layer.
func pngBytes(img image.Image) []byte {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&b, img); err != nil {
		return nil
	}
	return b.Bytes()
}

// minimalPDF is a valid one-page PDF, enough for the tray's PDF row.
func minimalPDF() []byte {
	return []byte("%PDF-1.4\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n" +
		"2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n" +
		"3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 200]>>endobj\n" +
		"trailer<</Root 1 0 R>>\n%%EOF\n")
}

// ensure fmt is used by states that name a file.
var _ = fmt.Sprintf

// TestPreviewStatesRender drives every state the page offers through the
// real views and checks that the surface actually shows something (and the
// closed notch shows nothing). It also guards desiredFrame staying inside
// the headless canvas, which the page depends on for its crop.
func TestPreviewStatesRender(t *testing.T) {
	w := newPreviewWorld(t)
	states := []string{
		previewCollapsed, previewHover, previewSearch, previewResults, previewTray,
		previewClipboard, previewTools, previewUsage, previewDrag,
		previewCharge, previewCharged, previewLow, previewHeadset, previewHeadsetOff,
		previewDownload, previewSettings + "-general", previewSettings + "-notch", previewSettings + "-tray",
		previewSettings + "-clipboard", previewSettings + "-usage", previewSettings + "-shortcuts",
		previewSettings + "-permissions", previewSettings + "-about",
	}
	for _, name := range states {
		w.applyState(name)
		w.settle()
		layers := w.windows()
		settings := strings.HasPrefix(name, previewSettings)
		var got *previewLayer
		for i := range layers {
			if layers[i].Opaque == settings {
				got = &layers[i]
				break
			}
		}
		if got == nil {
			t.Fatalf("%s: no %s layer", name, map[bool]string{true: "settings", false: "shelf"}[settings])
		}
		if got.W <= 0 || got.H <= 0 || got.Img == nil {
			t.Fatalf("%s: empty layer %+v", name, got)
		}
		if got.Img.Bounds().Dx() != got.W*previewScale || got.Img.Bounds().Dy() != got.H*previewScale {
			t.Fatalf("%s: layer %dx%d at scale %d, image %v", name, got.W, got.H, previewScale, got.Img.Bounds())
		}
		if name == previewCollapsed {
			if !previewTransparent(got.Img) {
				t.Fatalf("collapsed notch draws pixels; the shell must be fully transparent")
			}
		} else if previewTransparent(got.Img) {
			t.Fatalf("%s: layer is fully transparent", name)
		}
		if !settings {
			if got.X < 0 || got.Y < 0 || got.X+got.W > previewDesktopW || got.Y+got.H > previewDesktopH {
				t.Fatalf("%s: frame %+v leaves the desktop", name, got)
			}
		}
	}
}

// previewTransparent reports whether every pixel is fully transparent.
func previewTransparent(img *image.RGBA) bool {
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			return false
		}
	}
	return true
}

// stubPreviewAppSeams keeps the preview's clicks away from the machine:
// no login item, no Finder or browser, no global shortcut registration, no
// notification and no AppKit activation. The real app is untouched because
// every seam is restored after the test.
func stubPreviewAppSeams(t testingT) {
	t.Helper()
	oldLogin, oldSetLogin := openAtLogin, setOpenAtLogin
	oldOpen, oldExternal := openPath, openExternal
	oldAccess := accessibilityRequest
	oldVersion, oldLocale := appVersionProbe, setAppLocale
	oldReveal := revealInFinder
	oldNotify := sendNotification
	oldReg, oldUnreg := registerKey, unregisterKey
	oldActive, oldActivate, oldResign := appIsActive, activateApp, resignSelf
	oldWork, oldDispatch := goWork, dispatchMain
	oldFDAP, oldFDAR := fullDiskAccessProbe, fullDiskAccessRequest
	oldAXP, oldSCP := accessibilityProbe, screenCaptureProbe
	oldSCR, oldPrivacy := screenCaptureRequest, openPrivacyPane
	oldUpdateOn, oldUpdateCheck := updateEnabled, updateCheck
	oldRelaunch := relaunchApp

	openAtLogin = func() bool { return false }
	setOpenAtLogin = func(bool) error { return nil }
	openPath = func(string) error { return nil }
	openExternal = func(string) error { return nil }
	accessibilityRequest = func() bool { return true }
	appVersionProbe = func() string { return "1.12.1" }
	setAppLocale = func(string) {}
	revealInFinder = func(string) {}
	sendNotification = func(string, string) bool { return true }
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}
	appIsActive = func() bool { return false }
	activateApp = func(int) bool { return false }
	resignSelf = func() {}
	// The preview has one thread: slow work and its main-thread publish run
	// inline so a clicked refresh or range segment completes in the frame.
	goWork = func(fn func()) { fn() }
	dispatchMain = func(fn func()) { fn() }
	updateEnabled = func() bool { return false }
	updateCheck = func(context.Context) (*mygo.Update, error) {
		return nil, errors.New("preview has no update feed")
	}
	relaunchApp = func() {}
	// Every permission reads as granted: the preview must not prompt for
	// TCC access or open System Settings.
	fullDiskAccessProbe = func() bool { return true }
	fullDiskAccessRequest = func() bool { return true }
	accessibilityProbe = func() bool { return true }
	screenCaptureProbe = func() bool { return true }
	screenCaptureRequest = func() bool { return true }
	openPrivacyPane = func() {}

	t.Cleanup(func() {
		openAtLogin, setOpenAtLogin = oldLogin, oldSetLogin
		openPath, openExternal = oldOpen, oldExternal
		accessibilityRequest = oldAccess
		appVersionProbe, setAppLocale = oldVersion, oldLocale
		revealInFinder = oldReveal
		sendNotification = oldNotify
		registerKey, unregisterKey = oldReg, oldUnreg
		appIsActive, activateApp, resignSelf = oldActive, oldActivate, oldResign
		goWork, dispatchMain = oldWork, oldDispatch
		updateEnabled, updateCheck = oldUpdateOn, oldUpdateCheck
		relaunchApp = oldRelaunch
		fullDiskAccessProbe, fullDiskAccessRequest = oldFDAP, oldFDAR
		accessibilityProbe, screenCaptureProbe = oldAXP, oldSCP
		screenCaptureRequest, openPrivacyPane = oldSCR, oldPrivacy
	})
}
