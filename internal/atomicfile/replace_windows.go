//go:build windows

package atomicfile

import (
	"errors"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// replaceRetry is how long a rename that fails because the target is open
// keeps being tried before the save is reported as failed.
const replaceRetry = time.Second

// replace renames src over dst, retrying while Windows reports the target
// as in use. MoveFileEx replaces the target, but only when every handle on
// it allows delete: an editor saving, a scanner previewing or a sync client
// reading the file makes that false for a moment.
func replace(src, dst string) error {
	deadline := time.Now().Add(replaceRetry)
	delay := 5 * time.Millisecond
	for {
		err := os.Rename(src, dst)
		if err == nil || !retryable(err) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(delay)
		if delay < 100*time.Millisecond {
			delay *= 2
		}
	}
}

// retryable reports whether err is the target being open for a moment,
// rather than a permission or path error that would only repeat.
func retryable(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.ERROR_ACCESS_DENIED, // target without FILE_SHARE_DELETE
		windows.ERROR_SHARING_VIOLATION,
		windows.ERROR_LOCK_VIOLATION:
		return true
	}
	return false
}
