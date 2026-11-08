//go:build darwin

package mac

import (
	"os"
	"strings"

	"github.com/ebitengine/purego/objc"
)

// NSApplicationActivationOptions, as NSRunningApplication expects them.
const (
	nsApplicationActivateAllWindows        = 1 << 0
	nsApplicationActivateIgnoringOtherApps = 1 << 1
)

// FrontmostApp returns the frontmost application's bundle identifier and
// localized name through NSWorkspace.frontmostApplication — the app the
// user is copying from. Both are "" when the system reports none (early
// launch, no GUI session). Read-only, no permission needed. Main thread
// only.
func FrontmostApp() (bundleID, name string) {
	withPool(func() {
		app := send(send(class("NSWorkspace"), "sharedWorkspace"), "frontmostApplication")
		if app == 0 {
			return
		}
		bundleID = goString(send(app, "bundleIdentifier"))
		name = goString(send(app, "localizedName"))
	})
	return bundleID, name
}

// FrontmostPID returns the process identifier of the frontmost
// application, 0 when the system reports none. Main thread only.
func FrontmostPID() int {
	var pid int
	withPool(func() {
		app := send(send(class("NSWorkspace"), "sharedWorkspace"), "frontmostApplication")
		if app == 0 {
			return
		}
		pid = sendInt(app, "processIdentifier")
	})
	return pid
}

// OwnPID returns this process's process identifier, for telling the
// frontmost application apart from ShelfU itself. Main thread only.
func OwnPID() int { return os.Getpid() }

// AppActive reports whether this application is the active one
// (NSApplication.isActive). An accessory app that never activated anything
// still reports false while its panel holds the keyboard. Main thread only.
func AppActive() bool {
	var active bool
	withPool(func() {
		app := send(class("NSApplication"), "sharedApplication")
		if app == 0 {
			return
		}
		active = sendInt(app, "isActive") != 0
	})
	return active
}

// ActivateSelf brings this application to the front, so a window can take
// the keyboard and the user can type into it. Focus stealing: call only
// for a flow the user started explicitly (the clipboard hotkey). Main
// thread only.
func ActivateSelf() {
	withPool(func() {
		self := send(class("NSRunningApplication"), "currentApplication")
		if self != 0 {
			send(self, "activateWithOptions:", uintptr(nsApplicationActivateIgnoringOtherApps))
			return
		}
		send(send(class("NSApplication"), "sharedApplication"), "activateIgnoringOtherApps:", 1)
	})
}

// ActivateApp brings the application with the process identifier pid to
// the front and reports whether the system accepted the request. Used to
// hand the keyboard back to the app the user copied from. Main thread
// only.
func ActivateApp(pid int) bool {
	if pid <= 0 {
		return false
	}
	var ok bool
	withPool(func() {
		app := send(class("NSRunningApplication"), "runningApplicationWithProcessIdentifier:", uintptr(pid))
		if app == 0 {
			return
		}
		ok = sendInt(app, "activateWithOptions:", uintptr(nsApplicationActivateIgnoringOtherApps)) != 0
	})
	return ok
}

// DeactivateSelf resigns this application's active status by hiding it
// (NSApplication.hide:), used when the app to hand focus back to is gone.
// The shelf only activates for the keyboard-driven search flow; hiding
// afterwards leaves no key window and no frontmost shadow of a dead app.
// Main thread only.
func DeactivateSelf() {
	withPool(func() {
		send(send(class("NSApplication"), "sharedApplication"), "hide:", 0)
	})
}

// SetWindowAppearance pins a window to the dark or light appearance
// (NSWindow.appearance = NSAppearanceNameDarkAqua / Aqua), so its title bar
// and chrome draw accordingly regardless of the system setting. ShelfU's UI
// is always dark; the Settings window calls this with dark=true. Main
// thread only.
func SetWindowAppearance(window uintptr, dark bool) {
	if window == 0 || Init() != nil {
		return
	}
	withPool(func() {
		name := "NSAppearanceNameAqua"
		if dark {
			name = "NSAppearanceNameDarkAqua"
		}
		appearance := send(class("NSAppearance"), "appearanceNamed:", uintptr(nsString(name)))
		if appearance != 0 {
			send(id(window), "setAppearance:", uintptr(appearance))
		}
	})
}

// AppNameForBundleID resolves a display name for a bundle identifier: the
// name of the running application with that identifier, else the bundle's
// localized name from disk, else "". Read-only, no permission needed.
// Main thread only.
func AppNameForBundleID(bundleID string) string {
	if strings.TrimSpace(bundleID) == "" {
		return ""
	}
	var name string
	withPool(func() {
		apps := send(class("NSRunningApplication"), "runningApplicationsWithBundleIdentifier:",
			uintptr(nsString(bundleID)))
		if sendInt(apps, "count") > 0 {
			name = goString(send(send(apps, "objectAtIndex:", 0), "localizedName"))
		}
		if name != "" {
			return
		}
		url := send(send(class("NSWorkspace"), "sharedWorkspace"), "URLForApplicationWithBundleIdentifier:",
			uintptr(nsString(bundleID)))
		if url == 0 {
			return
		}
		info := send(send(class("NSBundle"), "bundleWithURL:", uintptr(url)), "localizedInfoDictionary")
		for _, key := range []string{"CFBundleDisplayName", "CFBundleName"} {
			if value := send(info, "objectForKey:", uintptr(nsString(key))); value != 0 {
				if name = goString(value); name != "" {
					return
				}
			}
		}
	})
	return name
}

// ReopenApp opens a bundle id's application the way the Dock's icon does:
// NSWorkspace.launchApplicationAtURL with the default options — it
// launches the app when it is not running, and for a running one it hands
// the request to the app itself, which is why a Dock click brings back
// what the Dock click shows. The explicit unhide after it covers an app
// the user hid with NSApp.hide (the hidden round's controlled fixture,
// which stands in for that state). Minimization is not exercised here and
// is not promised by this call's own tests. Existing instances keep their
// URL→running-app mapping, so this never starts a second copy.
// Main thread only.
func ReopenApp(bundleID string) bool {
	if bundleID == "" {
		return false
	}
	var ok bool
	withPool(func() {
		ws := send(class("NSWorkspace"), "sharedWorkspace")
		url := send(ws, "URLForApplicationWithBundleIdentifier:", uintptr(nsString(bundleID)))
		if url == 0 {
			return
		}
		// NSWorkspaceLaunchDefault == NSWorkspaceLaunchAsync (AppKit's
		// own header aliases it): the Dock-style launch-or-activate
		// without the "new instance"/"not running" variants, and the
		// workspace hands the work off instead of blocking this thread
		// until the app is up — a click on the UI must not stall on the
		// target's own launch time.
		configuration := send(class("NSDictionary"), "dictionary")
		launched := send(ws, "launchApplicationAtURL:options:configuration:error:",
			uintptr(url), uintptr(nsWorkspaceLaunchDefault), uintptr(configuration), 0)
		ok = launched != 0
		if !ok {
			return
		}
		// The launch options do not promise unhide for an app the user
		// hid themselves (Job 11.4's own Dock icon does, its open call
		// does not). A hidden app hidden this way stays frontmost
		// nothing-to-see unless the receiver unhides: AppKit's own
		// explicit "-unhide" seal for the running instance this URL
		// describes. Main thread only, read-only besides the request.
		apps := send(class("NSRunningApplication"),
			"runningApplicationsWithBundleIdentifier:", uintptr(nsString(bundleID)))
		if sendInt(apps, "count") > 0 {
			send(send(apps, "objectAtIndex:", 0), "unhide")
		}
	})
	return ok
}

// RunningApp is one entry of the app switcher's snapshot: a regular GUI
// application NSWorkspace reports as running.
type RunningApp struct {
	// BundleID is the stable identity: activation is by pid, but pinning
	// and re-launching resolve the bundle id again.
	BundleID string
	// Name is the app's localized name, "" when the system reports none.
	Name string
	// PID is valid for this snapshot only; the process may exit any time.
	PID int
}

// nsWorkspaceLaunchDefault is AppKit's NSWorkspaceLaunchDefault, which its
// header aliases to NSWorkspaceLaunchAsync (1<<16) — an asynchronous,
// Dock-equivalent open. The raw constant: purego has no enum table.
const nsWorkspaceLaunchDefault = 1 << 16

// nsApplicationActivationPolicyRegular selects the ordinary foreground
// applications: LaunchServices agents and accessories (menu-bar helpers
// like ShelfU itself) do not belong in an app switcher.
const nsApplicationActivationPolicyRegular = 0

// RunningApps lists the regular GUI applications running now, through
// NSWorkspace.runningApplications. Agents and accessory processes are
// left out, and so is ShelfU itself. Apps without a bundle identifier
// cannot be pinned or relaunched, so they are skipped too. Read-only, no
// permission needed. Main thread only.
func RunningApps() []RunningApp {
	var out []RunningApp
	own := OwnPID()
	withPool(func() {
		apps := send(send(class("NSWorkspace"), "sharedWorkspace"), "runningApplications")
		n := sendInt(apps, "count")
		out = make([]RunningApp, 0, n)
		for i := range n {
			app := send(apps, "objectAtIndex:", uintptr(i))
			if app == 0 {
				continue
			}
			if sendInt(app, "activationPolicy") != nsApplicationActivationPolicyRegular {
				continue
			}
			if sendInt(app, "isTerminated") != 0 {
				continue
			}
			bundleID := goString(send(app, "bundleIdentifier"))
			pid := sendInt(app, "processIdentifier")
			if bundleID == "" || pid == own {
				continue
			}
			out = append(out, RunningApp{
				BundleID: bundleID,
				Name:     goString(send(app, "localizedName")),
				PID:      pid,
			})
		}
	})
	return out
}

// AppPathForBundleID resolves a bundle identifier to its application
// bundle path through NSWorkspace.URLForApplicationWithBundleIdentifier,
// or "" when nothing installed answers for it — the app was uninstalled,
// or the id was never an application. Read-only, no permission needed.
// Main thread only.
func AppPathForBundleID(bundleID string) string {
	if bundleID == "" {
		return ""
	}
	var path string
	withPool(func() {
		url := send(send(class("NSWorkspace"), "sharedWorkspace"),
			"URLForApplicationWithBundleIdentifier:", uintptr(nsString(bundleID)))
		if url != 0 {
			path = goString(send(url, "path"))
		}
	})
	return path
}

// ObserveActiveApp reports every application activation through
// NSWorkspaceDidActivateApplicationNotification. The clipboard recorder
// needs the activation order, not just the foreground app at poll time: a
// copy followed by a quick app switch must still be attributed to the app
// the user copied from. The block runs on the main thread. Install and stop
// on the main thread; no permission is needed.
func ObserveActiveApp(changed func()) (stop func()) {
	if err := Init(); err != nil {
		return func() {}
	}
	var center, token id
	withPool(func() {
		ws := send(class("NSWorkspace"), "sharedWorkspace")
		center = send(ws, "notificationCenter")
		block := objc.NewBlock(func(_ objc.Block, _ id) { changed() })
		defer block.Release()
		token = send(center, "addObserverForName:object:queue:usingBlock:",
			uintptr(nsString("NSWorkspaceDidActivateApplicationNotification")),
			0, uintptr(send(class("NSOperationQueue"), "mainQueue")), uintptr(block))
		send(token, "retain")
	})
	return func() {
		withPool(func() {
			if token != 0 {
				send(center, "removeObserver:", uintptr(token))
				send(token, "release")
				token = 0
			}
		})
	}
}
