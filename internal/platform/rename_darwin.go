//go:build darwin

package platform

import (
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
		return errno
	}
	return nil
}
