//go:build darwin

package main

// The first-launch guide: where the bundle runs, the move that settles it in
// /Applications, the pending bit that survives restarts, the page that owns
// the panel while it is owed, and the permissions step that ends it.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
	"rakko/internal/store"
)

// onboardingShelf is a visible shelf on the guide's page with pending set and
// a temporary data directory, so no test touches a real install.
func onboardingShelf(t *testing.T) (*shelf, *ui.Tester) {
	t.Helper()
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	stubKeys(t)
	a := &shelf{
		store: s, dataDir: t.TempDir(), level: 2, visible: true,
		settings:   settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{},
	}
	a.settings.Onboarding = settings.OnboardingSettings{Pending: true, Step: onbStepIntro}
	a.onb = newOnboarding(a)
	a.registerPage(a.onb)
	a.tab, a.tabFrom = pageOnboarding, pageOnboarding
	return a, ui.NewTester(a.view, sheetW, panelTestH)
}

// stubKeys keeps the global hotkeys out of a headless test.
func stubKeys(t *testing.T) {
	t.Helper()
	reg, unreg := registerKey, unregisterKey
	registerKey = func(string, func()) error { return nil }
	unregisterKey = func(string) {}
	t.Cleanup(func() { registerKey, unregisterKey = reg, unreg })
}

func TestRunLocationNamesTheFourCases(t *testing.T) {
	const home = "/Users/me"
	for _, tt := range []struct {
		name, exe string
		want      runOrigin
	}{
		{"applications", "/Applications/Rakko.app/Contents/MacOS/rakko", originInstalled},
		{"a folder of apps", "/Users/me/Applications/Rakko.app/Contents/MacOS/rakko", originInstalled},
		{"downloads", "/Users/me/Downloads/Rakko.app/Contents/MacOS/rakko", originDownloads},
		{"downloads subfolder", "/Users/me/Downloads/build/Rakko.app/Contents/MacOS/rakko", originDownloads},
		{"disk image", "/Volumes/Rakko 1.13/Rakko.app/Contents/MacOS/rakko", originDiskImage},
		{"translocated", "/private/var/folders/x/T/AppTranslocation/9B1C-77/d/Rakko.app/Contents/MacOS/rakko", originTranslocated},
		{"not a bundle", "/tmp/go-build/rakko", originInstalled},
	} {
		if got := runLocation(tt.exe, home); got != tt.want {
			t.Errorf("%s: runLocation = %v, want %v", tt.name, got, tt.want)
		}
	}
	// Another user's Downloads is not ours.
	if got := runLocation("/Users/other/Downloads/Rakko.app/Contents/MacOS/rakko", home); got != originInstalled {
		t.Errorf("another user's Downloads read as %v", got)
	}
}

func TestMoveBundleSettlesItInApplications(t *testing.T) {
	tmp := t.TempDir()
	apps := filepath.Join(tmp, "Applications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(tmp, "Downloads", "Rakko.app")
	if err := os.MkdirAll(filepath.Join(src, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(src, "Contents", "MacOS", "rakko")
	if err := os.WriteFile(marker, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	dest, err := moveBundleToApplications(src, apps)
	if err != nil {
		t.Fatalf("move: %v", err)
	}
	if want := filepath.Join(apps, newBundleName); dest != want {
		t.Fatalf("dest = %q, want %q", dest, want)
	}
	if _, err := os.Stat(filepath.Join(dest, "Contents", "MacOS", "rakko")); err != nil {
		t.Fatalf("the bundle did not land whole: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("the source stayed behind: %v", err)
	}

	// A different bundle in Downloads, with an older Rakko.app already in
	// /Applications: the old one goes to the Trash, never overwritten.
	again := filepath.Join(tmp, "Downloads2", "Rakko.app")
	if err := os.MkdirAll(again, 0o755); err != nil {
		t.Fatal(err)
	}
	trashed := ""
	oldTrash := onbTrashPath
	onbTrashPath = func(path string) (string, error) {
		trashed = path
		return "", os.RemoveAll(path)
	}
	t.Cleanup(func() { onbTrashPath = oldTrash })
	if _, err := moveBundleToApplications(again, apps); err != nil {
		t.Fatalf("second move: %v", err)
	}
	if want := filepath.Join(apps, newBundleName); trashed != want {
		t.Fatalf("the existing app was not trashed: trashed %q, want %q", trashed, want)
	}

	// A Trash that refuses leaves the destination alone and reports why.
	third := filepath.Join(tmp, "Downloads3", "Rakko.app")
	if err := os.MkdirAll(third, 0o755); err != nil {
		t.Fatal(err)
	}
	onbTrashPath = func(string) (string, error) { return "", os.ErrPermission }
	if _, err := moveBundleToApplications(third, apps); err == nil {
		t.Fatal("a failed Trash was reported as a successful move")
	}
	if _, err := os.Stat(third); err != nil {
		t.Fatalf("the failed move lost the source bundle: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the failed Trash lost the installed app: %v", err)
	}

	// A copy that fails leaves the installed app untouched: the old one is
	// neither trashed nor overwritten, and no staging copy is left behind.
	fourth := filepath.Join(tmp, "Downloads4", "Rakko.app")
	if err := os.MkdirAll(filepath.Join(fourth, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fourth, "Contents", "MacOS", "rakko"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	trashed = ""
	onbTrashPath = func(path string) (string, error) { trashed = path; return "", os.RemoveAll(path) }
	oldCopy, oldOnly := onbCopyBundle, onbCopyOnly
	onbCopyOnly = func(string) bool { return true }
	onbCopyBundle = func(string, string) error { return errors.New("disk full") }
	t.Cleanup(func() { onbCopyBundle, onbCopyOnly = oldCopy, oldOnly })
	if _, err := moveBundleToApplications(fourth, apps); err == nil {
		t.Fatal("a failed copy was reported as a successful move")
	}
	if trashed != "" {
		t.Fatalf("the old app was trashed before the new one was whole: %q", trashed)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the failed copy lost the installed app: %v", err)
	}
	entries, err := os.ReadDir(apps)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Fatalf("a staging copy was left behind: %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(fourth, "Contents", "MacOS", "rakko")); err != nil {
		t.Fatalf("the failed move lost the source bundle: %v", err)
	}

	// A read-only source is copied: the copy lands under a staging name
	// beside the destination, the old app goes to the Trash only after it
	// is whole, and the source (a disk image) stays where it is.
	fifth := filepath.Join(tmp, "Volumes", newBundleName)
	if err := os.MkdirAll(filepath.Join(fifth, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fifth, "Contents", "MacOS", "new"), []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	oldMarker := filepath.Join(dest, "Contents", "MacOS", "rakko")
	if err := os.MkdirAll(filepath.Dir(oldMarker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(oldMarker, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	trashCalls, stagedBesideDest := 0, false
	onbCopyBundle = func(src, stage string) error {
		if filepath.Dir(stage) != apps {
			t.Errorf("the copy was staged at %s, not beside the destination", stage)
		}
		if trashCalls != 0 {
			t.Error("the old app was trashed before the copy was whole")
		}
		stagedBesideDest = true
		return copyBundle(src, stage)
	}
	onbTrashPath = func(path string) (string, error) { trashCalls++; return "", os.RemoveAll(path) }
	if _, err := moveBundleToApplications(fifth, apps); err != nil {
		t.Fatalf("copy move: %v", err)
	}
	if !stagedBesideDest || trashCalls != 1 {
		t.Fatalf("the order was not stage-then-trash: staged=%v trash=%d", stagedBesideDest, trashCalls)
	}
	if _, err := os.Stat(filepath.Join(dest, "Contents", "MacOS", "new")); err != nil {
		t.Fatalf("the new bundle did not land: %v", err)
	}
	if _, err := os.Stat(oldMarker); !os.IsNotExist(err) {
		t.Fatalf("the old app is still in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fifth, "Contents", "MacOS", "new")); err != nil {
		t.Fatalf("a read-only source was removed: %v", err)
	}
}

func TestCopyBundleKeepsTheSourceAndStripsQuarantine(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "Rakko.app")
	if err := os.MkdirAll(filepath.Join(src, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Contents", "MacOS", "rakko"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(tmp, "out", "Rakko.app")
	xattr := ""
	old := runXattr
	runXattr = func(bundle string) (string, error) { xattr = bundle; return "", nil }
	t.Cleanup(func() { runXattr = old })
	// copyBundle probes the destination's parent, so create it.
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyBundle(src, dest); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "Contents", "MacOS", "rakko")); err != nil {
		t.Fatalf("the copy is incomplete: %v", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("a copy removed the source: %v", err)
	}
	if xattr != dest {
		t.Fatalf("quarantine strip ran on %q, want %q", xattr, dest)
	}
}

func TestOnboardingStepsFollowTheRunLocation(t *testing.T) {
	for _, tt := range []struct {
		origin runOrigin
		want   []string
	}{
		{originInstalled, []string{onbStepIntro, onbStepPerms}},
		{originDownloads, []string{onbStepIntro, onbStepMove, onbStepPerms}},
		{originDiskImage, []string{onbStepIntro, onbStepMove, onbStepPerms}},
		{originTranslocated, []string{onbStepIntro, onbStepMove, onbStepPerms}},
	} {
		o := &onboardingFeature{origin: tt.origin}
		got := o.steps()
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("origin %v: steps = %v, want %v", tt.origin, got, tt.want)
		}
		// The permissions are the last step in both flows.
		o.step = len(got) - 1
		if !o.last() || o.current() != onbStepPerms {
			t.Errorf("origin %v: last step = %q (last=%v), want %q", tt.origin, o.current(), o.last(), onbStepPerms)
		}
	}
}

func TestOnboardingResumesAfterTheStepItLeft(t *testing.T) {
	cases := []struct {
		name string
		exe  string
		step string
		want string
	}{
		{"a move still pending", "/Users/me/Downloads/Rakko.app/Contents/MacOS/rakko", onbStepMove, onbStepMove},
		{"the move was finished by a launch from Applications", "/Applications/Rakko.app/Contents/MacOS/rakko", onbStepMove, onbStepPerms},
		{"a saved permissions step", "/Applications/Rakko.app/Contents/MacOS/rakko", onbStepPerms, onbStepPerms},
		{"a saved try step from an earlier build", "/Applications/Rakko.app/Contents/MacOS/rakko", "try", onbStepPerms},
		{"a saved try step on a bundle that still has to move", "/Users/me/Downloads/Rakko.app/Contents/MacOS/rakko", "try", onbStepPerms},
		{"a fresh install", "/Applications/Rakko.app/Contents/MacOS/rakko", "", onbStepIntro},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			oldExe, oldHome := onbExecutable, onbHome
			onbExecutable = func() (string, error) { return tt.exe, nil }
			onbHome = func() (string, error) { return "/Users/me", nil }
			t.Cleanup(func() { onbExecutable, onbHome = oldExe, oldHome })
			host := &shelf{settings: settings.Default()}
			host.settings.Onboarding = settings.OnboardingSettings{Pending: true, Step: tt.step}
			o := newOnboarding(host)
			if got := o.current(); got != tt.want {
				t.Fatalf("resumed at %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOnboardingFinishAndSkipPersist(t *testing.T) {
	for _, tt := range []struct {
		name string
		run  func(a *shelf)
		hint string
	}{
		{"finish", (*shelf).onboardingFinish, "Rakko 已就绪"},
		{"skip", (*shelf).onboardingSkip, "随时点刘海呼出"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := onboardingShelf(t)
			tt.run(a)
			if a.settings.Onboarding.Pending {
				t.Fatal("the guide is still owed after it ended")
			}
			if a.settings.Onboarding.Step != "" {
				t.Fatalf("the step stayed at %q", a.settings.Onboarding.Step)
			}
			// The file says the same, so a restart never reopens it.
			saved, err := settings.Load(a.dataDir)
			if err != nil {
				t.Fatal(err)
			}
			if saved.Onboarding.Pending || saved.Onboarding.Step != "" {
				t.Fatalf("settings.json still owes the guide: %+v", saved.Onboarding)
			}
			if a.status != tt.hint {
				t.Fatalf("status = %q, want %q", a.status, tt.hint)
			}
		})
	}
}

// The guide writes nothing of its own into the data directory: no sample
// picture, no onboarding folder. Walking every step and finishing leaves
// only the settings file the app itself saves.
func TestOnboardingWritesNoFilesIntoTheDataDir(t *testing.T) {
	a, view := onboardingShelf(t)
	for i := range a.onb.steps() {
		a.onb.step = i
		view.Frame()
		view.Frame()
	}
	a.onboardingFinish()
	var names []string
	err := filepath.WalkDir(a.dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == a.dataDir {
			return nil
		}
		names = append(names, d.Name())
		if d.IsDir() || strings.HasSuffix(d.Name(), ".png") {
			t.Errorf("the guide wrote %s into the data directory", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name != settings.FileName {
			t.Errorf("the data directory holds %q, want only %q", name, settings.FileName)
		}
	}
}

// The permissions step is the guide's last: 开始使用 — the button and ⏎ / →
// — retires the guide and folds the panel to the notch with the ready
// capsule on it.
func TestOnboardingPermsStepStartsUsing(t *testing.T) {
	t.Run("button", func(t *testing.T) {
		a, view := onboardingShelf(t)
		a.onb.step = len(a.onb.steps()) - 1
		if !a.onb.last() || a.onb.current() != onbStepPerms {
			t.Fatalf("last step = %q, want %q", a.onb.current(), onbStepPerms)
		}
		view.Frame()
		view.Frame()
		if !view.HasText("上一步") {
			t.Fatalf("上一步 is missing from the permissions step; texts %v", view.Texts())
		}
		if err := view.Click("开始使用"); err != nil {
			t.Fatalf("start using: %v", err)
		}
		if a.settings.Onboarding.Pending || a.settings.Onboarding.Step != "" {
			t.Fatalf("开始使用 left the guide owed: %+v", a.settings.Onboarding)
		}
		if a.status != "Rakko 已就绪" {
			t.Fatalf("status = %q, want the ready capsule", a.status)
		}
	})
	t.Run("key", func(t *testing.T) {
		a, _ := onboardingShelf(t)
		a.onb.step = len(a.onb.steps()) - 1
		a.onboardingKey("Enter")
		if a.settings.Onboarding.Pending || a.settings.Onboarding.Step != "" {
			t.Fatalf("⏎ left the guide owed: %+v", a.settings.Onboarding)
		}
		if a.status != "Rakko 已就绪" {
			t.Fatalf("status = %q, want the ready capsule", a.status)
		}
	})
}

func TestOnboardingPageOwnsEveryOpening(t *testing.T) {
	a, _ := onboardingShelf(t)
	a.visible, a.level = false, 1
	a.show(false)
	if a.currentLevel() != 2 || a.currentPage().id() != pageOnboarding {
		t.Fatalf("opening landed on level %d page %s, want the guide", a.currentLevel(), a.currentPage().id())
	}
	// Another page cannot take the panel while the guide is owed.
	a.setTab(pageTray)
	if a.currentPage().id() != pageOnboarding {
		t.Fatalf("the tray took over the panel: %s", a.currentPage().id())
	}
	// Esc on the guide skips it rather than backing out to a search bar.
	a.escape()
	if a.settings.Onboarding.Pending {
		t.Fatal("Esc did not skip the guide")
	}
	// Once it is done the panel opens normally again.
	a.visible, a.level = false, 1
	a.show(false)
	if a.currentPage().id() != pageTray {
		t.Fatalf("after the guide the panel opens on %s", a.currentPage().id())
	}
}

func TestOnboardingCapsuleStaysHiddenWhilePending(t *testing.T) {
	a, view := onboardingShelf(t)
	view.Frame()
	if a.fx.navK != 0 {
		t.Fatalf("the page capsule showed during the guide: navK=%v", a.fx.navK)
	}
	a.onboardingDone()
	view.Frame()
	if a.fx.navK != 1 {
		t.Fatalf("the capsule did not come back once the guide retired: navK=%v", a.fx.navK)
	}
}

func TestOnboardingPermissionRowAsksOnlyItsOwn(t *testing.T) {
	st, clk := stubPermissions(t)
	a, view := onboardingShelf(t)
	a.onb.step = 1 // the permissions step when installed
	if a.onb.current() != onbStepPerms {
		t.Fatalf("step = %s, want the permissions", a.onb.current())
	}
	view.Frame()
	view.Frame()
	if err := view.Click("辅助功能 · 刘海拖放保护"); err != nil {
		t.Fatalf("permission row: %v", err)
	}
	if st.axRequests != 1 || st.fdaRequests != 0 || st.scRequests != 0 {
		t.Fatalf("the row asked for more than itself: fda=%d ax=%d screen=%d", st.fdaRequests, st.axRequests, st.scRequests)
	}
	if a.perm.stage != permissionWaiting {
		t.Fatalf("the walk did not start: stage %v", a.perm.stage)
	}
	st.ax = true
	clk.fireLast(t)
	if a.perm.stage != permissionIdle {
		t.Fatalf("the single-row walk did not end: stage %v", a.perm.stage)
	}
}

func TestEnglishOnboardingPagesSayEnglishAndFit(t *testing.T) {
	a, view := onboardingShelf(t)
	a.locale = "en"
	view.SetLocale("en")
	drain := fitSink(t)
	for i := range a.onb.steps() {
		a.onb.step = i
		view.Frame()
		drain() // the first frame of a step has no boxes to measure
		view.Frame()
		where := string(a.onb.current())
		assertNoHan(t, where, view.Texts())
		assertPageFits(t, view, where, drain(), a.sheetTop())
	}
}

func TestOnboardingIsOwedOnlyForAFreshInstall(t *testing.T) {
	const broken = "settings.json is not valid JSON"
	for _, tt := range []struct {
		name        string
		fresh, save bool
		err         error
		want        bool
	}{
		{"a fresh install", true, false, nil, true},
		{"an upgraded install", false, false, nil, false},
		{"a relaunch mid-guide", false, true, nil, true},
		{"a fresh install whose file could not be made", true, false, errors.New(broken), false},
		{"an upgraded install with a broken file", false, false, errors.New(broken), false},
		{"a mid-guide file that cannot be read", false, true, errors.New(broken), false},
	} {
		if got := onboardingOwed(tt.fresh, tt.err, tt.save); got != tt.want {
			t.Errorf("%s: onboardingOwed = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A linked home (or macOS's own /var -> /private/var) must not hide the
// Downloads rule: the resolved bundle and the resolved home are compared.
func TestRunLocationFollowsALinkedHome(t *testing.T) {
	tmp := t.TempDir()
	real := filepath.Join(tmp, "real")
	bundle := filepath.Join(real, "Downloads", "Rakko.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bundle, "Contents", "MacOS", "rakko")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(tmp, "linked-home")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := runLocation(exe, link); got != originDownloads {
		t.Fatalf("a linked home read as %v, want Downloads", got)
	}
}

// ⏎ / → on the move step runs the move itself — the primary button's
// action — and the app reopens from the new bundle at the permissions.
func TestOnboardingMoveKeySettlesTheBundle(t *testing.T) {
	tmp := t.TempDir()
	bundle := filepath.Join(tmp, "Downloads", newBundleName)
	exe := filepath.Join(bundle, "Contents", "MacOS", "Rakko")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	apps := filepath.Join(tmp, "Applications")
	if err := os.MkdirAll(apps, 0o755); err != nil {
		t.Fatal(err)
	}
	a, _ := onboardingShelf(t)
	a.onb.origin = originDownloads
	a.onb.step = 1
	if a.onb.current() != onbStepMove {
		t.Fatalf("step = %s, want the move", a.onb.current())
	}

	// The move runs off the main thread and hands its results back through
	// the dispatch seam; the test plays the main thread by pumping them.
	queue := make(chan func(), 8)
	oldExe, oldApps, oldDispatch, oldAfter := onbExecutable, onbApplicationsDir, onbMoveDispatch, onbMoveAfterFunc
	oldPlan, oldQuit := onbOpenPlan, onbQuit
	onbExecutable = func() (string, error) { return exe, nil }
	onbApplicationsDir = apps
	onbMoveDispatch = func(fn func()) { queue <- fn }
	onbMoveAfterFunc = func(_ time.Duration, fn func()) permissionTimer {
		fn()
		return &fakePermissionTimer{}
	}
	var plan relaunchPlan
	opened, quit := false, false
	onbOpenPlan = func(p relaunchPlan) error { opened, plan = true, p; return nil }
	onbQuit = func() { quit = true }
	t.Cleanup(func() {
		onbExecutable, onbApplicationsDir, onbMoveDispatch, onbMoveAfterFunc = oldExe, oldApps, oldDispatch, oldAfter
		onbOpenPlan, onbQuit = oldPlan, oldQuit
	})

	a.onboardingKey("Enter")
	deadline := time.After(10 * time.Second)
	for !quit {
		select {
		case fn := <-queue:
			fn()
		case <-deadline:
			t.Fatalf("the move never finished; moveErr=%v", a.onb.moveErr)
		}
	}
	dest := filepath.Join(apps, newBundleName)
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the bundle did not land: %v (moveErr=%v)", err, a.onb.moveErr)
	}
	if !a.onb.moved {
		t.Fatal("the guide does not know the move succeeded")
	}
	if !opened {
		t.Fatal("the move did not reopen the app")
	}
	if !strings.Contains(strings.Join(plan.Args, " "), dest) {
		t.Fatalf("the relaunch plan does not name the moved bundle: %+v", plan)
	}
	if a.settings.Onboarding.Step != onbStepPerms {
		t.Fatalf("the step was not persisted before the relaunch: %q", a.settings.Onboarding.Step)
	}
}

// A page that is no longer offered falls back to the tray: once the guide
// ends its page is disabled, and nothing may land on it.
func TestSetLevelPageNeverLandsOnADisabledPage(t *testing.T) {
	a, _ := onboardingShelf(t)
	a.visible, a.level = true, 1
	a.onboardingDone()
	a.setLevelPage(2, pageOnboarding)
	if a.currentLevel() != 2 || a.currentPage().id() != pageTray {
		t.Fatalf("landed on level %d page %s, want the tray", a.currentLevel(), a.currentPage().id())
	}
}
