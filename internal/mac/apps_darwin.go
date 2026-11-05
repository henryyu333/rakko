//go:build darwin

package mac

import (
	"os"
	"strings"
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
