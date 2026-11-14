//go:build !windows

package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// lockRoot takes the exclusive advisory lock that gives one process
// ownership of a data directory. flock is tied to the open file, so the
// kernel releases it when the descriptor closes — including abnormal
// process death — and a leftover .lock file never blocks later runs.
// Two Stores in the same process conflict too, which is the intended
// contract: one live handle per directory.
func lockRoot(root string) (*os.File, error) {
	lockPath := filepath.Join(root, lockFileName)
	// O_NOFOLLOW: a .lock that is a symlink is refused outright, so the
	// open can never reach — and the truncate below never rewrite — the
	// file it points at.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("store: lock file %s is a symlink; refusing to follow it", lockPath)
		}
		return nil, fmt.Errorf("store: opening lock file: %w", err)
	}
	// Anything but a regular file (a device, a FIFO) has no meaningful
	// truncate and is not ours to lock.
	if fi, serr := f.Stat(); serr != nil || !fi.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("store: lock file %s is not a regular file", lockPath)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
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
