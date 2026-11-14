//go:build windows

package platform

// Which binary may be written into an agent tool's configuration. On Windows
// Rakko installs per user with NSIS: the executable lives in the install
// directory the uninstall entry records (or the default one), and the
// updater renames a running executable to .Rakko.exe.old-<pid> inside that
// same directory. A development build runs from a temporary directory or a
// checkout, so its path would stop working at once.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// rakkoExeName is the installed executable's name.
const rakkoExeName = "Rakko.exe"

// uninstallKey is Rakko's own uninstall entry (identifier from mygo.json),
// which records the directory the user installed into.
const uninstallKey = `Software\Microsoft\Windows\CurrentVersion\Uninstall\local.y3.rakko`

// installDir returns the directory Rakko is installed in: the one the
// uninstall entry records when it is present, otherwise NSIS's default.
// It is a variable so tests can pin a temporary install directory.
var installDir = func() (string, bool) {
	if key, err := registry.OpenKey(registry.CURRENT_USER, uninstallKey, registry.QUERY_VALUE); err == nil {
		value, _, verr := key.GetStringValue("InstallLocation")
		key.Close()
		if verr == nil && strings.TrimSpace(value) != "" {
			return filepath.Clean(strings.TrimSpace(value)), true
		}
	}
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "Programs", "Rakko"), true
	}
	return "", false
}

// StableExePath reports whether exe is the installed Rakko executable and
// returns the canonical path to write into a tool's configuration. The
// updater's in-place name for a running executable resolves to the installed
// name beside it, so a configuration written while an update is pending
// keeps working after the next start.
func StableExePath(exe string) (string, bool) {
	if exe == "" {
		return "", false
	}
	path := filepath.Clean(exe)
	path = strings.TrimPrefix(path, `\\?\`)
	if !rakkoExeNameMatches(filepath.Base(path)) {
		return "", false
	}
	dir, ok := installDir()
	if !ok || !strings.EqualFold(filepath.Clean(filepath.Dir(path)), dir) {
		return "", false
	}
	return filepath.Join(dir, rakkoExeName), true
}

// rakkoExeNameMatches reports whether base is Rakko.exe or the updater's
// in-place name for a running executable, .Rakko.exe.old-<pid>.
func rakkoExeNameMatches(base string) bool {
	if base == rakkoExeName {
		return true
	}
	rest, ok := strings.CutPrefix(base, "."+rakkoExeName+".old-")
	if !ok || rest == "" {
		return false
	}
	_, err := strconv.ParseUint(rest, 10, 64)
	return err == nil
}
