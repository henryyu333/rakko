//go:build darwin

package platform

import (
	"errors"
	"syscall"
	"unsafe"
)

// RenameNoReplace moves src to dst without ever overwriting an existing
// entry: the rename fails with EEXIST instead, which callers map to a name
// conflict.
func RenameNoReplace(src, dst string) error {
	const renameatxNp = 488 // renameatx_np, a RENAME_EXCL move
	const renameExcl = 0x4
	atFdcwd := ^uintptr(1) // AT_FDCWD = -2 as uintptr
	srcB, err := syscall.BytePtrFromString(src)
	if err != nil {
		return err
	}
	dstB, err := syscall.BytePtrFromString(dst)
	if err != nil {
		return err
	}
	_, _, errno := syscall.Syscall6(renameatxNp, atFdcwd,
		uintptr(unsafe.Pointer(srcB)), atFdcwd, uintptr(unsafe.Pointer(dstB)), renameExcl, 0)
	if errno != 0 {
		return renameError(errno)
	}
	return nil
}

// renameError translates the rename's errno into the shared errors.
func renameError(errno syscall.Errno) error {
	switch errno {
	case syscall.EEXIST:
		return ErrExists
	case syscall.ENOENT:
		return ErrNotExist
	case syscall.EACCES, syscall.EPERM:
		return ErrPermission
	case syscall.ENAMETOOLONG:
		return ErrNameTooLong
	}
	return errno
}

// IsNotDirectory reports whether err says a path component is not a
// directory, the failure a Downloads folder replaced by a file gives.
func IsNotDirectory(err error) bool { return errors.Is(err, syscall.ENOTDIR) }
