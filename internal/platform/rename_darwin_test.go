//go:build darwin

package platform

import (
	"syscall"
	"testing"
)

func TestRenameErrorMapping(t *testing.T) {
	for errno, want := range map[syscall.Errno]error{
		syscall.EEXIST:       ErrExists,
		syscall.ENOENT:       ErrNotExist,
		syscall.EACCES:       ErrPermission,
		syscall.EPERM:        ErrPermission,
		syscall.ENAMETOOLONG: ErrNameTooLong,
	} {
		if got := renameError(errno); got != want {
			t.Errorf("renameError(%v) = %v, want %v", errno, got, want)
		}
	}
	if got := renameError(syscall.EIO); got != syscall.EIO {
		t.Errorf("unmapped errno changed: %v", got)
	}
}
