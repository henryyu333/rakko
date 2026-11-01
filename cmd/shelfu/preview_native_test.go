//go:build darwin

package main

// Consistency shots: the same five states from the real AppKit window and
// from the headless preview world, in one process, side by side. Run through
// the native gate with the smoke harness:
//
//	SHELF_BASKET_SMOKE=1 RAKKO_PREVIEW_SHOTS=1 go test -run TestPreviewConsistencyShots -count=1 ./cmd/shelfu
//
// The comparison is the honest part: window sizes, mean absolute RGB
// difference and the share of pixels that differ by more than 12/255 are
// written to /tmp/rakko-preview/consistency.txt together with the images.

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/mac"
	"shelf/internal/settings"
)

const (
	previewShotDir    = "/tmp/rakko-preview"
	previewShotPrefix = "consistency-"
)

// TestPreviewConsistencyShots needs the real window (SHELF_BASKET_SMOKE)
// and its own opt-in, so the normal smoke run does not open Settings.
func TestPreviewConsistencyShots(t *testing.T) {
	if os.Getenv("SHELF_BASKET_SMOKE") != "1" {
		t.Skip("set SHELF_BASKET_SMOKE=1 to run the native consistency shots")
	}
	if os.Getenv("RAKKO_PREVIEW_SHOTS") != "1" {
		t.Skip("set RAKKO_PREVIEW_SHOTS=1 to write and compare the shots")
	}
	if err := os.MkdirAll(previewShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The smoke binary's resource path is not the app bundle's; point the
	// otter loader at the repository's pictures like the other smoke tests.
	if _, err := os.Stat("../../resources/otter"); err == nil {
		otterDir = func() string { return "../../resources/otter" }
		resetOtters()
	}
	// The smoke shelf is a real app with a real window; only this test's
	// temp dirs and fake data are used.
	a := redesignShelf(t)
	a.dataDir = t.TempDir()
	a.settings = settings.Default()
	a.settings.AIUsage.Enabled = true
	a.thumbnails = map[string]*ui.Bitmap{}
	a.thumbPending = map[string]bool{}
	a.appNames = map[string]string{}
	a.spotIcons = map[string]*ui.Bitmap{}
	a.spotPending = map[string]bool{}

	cursor := mygo.Screen.CursorScreenPoint()
	disp := mygo.Screen.DisplayNearestPoint(cursor)
	nx, ny, nw, nh, hasNotch := mac.NotchRect(disp.ID)
	t.Logf("display %d %+v work=%+v notch=(%d,%d %d×%d) has=%v",
		disp.ID, disp.Bounds, disp.WorkArea, nx, ny, nw, nh, hasNotch)

	// Same bytes in both worlds: the tray pictures and the clipboard text.
	files := previewFiles(t, t.TempDir())
	onMain(func() {
		if err := a.store.AddFiles(files); err != nil {
			t.Fatal(err)
		}
		for _, text := range previewClipTexts {
			if err := a.store.AddClipText(text, "com.apple.Safari"); err != nil {
				t.Fatal(err)
			}
		}
		a.place(cursor)
		a.setTab(pageTray)
		a.visible = false
	})

	// The AI usage page in both worlds is the same fake feature.
	nativeUsage := previewUsageFeature(t, a)
	_ = nativeUsage

	w := newPreviewWorld(t)
	w.useDisplay(disp, nx, ny, nw, nh, hasNotch)
	// Same fake Finder icons on both sides: the headless cache is what the
	// preview paints, and the live app would otherwise ask NSWorkspace.
	for path, b := range w.a.spotIcons {
		a.spotIcons[path] = b
	}

	var report string
	report += fmt.Sprintf("display %d bounds=%+v work=%+v notch=%d,%d %dx%d has=%v\n",
		disp.ID, disp.Bounds, disp.WorkArea, nx, ny, nw, nh, hasNotch)
	report += fmt.Sprintf("preview canvas %d×%d scale %d\n\n", previewCanvasW, previewCanvasH, previewScale)

	states := []struct {
		name     string
		settings bool
		apply    func()
	}{
		{"collapsed", false, func() {
			onMain(func() {
				a.visible, a.peeking = false, false
				a.progress, a.animationFrom = 0, 0
				a.animationStart = time.Time{}
				a.closeAt = time.Time{}
				a.syncWindowFrame()
				a.win.Invalidate()
			})
		}},
		{"search", false, func() {
			onMain(func() {
				a.place(cursor)
				a.show(false)
				a.progress, a.animationFrom = 1, 1
				a.animationStart = time.Time{}
				a.syncWindowFrame()
				a.win.Invalidate()
			})
		}},
		{"results", false, func() {
			onMain(func() {
				a.setLevel(1)
				a.homeQuery, a.homeSel = "设计", 0
				a.homeSpot = homeSpotState{query: "设计", items: w.spotHits, done: true, typedAt: time.Now()}
				a.win.Invalidate()
			})
		}},
		{"tray", false, func() {
			onMain(func() {
				a.setLevel(2)
				a.setTab(pageTray)
				a.win.Invalidate()
			})
		}},
		{"settings-usage", true, func() {
			onMain(func() {
				a.hide(hideExplicit)
				a.openSettings()
				a.loginOn, a.loginErr = false, ""
				a.settingsPage = settingsPageAIUsage
				a.settingsScroll.Y = 0
				if a.settingsWin != nil {
					a.settingsWin.Invalidate()
				}
			})
		}},
	}

	for _, st := range states {
		time.Sleep(250 * time.Millisecond)
		st.apply()
		time.Sleep(900 * time.Millisecond) // settle the live springs and thumbnails

		// Native window.
		var win *mygo.Window
		onMain(func() {
			if st.settings {
				win = a.settingsWin
			} else {
				win = a.win
			}
		})
		if win == nil {
			t.Fatalf("%s: no native window", st.name)
		}
		nativePath := filepath.Join(previewShotDir, previewShotPrefix+"native-"+st.name+".png")
		native := previewShotWindow(t, win, nativePath)
		if st.settings {
			// A titled window's screenshot includes its title bar; the
			// settings view is only the 820 × 560 content.
			var frameH int
			onMain(func() { frameH = int(math.Round(mac.WindowFrame(win.NativeHandle()).Size.Height)) })
			if bar := frameH - settingsHeight; bar > 0 {
				native = cropTop(native, bar*previewScale)
			}
		}

		// Headless world.
		w.applyState(st.name)
		w.settle()
		layers := w.windows()
		var headless *image.RGBA
		for _, l := range layers {
			if l.Opaque == st.settings {
				headless = l.Img
				break
			}
		}
		if headless == nil {
			t.Fatalf("%s: no headless layer", st.name)
		}
		headlessPath := filepath.Join(previewShotDir, previewShotPrefix+"headless-"+st.name+".png")
		if err := os.WriteFile(headlessPath, pngBytes(headless), 0o644); err != nil {
			t.Fatal(err)
		}

		mad, pct, diffPath := previewCompare(native, headless, filepath.Join(previewShotDir, previewShotPrefix+"diff-"+st.name+".png"))
		line := fmt.Sprintf("%-15s native %v  headless %v  mad=%.2f  >12/255=%.2f%%  diff=%s",
			st.name, native.Bounds().Size(), headless.Bounds().Size(), mad, pct, diffPath)
		t.Log(line)
		report += line + "\n"
	}

	// Close the Settings window this test opened.
	onMain(func() {
		if a.settingsWin != nil {
			a.settingsWin.Close()
			a.settingsWin = nil
		}
		a.visible = false
		a.syncWindowFrame()
	})
	reportPath := filepath.Join(previewShotDir, "consistency.txt")
	if err := os.WriteFile(reportPath, []byte(report), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("report: %s", reportPath)
}

// useDisplay replaces the world's synthetic geometry with the app's real
// display, so the two renders ask for identical window frames.
func (w *previewWorld) useDisplay(d mygo.Display, nx, ny, nw, nh int, has bool) {
	displayNearest = func(mygo.Point) mygo.Display { return d }
	notchRect = func(int64) (int, int, int, int, bool) { return nx, ny, nw, nh, has }
	w.a.place(mygo.Point{X: d.Bounds.X + d.Bounds.Width/2, Y: d.Bounds.Y + max(nh, 1)/2})
	w.a.frame = w.a.desiredFrame()
	w.pointer = mygo.Point{X: d.Bounds.X + d.Bounds.Width/2, Y: d.Bounds.Y + d.Bounds.Height/2}
}

// previewUsageFeature gives a real shelf the same fake usage page the
// headless world uses.
func previewUsageFeature(t *testing.T, a *shelf) *usageFeature {
	t.Helper()
	now := time.Now()
	codex := &fakeQuotaFetch{q: okQuota(aiusage.ToolCodex, "Plus", now)}
	claude := &fakeQuotaFetch{q: okQuota(aiusage.ToolClaude, "Pro", now)}
	scan := &fakeUsageScanner{summaries: usageShotSummaries(now)}
	f := newUsageFeature(a, usageDeps{
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
	a.registerPage(f)
	return f
}

// cropTop cuts dy pixels off the top of an image.
func cropTop(img image.Image, dy int) image.Image {
	b := img.Bounds()
	if dy <= 0 || dy >= b.Dy() {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()-dy))
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			out.Set(x, y, img.At(b.Min.X+x, b.Min.Y+dy+y))
		}
	}
	return out
}

// previewShotWindow screenshots one window without its shadow and decodes
// it.
func previewShotWindow(t *testing.T, w *mygo.Window, path string) image.Image {
	t.Helper()
	num := mac.WindowNumber(w.NativeHandle())
	if num == 0 {
		t.Fatalf("%s: window has no CGWindowID", path)
	}
	time.Sleep(450 * time.Millisecond)
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture: %v (%s)", path, err, out)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatalf("%s: decode: %v", path, err)
	}
	return img
}

// previewCompare reports the mean absolute RGB difference and the share of
// pixels whose largest channel difference exceeds 12, and writes an
// amplified diff image.
func previewCompare(a, b image.Image, diffPath string) (mad, pct float64, path string) {
	ba, bb := a.Bounds(), b.Bounds()
	w, h := min(ba.Dx(), bb.Dx()), min(ba.Dy(), bb.Dy())
	if w <= 0 || h <= 0 {
		return 0, 100, ""
	}
	diff := image.NewRGBA(image.Rect(0, 0, w, h))
	var sum float64
	var over int
	for y := range h {
		for x := range w {
			r1, g1, b1, _ := a.At(ba.Min.X+x, ba.Min.Y+y).RGBA()
			r2, g2, b2, _ := b.At(bb.Min.X+x, bb.Min.Y+y).RGBA()
			d := []float64{
				math.Abs(float64(r1>>8) - float64(r2>>8)),
				math.Abs(float64(g1>>8) - float64(g2>>8)),
				math.Abs(float64(b1>>8) - float64(b2>>8)),
			}
			m := max(d[0], max(d[1], d[2]))
			sum += (d[0] + d[1] + d[2]) / 3
			if m > 12 {
				over++
			}
			diff.Set(x, y, color.RGBA{
				R: uint8(min(m*4, 255)),
				G: uint8(min(m*2, 255)),
				B: uint8(min(m*1, 255) / 2),
				A: 255,
			})
		}
	}
	n := float64(w * h)
	if err := os.WriteFile(diffPath, pngBytes(diff), 0o644); err != nil {
		diffPath = ""
	}
	return sum / n, float64(over) * 100 / n, diffPath
}
