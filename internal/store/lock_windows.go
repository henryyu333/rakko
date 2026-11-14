//go:build windows

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// lockRoot takes the exclusive lock that gives one process ownership of a
// data directory. LockFileEx is tied to the open handle, so the system
// releases it when the handle closes — including abnormal process death —
// and a leftover .lock file never blocks later runs. Two Stores in the same
// process conflict too, which is the intended contract: one live handle per
// directory.
func lockRoot(root string) (*os.File, error) {
	lockPath := filepath.Join(root, lockFileName)
	// FILE_FLAG_OPEN_REPARSE_POINT opens the link itself instead of what it
	// points at, the Windows equivalent of O_NOFOLLOW: a .lock that is a
	// symlink or a junction is refused below, so the open can never reach —
	// and the truncate never rewrite — the file it points at.
	name, err := windows.UTF16PtrFromString(lockPath)
	if err != nil {
		return nil, fmt.Errorf("store: opening lock file: %w", err)
	}
	h, err := windows.CreateFile(name,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return nil, fmt.Errorf("store: opening lock file: %w", err)
	}
	f := os.NewFile(uintptr(h), lockPath)
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		f.Close()
		return nil, fmt.Errorf("store: opening lock file: %w", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		f.Close()
		return nil, fmt.Errorf("store: lock file %s is a symlink; refusing to follow it", lockPath)
	}
	// Anything but a regular file (a device, a pipe) has no meaningful
	// truncate and is not ours to lock.
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		f.Close()
		return nil, fmt.Errorf("store: lock file %s is not a regular file", lockPath)
	}
	// The whole-file lock, taken without waiting: a second instance must
	// hear "in use" now, not block until the first one exits.
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, ^uint32(0), ^uint32(0), ol); err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
			return nil, fmt.Errorf("%w%s: %s", ErrLocked, lockHolder(lockPath), root)
		}
		return nil, fmt.Errorf("store: locking %s: %w", root, err)
	}
	// Best-effort diagnostics: record the holder pid for the conflict
	// message above. The lock, not this text, is what enforces exclusion.
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(fmt.Sprintf("%d\n", os.Getpid())), 0)
	return f, nil
}

// lockHolder is the " (pid N)" note for a lock conflict, or "" when the
// holder recorded no pid.
func lockHolder(lockPath string) string {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return ""
	}
	pid := strings.TrimSpace(string(data))
	if pid == "" {
		return ""
	}
	return " (pid " + pid + ")"
}
