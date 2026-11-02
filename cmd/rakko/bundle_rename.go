//go:build darwin

package main

// An in-app update of an earlier name replaced whatever bundle it found at
// Shelf.app or ShelfU.app with the new app. This app ships as Rakko.app, so a
// bundle that still lives at one of the old names is moved to .../Rakko.app
// on its first launch and reopened from there. Renaming a running bundle is
// safe: the process keeps running from the moved files, and the reopened
// instance owns the visible UI.

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	// newBundleName is the bundle this app ships as.
	newBundleName = "Rakko.app"
)

// oldBundleNames are the bundles the earlier names installed and updated
// into, newest first. Only an executable inside one of them is renamed.
var oldBundleNames = []string{"ShelfU.app", "Shelf.app"}

// isOldBundleName reports whether name is one of the bundles an earlier
// version of the app ran from.
func isOldBundleName(name string) bool {
	for _, old := range oldBundleNames {
		if name == old {
			return true
		}
	}
	return false
}

// selfRenameTarget reports the rename an executable needs: the bundle it
// runs from and the bundle it should run from. ok is false unless exe is
// inside a bundle called ShelfU.app or Shelf.app, which are the only shapes
// an old installed copy has.
func selfRenameTarget(exe string) (oldBundle, newBundle string, ok bool) {
	exe = filepath.Clean(exe)
	macOSDir := filepath.Dir(exe)
	contentsDir := filepath.Dir(macOSDir)
	bundle := filepath.Dir(contentsDir)
	if filepath.Base(macOSDir) != "MacOS" || filepath.Base(contentsDir) != "Contents" || !isOldBundleName(filepath.Base(bundle)) {
		return "", "", false
	}
	return bundle, filepath.Join(filepath.Dir(bundle), newBundleName), true
}

// selfRenameBundle moves a .../ShelfU.app or .../Shelf.app bundle to
// .../Rakko.app and reopens the app from there. It does nothing when the app
// does not run from such a bundle (go run, go test), when the renamed bundle
// already exists, or when the rename fails (a read-only directory, App
// Translocation); the app then keeps running from where it is.
func selfRenameBundle() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	oldBundle, nextBundle, ok := selfRenameTarget(exe)
	if !ok {
		return
	}
	if _, serr := os.Stat(nextBundle); serr == nil {
		return
	}
	if rerr := os.Rename(oldBundle, nextBundle); rerr != nil {
		log.Printf("%s: 无法把 %s 重命名为 %s，继续从原位置运行：%v", appName, oldBundle, nextBundle, rerr)
		return
	}
	args := append([]string{"-n", nextBundle, "--args"}, os.Args[1:]...)
	if oerr := exec.Command("/usr/bin/open", args...).Run(); oerr != nil {
		log.Printf("%s: 应用包已改名为 %s，但重新打开失败，继续从新位置运行：%v", appName, nextBundle, oerr)
		return
	}
	log.Printf("%s: 应用包已改名为 %s，正在重新打开", appName, nextBundle)
	os.Exit(0)
}
