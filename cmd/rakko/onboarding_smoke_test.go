//go:build darwin

package main

// Real first launch, end to end: the built app, a temporary data directory
// without settings.json, the live notch window. The test launches the app
// itself (so the real startup path decides that a fresh install owes the
// guide), walks it with real key and pointer events, and screenshots what
// the screen shows. Gated by RAKKO_BASKET_SMOKE=1 and run through
// native-gate.sh; shots land in /tmp/Rakko-onboarding/shots.
//
// The move step is pointed at a temporary "Applications" folder through
// RAKKO_ONBOARDING_APPS, so nothing is ever moved into the real
// /Applications.

import (
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/locales"
)

const onboardingShotDir = "/tmp/Rakko-onboarding/shots"

func TestOnboardingFirstLaunchNative(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 (through native-gate.sh) to drive the real app")
	}
	if err := os.MkdirAll(onboardingShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	primary := mygo.Screen.PrimaryDisplay()
	// The panel follows the pointer's display; park the pointer on the
	// primary one, off the notch itself.
	movePointer(float32(primary.Bounds.X)+float32(primary.Bounds.Width)/2, float32(primary.Bounds.Y)+120)

	// A copy that already lives in an Applications folder: three steps.
	tmp := t.TempDir()
	appsBundle := buildRakkoBundle(t, tmp, "Applications")
	data := filepath.Join(tmp, "data")
	if _, err := os.Stat(filepath.Join(data, settings.FileName)); !os.IsNotExist(err) {
		t.Fatalf("the fresh data directory already has settings.json: %v", err)
	}
	proc := launchRakko(t, appsBundle, data, "")
	shotScreen(t, primary, "native-onboarding-intro")
	pressKey(t, 36 /* Return */)
	time.Sleep(700 * time.Millisecond)
	shotScreen(t, primary, "native-onboarding-perms")
	pressKey(t, 36)
	time.Sleep(700 * time.Millisecond)
	shotScreen(t, primary, "native-onboarding-try")

	// Drag the sample chip into the panel: the real drag path, then the
	// file lands in the tray. A click is the fallback the design offers.
	chip := chipRect(t)
	dragPointer(t,
		float32(primary.Bounds.X)+panelLeft(primary)+chip.X+chip.W/2,
		float32(primary.Bounds.Y)+chip.Y+chip.H/2,
		float32(primary.Bounds.X)+panelLeft(primary)+416/2,
		float32(primary.Bounds.Y)+300)
	if !waitTrayItems(t, data, 1, 3*time.Second) {
		clickPointer(t,
			float32(primary.Bounds.X)+panelLeft(primary)+chip.X+chip.W/2,
			float32(primary.Bounds.Y)+chip.Y+chip.H/2)
		if !waitTrayItems(t, data, 1, 3*time.Second) {
			t.Fatalf("neither the chip drag nor its click landed a file; app log:\n%s", proc.log())
		}
		t.Log("the chip click landed the sample after the drag missed")
	}
	time.Sleep(1200 * time.Millisecond)
	shotScreen(t, primary, "native-onboarding-finish")

	pressKey(t, 36)
	time.Sleep(900 * time.Millisecond)
	shotScreen(t, primary, "native-onboarding-capsule")
	saved, err := settings.Load(data)
	if err != nil {
		t.Fatalf("settings after the guide: %v", err)
	}
	if saved.Onboarding.Pending || saved.Onboarding.Step != "" {
		t.Fatalf("the finished guide still owes a step: %+v", saved.Onboarding)
	}
	proc.stop(t)

	// A copy in a Downloads-shaped folder: four steps, and the move must
	// reopen the app from the temporary Applications folder.
	tmp2 := t.TempDir()
	loadBundle := buildRakkoBundle(t, tmp2, "Downloads")
	apps2 := filepath.Join(tmp2, "Applications")
	if err := os.MkdirAll(apps2, 0o755); err != nil {
		t.Fatal(err)
	}
	data2 := filepath.Join(tmp2, "data")
	proc2 := launchRakko(t, loadBundle, data2, apps2)
	shotScreen(t, primary, "native-onboarding-downloads-intro")
	pressKey(t, 36)
	time.Sleep(700 * time.Millisecond)
	shotScreen(t, primary, "native-onboarding-downloads-move")
	// The primary button on the move step runs the move.
	clickPointer(t,
		float32(primary.Bounds.X)+panelLeft(primary)+440-12-60,
		float32(primary.Bounds.Y)+250-12-15)
	moved := waitFor(t, 25*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(apps2, newBundleName))
		return err == nil
	})
	if !moved {
		t.Fatalf("the bundle never reached %s; app log:\n%s", apps2, proc2.log())
	}
	oldPID := proc2.pid
	proc2.stopAfterQuit(t)
	movedBundle := filepath.Join(apps2, newBundleName)
	again := waitFor(t, 25*time.Second, func() bool {
		for _, pid := range rakkoPIDs(movedBundle) {
			if pid != oldPID {
				return true
			}
		}
		return false
	})
	if !again {
		t.Fatalf("the moved app did not reopen; app log:\n%s", proc2.log())
	}
	time.Sleep(1500 * time.Millisecond)
	shotScreen(t, primary, "native-onboarding-after-move")
	saved2, err := settings.Load(data2)
	if err != nil {
		t.Fatalf("settings after the move: %v", err)
	}
	if saved2.Onboarding.Step != onbStepPerms {
		t.Fatalf("the relaunched app resumes at %q, want %q", saved2.Onboarding.Step, onbStepPerms)
	}
	if !saved2.Onboarding.Pending {
		t.Fatal("the relaunched app no longer owes the guide")
	}
	// Only the copy this test launched: never a Rakko the user installed.
	killRakkoPIDs(t, movedBundle)
}

// --- launching the real app ----------------------------------------------

// builtProc is the launched app and where its own log went.
type builtProc struct {
	cmd *exec.Cmd
	pid int
	out *os.File
}

func (p *builtProc) log() string {
	if p == nil || p.out == nil {
		return ""
	}
	data, _ := os.ReadFile(p.out.Name())
	return string(data)
}

func (p *builtProc) stop(t *testing.T) {
	t.Helper()
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return
	}
	_ = p.cmd.Process.Kill()
	_ = p.cmd.Wait()
}

// stopAfterQuit waits for the app to quit itself (the move's relaunch) and
// harvests its log without killing the new instance.
func (p *builtProc) stopAfterQuit(t *testing.T) {
	t.Helper()
	if p == nil || p.cmd == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		p.stop(t)
	}
}

// buildRakkoBundle writes a minimal app bundle whose bundle sits directly in
// <tmp>/<folder>: an Applications folder reads as installed, Downloads asks
// for the move.
func buildRakkoBundle(t *testing.T, tmp, folder string) string {
	t.Helper()
	bundle := filepath.Join(tmp, folder, newBundleName)
	exe := filepath.Join(bundle, "Contents", "MacOS", "Rakko")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", exe, "./cmd/rakko")
	build.Dir = filepath.Join("..", "..")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the app: %v\n%s", err, out)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleExecutable</key><string>Rakko</string>
<key>CFBundleIdentifier</key><string>local.y3.rakko.onboarding-smoke</string>
<key>CFBundleName</key><string>Rakko</string>
<key>CFBundleShortVersionString</key><string>1.0</string>
<key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle
}

// launchRakko starts the bundle with its own data directory and waits for
// its "Rakko ready" line, so the guide is up before the test draws.
func launchRakko(t *testing.T, bundle, data, appsDir string) *builtProc {
	t.Helper()
	logFile, err := os.CreateTemp("", "rakko-onboarding-smoke-*.log")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(bundle, "Contents", "MacOS", "Rakko"),
		"--data-dir", data, "--show")
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), "RAKKO_ONBOARDING_APPS="+appsDir)
	if err := cmd.Start(); err != nil {
		t.Fatalf("launching %s: %v", bundle, err)
	}
	p := &builtProc{cmd: cmd, pid: cmd.Process.Pid, out: logFile}
	t.Cleanup(func() { p.stop(t) })
	ready := waitFor(t, 25*time.Second, func() bool {
		data, _ := os.ReadFile(logFile.Name())
		return strings.Contains(string(data), "Rakko ready")
	})
	if !ready {
		t.Fatalf("the app did not become ready; log:\n%s", p.log())
	}
	// The opening runs notch -> search bar -> page; the guide's page draws
	// only at the end of that flight.
	time.Sleep(1800 * time.Millisecond)
	return p
}

// rakkoPIDs lists the processes launched from bundle, and only those.
func rakkoPIDs(bundle string) []int {
	var pids []int
	out, err := exec.Command("pgrep", "-f", filepath.Join(bundle, "Contents", "MacOS", "Rakko")).Output()
	if err != nil {
		return nil
	}
	for _, f := range strings.Fields(string(out)) {
		var pid int
		if _, err := fmt.Sscanf(f, "%d", &pid); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func killRakkoPIDs(t *testing.T, bundle string) {
	t.Helper()
	for _, pid := range rakkoPIDs(bundle) {
		_ = exec.Command("kill", fmt.Sprint(pid)).Run()
	}
}

// --- screen and input -----------------------------------------------------

// panelLeft is where the 440pt shell starts on the primary display.
func panelLeft(d mygo.Display) float32 {
	return (float32(d.Bounds.Width) - sheetW) / 2
}

// chipRect is the sample chip's box inside the panel window: measured by
// laying the same guide out headlessly, so the pointer lands on the real
// chip whatever the system language does to line heights.
func chipRect(t *testing.T) ui.Rect {
	t.Helper()
	a := &shelf{settings: settings.Default(), thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}}
	a.settings.Onboarding = settings.OnboardingSettings{Pending: true}
	a.onb = &onboardingFeature{host: a, origin: originInstalled}
	a.onb.step = len(a.onb.steps()) - 1
	a.registerPage(a.onb)
	a.tab, a.tabFrom, a.level, a.visible = pageOnboarding, pageOnboarding, 2, true
	a.locale = mygo.App.Locale()
	v := ui.NewTester(a.view, sheetW, sheetH)
	v.SetLocale(mygo.App.Locale())
	v.Frame()
	v.Frame()
	name := a.onb.sampleName(locales.In(mygo.App.Locale()))
	r, ok := v.Find(name)
	if !ok {
		t.Fatalf("the chip %q is not in the layout", name)
	}
	return r
}

func waitFor(t *testing.T, d time.Duration, ok func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return ok()
}

// waitTrayItems waits until the data directory's state.json holds n items.
func waitTrayItems(t *testing.T, data string, n int, d time.Duration) bool {
	t.Helper()
	return waitFor(t, d, func() bool {
		raw, err := os.ReadFile(filepath.Join(data, "state.json"))
		if err != nil {
			return false
		}
		var st struct {
			Items []struct {
				Path string `json:"path"`
			} `json:"items"`
		}
		if json.Unmarshal(raw, &st) != nil {
			return false
		}
		return len(st.Items) >= n
	})
}

// shotScreen captures the display, cropped to the top centre where the
// panel hangs, and writes it to the evidence directory.
func shotScreen(t *testing.T, d mygo.Display, name string) string {
	t.Helper()
	raw, err := os.CreateTemp("", "rakko-screen-*.png")
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()
	defer os.Remove(raw.Name())
	if out, err := exec.Command("screencapture", "-x", "-o", raw.Name()).CombinedOutput(); err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	f, err := os.Open(raw.Name())
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		t.Fatalf("%s: decode: %v", name, err)
	}
	scale := float64(img.Bounds().Dx()) / float64(d.Bounds.Width)
	cw, ch := int(560*scale), int(320*scale)
	x0 := img.Bounds().Dx()/2 - cw/2
	y0 := int(float64(d.Bounds.Y)*scale) - int(8*scale)
	if y0 < 0 {
		y0 = 0
	}
	rect := image.Rect(x0, y0, x0+cw, y0+ch).Intersect(img.Bounds())
	dst := image.NewRGBA(image.Rect(0, 0, rect.Dx(), rect.Dy()))
	draw.Draw(dst, dst.Bounds(), img, rect.Min, draw.Src)
	path := filepath.Join(onboardingShotDir, name+".png")
	out, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(out, dst); err != nil {
		out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("shot %s", path)
	return path
}

// cgFuncs holds the CoreGraphics entry points the pointer and key events
// need; the smoke suite posts the same events elsewhere.
type cgFuncs struct {
	createKey   func(uintptr, uint16, bool) uintptr
	createMouse func(uintptr, uint32, float64, float64, uint32) uintptr
	post        func(uint32, uintptr)
	release     func(uintptr)
}

func cg(t *testing.T) *cgFuncs {
	t.Helper()
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		t.Fatal(err)
	}
	c := &cgFuncs{}
	purego.RegisterLibFunc(&c.createKey, lib, "CGEventCreateKeyboardEvent")
	purego.RegisterLibFunc(&c.createMouse, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&c.post, lib, "CGEventPost")
	purego.RegisterLibFunc(&c.release, cf, "CFRelease")
	return c
}

func pressKey(t *testing.T, key uint16) {
	t.Helper()
	c := cg(t)
	for _, down := range []bool{true, false} {
		ev := c.createKey(0, key, down)
		if ev == 0 {
			t.Fatal("CGEventCreateKeyboardEvent returned nil")
		}
		c.post(0, ev)
		if ev != 0 {
			c.release(ev)
		}
		time.Sleep(60 * time.Millisecond)
	}
}

func movePointer(x, y float32) {
	c := cgNoT()
	if c == nil {
		return
	}
	ev := c.createMouse(0, 5 /* moved */, float64(x), float64(y), 0)
	if ev == 0 {
		return
	}
	c.post(0, ev)
	c.release(ev)
	time.Sleep(120 * time.Millisecond)
}

// cgNoT is cg for the one call that runs before a *testing.T is needed.
func cgNoT() *cgFuncs {
	lib, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_LAZY)
	if err != nil {
		return nil
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_LAZY)
	if err != nil {
		return nil
	}
	c := &cgFuncs{}
	purego.RegisterLibFunc(&c.createMouse, lib, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&c.post, lib, "CGEventPost")
	purego.RegisterLibFunc(&c.release, cf, "CFRelease")
	return c
}

func clickPointer(t *testing.T, x, y float32) {
	t.Helper()
	movePointer(x, y)
	c := cg(t)
	for _, kind := range []uint32{1 /* leftDown */, 2 /* leftUp */} {
		ev := c.createMouse(0, kind, float64(x), float64(y), 0)
		if ev == 0 {
			t.Fatal("CGEventCreateMouseEvent returned nil")
		}
		c.post(0, ev)
		c.release(ev)
		time.Sleep(60 * time.Millisecond)
	}
}

func dragPointer(t *testing.T, x0, y0, x1, y1 float32) {
	t.Helper()
	movePointer(x0, y0)
	c := cg(t)
	down := c.createMouse(0, 1 /* leftDown */, float64(x0), float64(y0), 0)
	if down == 0 {
		t.Fatal("CGEventCreateMouseEvent returned nil")
	}
	c.post(0, down)
	c.release(down)
	const steps = 12
	for i := 1; i <= steps; i++ {
		f := float32(i) / steps
		x, y := x0+(x1-x0)*f, y0+(y1-y0)*f
		ev := c.createMouse(0, 6 /* leftDragged */, float64(x), float64(y), 0)
		if ev == 0 {
			t.Fatal("CGEventCreateMouseEvent returned nil")
		}
		c.post(0, ev)
		c.release(ev)
		time.Sleep(25 * time.Millisecond)
	}
	up := c.createMouse(0, 2 /* leftUp */, float64(x1), float64(y1), 0)
	if up == 0 {
		t.Fatal("CGEventCreateMouseEvent returned nil")
	}
	c.post(0, up)
	c.release(up)
	time.Sleep(150 * time.Millisecond)
}
