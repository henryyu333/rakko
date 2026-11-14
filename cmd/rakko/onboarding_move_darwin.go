//go:build darwin

package main

// Where the running bundle lives, how it moves into the Applications
// folder, and how the moved app reopens. macOS only: the move, the
// translocation check and the quarantine strip are the platform's.

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
)

// --- run location ------------------------------------------------------------

// onboardingOriginSeams keeps the location probe and the move testable: no
// unit test touches /Applications, the Trash or the real executable.
var (
	onbExecutable = func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
		return exe, nil
	}
	// onbHome is where ~/Downloads is looked for. RAKKO_ONBOARDING_HOME
	// points it at a temporary tree for the native smoke, so a copy can
	// "run from Downloads" without touching the real folder.
	onbHome = func() (string, error) {
		if dir := os.Getenv("RAKKO_ONBOARDING_HOME"); dir != "" {
			return dir, nil
		}
		return os.UserHomeDir()
	}
	// onbApplicationsDir is where the move lands. /Applications in the app;
	// RAKKO_ONBOARDING_APPS points it at a temporary folder for the native
	// smoke, so no test can move anything into the real Applications.
	onbApplicationsDir = func() string {
		if dir := os.Getenv("RAKKO_ONBOARDING_APPS"); dir != "" {
			return dir
		}
		return "/Applications"
	}()
	onbTrashPath = platform.TrashFile
	onbOpenPlan  = relaunchStart
	onbQuit      = relaunchQuit
)

// onboardingOrigin answers where the running bundle lives. Only the four
// cases matter: inside an Applications folder (settled, whatever the
// volume), inside ~/Downloads, on a mounted image, and the translocated
// path Gatekeeper substitutes when a quarantined app launches straight
// from a DMG.
func onboardingOrigin() runOrigin {
	exe, err := onbExecutable()
	if err != nil {
		return originInstalled
	}
	home, _ := onbHome()
	return runLocation(exe, home)
}

// runLocation is the pure answer: exe is the resolved executable path,
// home the user's home directory. Translocation resolves the staged
// bundle back to where the user put it before judging.
func runLocation(exe, home string) runOrigin {
	// The executable comes resolved (onbExecutable follows symlinks), so
	// the home must be too: /var is a link to /private/var here, and a
	// home on a linked volume is just as ordinary.
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	bundle, ok := appBundleForExecutable(exe)
	if !ok {
		return originInstalled // go run, go test: not a bundle to move
	}
	if resolved, err := filepath.EvalSymlinks(bundle); err == nil {
		bundle = resolved
	}
	if strings.Contains(bundle, "/AppTranslocation/") {
		if real := translocatedSource(bundle); real != "" {
			bundle = real
		} else {
			return originTranslocated
		}
	}
	if strings.HasPrefix(bundle, "/Volumes/") {
		return originDiskImage
	}
	if home != "" && withinDir(bundle, filepath.Join(home, "Downloads")) {
		return originDownloads
	}
	return originInstalled
}

// withinDir reports whether path sits inside dir (not dir itself: the
// bundle in ~/Downloads, never the folder).
func withinDir(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

// translocatedSource recovers the bundle a translocated app was staged
// from: com.apple.quarantine's staging area keeps the original under the
// same AppTranslocation uuid's sibling. When nothing resolves, the
// translocated path itself still says the app was staged.
func translocatedSource(bundle string) string {
	// /private/var/folders/…/AppTranslocation/<uuid>/d/Rakko.app: the
	// original bundle's location is what the user's own record says — the
	// path the app was opened from, which the quarantine attribute does
	// not preserve. With no reliable way back, still call it translocated.
	return ""
}

// --- the move ---------------------------------------------------------------

// onbCopyOnly reports a source that has to be copied rather than moved: a
// read-only volume, or Gatekeeper's staging area. A test replaces it to run
// the copy path against a writable temporary bundle.
var onbCopyOnly = func(bundle string) bool {
	return strings.HasPrefix(bundle, "/Volumes/") || strings.Contains(bundle, "/AppTranslocation/")
}

// onbCopyBundle is that copy. A test replaces it to fail one.
var onbCopyBundle = copyBundle

// onbMoveAfterFunc and onbMoveDispatch mirror the permission flow's seams:
// the move's own clock, so a test can hold the relaunch.
var (
	onbMoveAfterFunc = func(d time.Duration, fn func()) permissionTimer {
		return time.AfterFunc(d, fn)
	}
	onbMoveDispatch = mygo.RunOnMain
)

// onboardingMove runs the 移到「应用程序」 action: the bundle copies or
// moves to /Applications (a disk image copies; a local folder moves), an
// older Rakko.app at the destination goes to the Trash first, and the app
// relaunches from the new home after the row has shown the check mark.
// Everything after the user's click is best-effort: a failure stays on
// the row and 继续 still works.
func (a *shelf) onboardingMove() {
	o := a.onb
	if o == nil || o.moving || o.moved {
		return
	}
	exe, err := onbExecutable()
	if err != nil {
		o.moveErr = err
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	bundle, ok := appBundleForExecutable(exe)
	if !ok {
		o.moveErr = fmt.Errorf("not inside an app bundle")
		if a.win != nil {
			a.win.Invalidate()
		}
		return
	}
	o.moving = true
	o.moveErr = nil
	if a.win != nil {
		a.win.Invalidate()
	}
	move := func() (string, error) { return moveBundleToApplications(bundle, onbApplicationsDir) }
	go func() {
		dest, err := move()
		onbMoveDispatch(func() {
			o.moving = false
			if err != nil {
				o.moveErr = err
				if a.win != nil {
					a.win.Invalidate()
				}
				return
			}
			o.moved = true
			// The next step is persisted before the relaunch so the new
			// process opens on the permissions, not the intro.
			a.saveOnboarding(o.steps()[o.step+1])
			onbMoveAfterFunc(900*time.Millisecond, func() {
				onbMoveDispatch(func() { a.onboardingRelaunch(dest) })
			})
			if a.win != nil {
				a.win.Invalidate()
			}
		})
	}()
}

// onboardingRelaunch reopens the app from its new bundle, after the old
// process lets the single-instance lock go — the same wait-then-open the
// permission flow's relaunch plans.
func (a *shelf) onboardingRelaunch(bundle string) {
	plan := relaunchPlanForBundle(bundle, os.Args[1:], os.Getpid())
	if err := onbOpenPlan(plan); err != nil {
		a.report(err)
		return
	}
	onbQuit()
}

// relaunchPlanForBundle is relaunchPlanFor with the bundle already known:
// the moved app reopens at dest even while the old executable's path still
// points at the source directory.
func relaunchPlanForBundle(bundle string, args []string, pid int) relaunchPlan {
	var b strings.Builder
	fmt.Fprintf(&b, "while kill -0 %d 2>/dev/null; do sleep 0.1; done; sleep 0.3; ", pid)
	fmt.Fprintf(&b, "exec /usr/bin/open -n %s", shellQuote(bundle))
	if len(args) > 0 {
		b.WriteString(" --args")
	}
	for _, arg := range args {
		b.WriteByte(' ')
		b.WriteString(shellQuote(arg))
	}
	return relaunchPlan{Exec: "/bin/sh", Args: []string{"-c", b.String()}}
}

// moveBundleToApplications settles bundle at destDir/Rakko.app. The new
// bundle lands whole under a staging name beside the destination first;
// only then does an older Rakko.app go to the Trash, and the last rename is
// atomic on the same volume. A failure at any step leaves the old app where
// it was — the user never ends up with neither — and a staged move goes
// back to its own folder instead of being deleted with the copy.
func moveBundleToApplications(bundle, destDir string) (string, error) {
	dest := filepath.Join(destDir, newBundleName)
	if bundle == dest {
		return dest, nil
	}
	stage := filepath.Join(destDir, "."+newBundleName+".new")
	_ = os.RemoveAll(stage) // a crashed earlier attempt left nothing usable
	readOnly := onbCopyOnly(bundle)
	// staged: the app now sits at stage because it was moved there; a copy
	// leaves the source in place and the stage is its duplicate.
	staged, removeSource := false, false
	switch {
	case readOnly:
		if err := onbCopyBundle(bundle, stage); err != nil {
			_ = os.RemoveAll(stage)
			return "", err
		}
	default:
		err := os.Rename(bundle, stage)
		switch {
		case err == nil:
			staged = true
		case errorsIsEXDEV(err):
			// Downloads and /Applications on different volumes: copy, then
			// drop the source once the new app is in place.
			if cerr := onbCopyBundle(bundle, stage); cerr != nil {
				_ = os.RemoveAll(stage)
				return "", cerr
			}
			removeSource = true
		default:
			return "", err
		}
	}
	// abandon puts the app back the way the user had it: a staged move goes
	// home, a copy is dropped.
	abandon := func() {
		if staged {
			if err := os.Rename(stage, bundle); err != nil {
				log.Printf("%s: keeping the new bundle at %s; moving it back to %s failed: %v", appName, stage, bundle, err)
			}
			return
		}
		_ = os.RemoveAll(stage)
	}
	if _, err := os.Stat(dest); err == nil {
		if _, err := onbTrashPath(dest); err != nil {
			abandon()
			return "", fmt.Errorf("existing %s could not be trashed: %w", dest, err)
		}
	}
	if err := os.Rename(stage, dest); err != nil {
		abandon()
		return "", err
	}
	if removeSource {
		if err := os.RemoveAll(bundle); err != nil {
			log.Printf("%s: moved to %s but the old copy at %s stayed: %v", appName, dest, bundle, err)
		}
	}
	return dest, nil
}

func errorsIsEXDEV(err error) bool {
	for e := err; e != nil; e = unwrapErr(e) {
		if e == syscall.EXDEV {
			return true
		}
	}
	return false
}

func unwrapErr(err error) error {
	type unwrapper interface{ Unwrap() error }
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

// copyBundle copies the app bundle wholesale (symlinks preserved) for the
// read-only sources. It fails before writing when the destination's parent
// refuses a probe file, so the error names the real cause — /Applications
// needing admin, a full disk — rather than a missing file deep inside.
func copyBundle(src, dest string) error {
	parent := filepath.Dir(dest)
	probe, err := os.CreateTemp(parent, ".rakko-move-*")
	if err != nil {
		return err
	}
	probe.Close()
	os.Remove(probe.Name())
	if err := copyTree(src, dest); err != nil {
		os.RemoveAll(dest)
		return err
	}
	// A copied bundle keeps the quarantine bit the source had: without
	// clearing it the relaunched app is translocated again — moved in
	// name only. Clearing is best-effort; a failure keeps the copy.
	if out, err := runXattr(dest); err != nil {
		log.Printf("%s: copied to %s but quarantine stayed (%v: %s)", appName, dest, err, out)
	}
	return nil
}

// runXattr is a seam for the quarantine strip, which a unit test replaces.
var runXattr = func(bundle string) (string, error) {
	out, err := exec.Command("/usr/bin/xattr", "-dr", "com.apple.quarantine", bundle).CombinedOutput()
	return string(out), err
}

// copyTree duplicates a directory tree, preserving modes and symlinks.
func copyTree(src, dest string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dest)
	}
	if !info.IsDir() {
		return copyOnboardingFile(src, dest, info.Mode())
	}
	if err := os.MkdirAll(dest, info.Mode().Perm()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dest, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyOnboardingFile(src, dest string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
