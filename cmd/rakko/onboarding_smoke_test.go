//go:build darwin

package main

// Real first launch, end to end: the built app, a temporary data directory
// without settings.json, the live notch window. The test launches the app
// itself (so the real startup path decides that a fresh install owes the
// guide), walks it with real key events, and screenshots its own window —
// by CGWindowID, so another Rakko on the same screen can never be mistaken
// for this one. Gated by RAKKO_BASKET_SMOKE=1 and run through
// native-gate.sh; shots land in /tmp/Rakko-onboarding/shots.
//
// The move step is pointed at a temporary "Applications" folder through
// RAKKO_ONBOARDING_APPS and a temporary home through RAKKO_ONBOARDING_HOME,
// so nothing is ever moved into the real /Applications or ~/Downloads.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ebitengine/purego"
	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
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
	screenH := float64(primary.Bounds.Y + primary.Bounds.Height)
	// The panel follows the pointer's display; park the pointer on the
	// primary one, off the notch itself.
	want := mygo.Point{X: int(primary.Bounds.X) + int(primary.Bounds.Width)/2, Y: int(primary.Bounds.Y) + 120}
	movePointer(float32(want.X), float32(want.Y))
	time.Sleep(300 * time.Millisecond)
	t.Logf("primary=%+v cursor=%+v want=%+v displays=%+v", primary.Bounds, mygo.Screen.CursorScreenPoint(), want, displayBounds())

	// A copy that already lives in an Applications folder: two steps, the
	// intro and the permissions. 开始使用 on the permissions retires the
	// guide and folds the panel to the notch with the ready capsule.
	tmp := t.TempDir()
	appsBundle := buildRakkoBundle(t, tmp, "Applications")
	data := filepath.Join(tmp, "data")
	if _, err := os.Stat(filepath.Join(data, settings.FileName)); !os.IsNotExist(err) {
		t.Fatalf("the fresh data directory already has settings.json: %v", err)
	}
	run := launchRakko(t, appsBundle, data, "")
	run.shot(t, "native-onboarding-intro")
	run.press(t, 36 /* Return */)
	time.Sleep(700 * time.Millisecond)
	run.shot(t, "native-onboarding-perms")
	run.press(t, 36 /* Return: 开始使用 */)
	if !waitFor(t, 5*time.Second, func() bool {
		saved, err := settings.Load(data)
		return err == nil && !saved.Onboarding.Pending && saved.Onboarding.Step == ""
	}) {
		t.Fatalf("the permissions step did not retire the guide; app log:\n%s", run.log())
	}
	time.Sleep(1200 * time.Millisecond)
	run.shot(t, "native-onboarding-ready")
	saved, err := settings.Load(data)
	if err != nil {
		t.Fatalf("settings after the guide: %v", err)
	}
	if saved.Onboarding.Pending || saved.Onboarding.Step != "" {
		t.Fatalf("the finished guide still owes a step: %+v", saved.Onboarding)
	}
	run.stop(t)

	// A copy in a Downloads-shaped folder: four steps, and the move must
	// reopen the app from the temporary Applications folder.
	tmp2 := t.TempDir()
	loadBundle := buildRakkoBundle(t, tmp2, "Downloads")
	apps2 := filepath.Join(tmp2, "Applications")
	if err := os.MkdirAll(apps2, 0o755); err != nil {
		t.Fatal(err)
	}
	data2 := filepath.Join(tmp2, "data")
	t.Setenv("RAKKO_ONBOARDING_HOME", tmp2)
	run2 := launchRakko(t, loadBundle, data2, apps2)
	run2.shot(t, "native-onboarding-downloads-intro")
	run2.press(t, 36)
	time.Sleep(700 * time.Millisecond)
	run2.shot(t, "native-onboarding-downloads-move")
	// ⏎ runs the move step's primary action, the same as the button.
	run2.press(t, 36)
	moved := waitFor(t, 25*time.Second, func() bool {
		_, err := os.Stat(filepath.Join(apps2, newBundleName))
		return err == nil
	})
	if !moved {
		t.Fatalf("the bundle never reached %s; app log:\n%s", apps2, run2.log())
	}
	oldPID := run2.pid
	run2.stopAfterQuit(t)
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
		t.Fatalf("the moved app did not reopen; app log:\n%s", run2.log())
	}
	var relaunched *appRun
	for _, pid := range rakkoPIDs(movedBundle) {
		if pid != oldPID {
			if w, ok := waitWindow(t, pid, 8*time.Second); ok {
				relaunched = &appRun{t: t, pid: pid, win: w.number, frame: w.frame, screenH: screenH}
				break
			}
		}
	}
	if relaunched == nil {
		t.Fatalf("the moved app's window never appeared; app log:\n%s", run2.log())
	}
	defer relaunched.stop(t)
	time.Sleep(1200 * time.Millisecond)
	if args, err := exec.Command("ps", "-o", "command=", "-p", fmt.Sprint(relaunched.pid)).Output(); err == nil {
		t.Logf("relaunched pid %d: %s", relaunched.pid, strings.TrimSpace(string(args)))
	}
	t.Logf("relaunched win=%d frame=%+v", relaunched.win, relaunched.frame)
	relaunched.shot(t, "native-onboarding-after-move")
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

// appRun is one launched app: its process, log and the notch window the
// test may drive.
type appRun struct {
	t       *testing.T
	cmd     *exec.Cmd
	pid     int
	out     *os.File
	win     int
	frame   mygo.Rectangle
	screenH float64
}

func (r *appRun) log() string {
	if r == nil || r.out == nil {
		return ""
	}
	data, _ := os.ReadFile(r.out.Name())
	return string(data)
}

func (r *appRun) stop(t *testing.T) {
	t.Helper()
	if r == nil || r.cmd == nil || r.cmd.Process == nil {
		return
	}
	_ = r.cmd.Process.Kill()
	_ = r.cmd.Wait()
}

// stopAfterQuit waits for the app to quit itself (the move's relaunch) and
// harvests its log without killing the new instance.
func (r *appRun) stopAfterQuit(t *testing.T) {
	t.Helper()
	if r == nil || r.cmd == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = r.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		r.stop(t)
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
	// The checkout's resources, where a signed build ships them: the app
	// icon on the move step and the otter's poses need them.
	if err := copyTree(filepath.Join("..", "..", "resources"), filepath.Join(bundle, "Contents", "Resources")); err != nil {
		t.Fatalf("staging resources: %v", err)
	}
	return bundle
}

// launchRakko starts the bundle with its own data directory, waits for its
// "Rakko ready" line and for its notch window, so the guide is up before
// the test draws.
func launchRakko(t *testing.T, bundle, data, appsDir string) *appRun {
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
	r := &appRun{t: t, cmd: cmd, pid: cmd.Process.Pid, out: logFile}
	t.Cleanup(func() { r.stop(t) })
	ready := waitFor(t, 25*time.Second, func() bool {
		data, _ := os.ReadFile(logFile.Name())
		return strings.Contains(string(data), "Rakko ready")
	})
	if !ready {
		t.Fatalf("the app did not become ready; log:\n%s", r.log())
	}
	w, ok := waitWindow(t, r.pid, 15*time.Second)
	if !ok {
		t.Fatalf("the notch window never appeared; log:\n%s", r.log())
	}
	r.win, r.frame = w.number, w.frame
	t.Logf("window %d frame=%+v", r.win, r.frame)
	primary := mygo.Screen.PrimaryDisplay()
	r.screenH = float64(primary.Bounds.Y + primary.Bounds.Height)
	// The opening runs notch -> search bar -> page; the guide's page draws
	// only at the end of that flight.
	time.Sleep(1800 * time.Millisecond)
	return r
}

func displayBounds() []mygo.Rectangle {
	var out []mygo.Rectangle
	for _, d := range mygo.Screen.Displays() {
		out = append(out, d.Bounds)
	}
	return out
}

type windowInfo struct {
	number int
	frame  mygo.Rectangle
}

// waitWindow waits for the app's panel: the widest window of its PID, at
// least the guide's own width.
func waitWindow(t *testing.T, pid int, d time.Duration) (windowInfo, bool) {
	t.Helper()
	var best windowInfo
	ok := waitFor(t, d, func() bool {
		found := false
		onMain(func() {
			for _, w := range mac.WindowsOfPID(pid) {
				if w.Bounds.Size.Width < 430 || w.Bounds.Size.Height < 240 {
					continue
				}
				area := w.Bounds.Size.Width * w.Bounds.Size.Height
				if found && area <= float64(best.frame.Width*best.frame.Height) {
					continue
				}
				best = windowInfo{number: w.Number, frame: mygo.Rectangle{
					X: int(w.Bounds.Origin.X), Y: int(w.Bounds.Origin.Y),
					Width: int(w.Bounds.Size.Width), Height: int(w.Bounds.Size.Height),
				}}
				found = true
			}
		})
		return found
	})
	return best, ok
}

// ownsPoint proves WindowServer routes the point to this app's window, so a
// posted event can never land in another app (the user's own Rakko sits at
// the same notch).
func (r *appRun) ownsPoint(x, y float32) bool {
	var hit int
	onMain(func() { hit = mac.WindowAtPoint(float64(x), float64(y), r.screenH) })
	return hit == r.win
}

// mustOwn waits for WindowServer to route the point to this app's window.
// Another Rakko at the same notch can briefly sit in front; the press is
// never posted elsewhere.
func (r *appRun) mustOwn(x, y float32, what string) {
	r.t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		if r.ownsPoint(x, y) {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("%s: the point (%.0f,%.0f) is not this app's window %d; refusing to post", what, x, y, r.win)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// shot captures this app's own window by id, whatever is in front of it.
func (r *appRun) shot(t *testing.T, name string) string {
	t.Helper()
	if err := os.MkdirAll(onboardingShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(onboardingShotDir, name+".png")
	out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", r.win), path).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: screencapture failed (%v): %s", name, err, out)
	}
	t.Logf("shot %s", path)
	return path
}

// --- input ----------------------------------------------------------------

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

// --- CoreGraphics ---------------------------------------------------------

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

func (r *appRun) press(t *testing.T, key uint16) {
	t.Helper()
	c := cg(t)
	for _, down := range []bool{true, false} {
		ev := c.createKey(0, key, down)
		if ev == 0 {
			t.Fatal("CGEventCreateKeyboardEvent returned nil")
		}
		c.post(0, ev)
		c.release(ev)
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

func (r *appRun) click(t *testing.T, x, y float32) {
	t.Helper()
	r.mustOwn(x, y, "click")
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

func (r *appRun) drag(t *testing.T, x0, y0, x1, y1 float32) {
	t.Helper()
	r.mustOwn(x0, y0, "drag")
	r.mustOwn(x1, y1, "drag release")
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
