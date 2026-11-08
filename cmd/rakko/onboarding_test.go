//go:build darwin

package main

// The first-launch guide: where the bundle runs, the move that settles it in
// /Applications, the pending bit that survives restarts, the page that owns
// the panel while it is owed, and the two ways the try step lands a file.

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
		{originInstalled, []string{onbStepIntro, onbStepPerms, onbStepTry}},
		{originDownloads, []string{onbStepIntro, onbStepMove, onbStepPerms, onbStepTry}},
		{originDiskImage, []string{onbStepIntro, onbStepMove, onbStepPerms, onbStepTry}},
		{originTranslocated, []string{onbStepIntro, onbStepMove, onbStepPerms, onbStepTry}},
	} {
		o := &onboardingFeature{origin: tt.origin}
		got := o.steps()
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("origin %v: steps = %v, want %v", tt.origin, got, tt.want)
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

func TestOnboardingTryKeepsPendingForStartUsing(t *testing.T) {
	a, _ := onboardingShelf(t)
	a.onb.step = len(a.onb.steps()) - 1
	a.onb.tried = false
	a.onboardingCatch()
	if !a.onb.tried {
		t.Fatal("the try step was not marked done")
	}
	if !a.settings.Onboarding.Pending {
		t.Fatal("the guide closed itself before 开始使用")
	}
	if !a.onb.last() {
		t.Fatal("the try step is not the last one")
	}
	a.onboardingFinish()
	if a.settings.Onboarding.Pending {
		t.Fatal("开始使用 did not retire the guide")
	}
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

func TestOnboardingCapsuleWaitsForTheFinishBlock(t *testing.T) {
	a, view := onboardingShelf(t)
	view.Frame()
	if a.fx.navK != 0 {
		t.Fatalf("the page capsule showed during the guide: navK=%v", a.fx.navK)
	}
	a.onb.tried = true
	view.Frame()
	if a.fx.navK != 1 {
		t.Fatalf("the capsule did not come back for the finish block: navK=%v", a.fx.navK)
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

func TestOnboardingDemoDropLandsTheSampleInTheTray(t *testing.T) {
	a, view := onboardingShelf(t)
	a.onb.step = len(a.onb.steps()) - 1
	view.Frame()
	a.onboardingDemoDrop()
	if !a.onb.tried {
		t.Fatal("the demo drop did not complete the try step")
	}
	if len(a.store.Items) != 1 {
		t.Fatalf("the tray holds %d items, want the sample", len(a.store.Items))
	}
	if got := a.store.Items[0].Path; !strings.HasSuffix(got, ".png") {
		t.Fatalf("the demo file is %q", got)
	}
	view.Frame()
	if !view.HasText("就这些") {
		t.Fatalf("the finish block did not draw; texts %v", view.Texts())
	}
	if !view.HasText("开始使用") {
		t.Fatalf("开始使用 is missing from the finish block; texts %v", view.Texts())
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

func TestOnboardingSampleIsRemovableAndNamedForItsDay(t *testing.T) {
	a, _ := onboardingShelf(t)
	o := a.onb
	dir := filepath.Join(a.dataDir, "onboarding")
	first, err := o.ensureSample()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != dir {
		t.Fatalf("the sample went to %s, want %s", filepath.Dir(first), dir)
	}
	if !strings.Contains(filepath.Base(first), time.Now().Format("2006-01-02")) {
		t.Fatalf("the sample name %q does not carry today's date", filepath.Base(first))
	}
	// A second call reuses the same file instead of writing another.
	second, err := o.ensureSample()
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("ensureSample wrote a second file: %q vs %q", second, first)
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	third, err := o.ensureSample()
	if err != nil {
		t.Fatal(err)
	}
	if third != first {
		t.Fatalf("a removed sample was not rewritten: %q vs %q", third, first)
	}
}

func TestOnboardingIsOwedOnlyForAFreshInstall(t *testing.T) {
	const broken = "settings.json is not valid JSON"
	for _, tt := range []struct {
		name  string
		fresh bool
		err   error
		want  bool
	}{
		{"a fresh install", true, nil, true},
		{"an upgraded install", false, nil, false},
		{"a fresh install whose file could not be made", true, errors.New(broken), false},
		{"an upgraded install with a broken file", false, errors.New(broken), false},
	} {
		if got := onboardingOwed(tt.fresh, tt.err); got != tt.want {
			t.Errorf("%s: onboardingOwed = %v, want %v", tt.name, got, tt.want)
		}
	}
}

// A click on the sample chip (no drag) plays the demo, as the prototype's
// chip does: the pointer path the panel really receives.
func TestOnboardingChipClickPlaysTheDemo(t *testing.T) {
	a, view := onboardingShelf(t)
	a.onb.step = len(a.onb.steps()) - 1
	view.Frame()
	view.Frame()
	name := a.onb.sampleName(a.appLoc())
	if err := view.Click(name); err != nil {
		t.Fatalf("chip click: %v", err)
	}
	if a.onb.demoAt.IsZero() {
		t.Fatal("the chip click did not arm the demo")
	}

	// The demo's clock lands the file on its last phase.
	view.Advance(900 * time.Millisecond)
	view.Frame()
	if !a.onb.tried || len(a.store.Items) != 1 {
		t.Fatalf("the demo did not land the sample: tried=%v items=%d", a.onb.tried, len(a.store.Items))
	}
}
