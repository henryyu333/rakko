//go:build darwin

package atomicfile

import (
	"os"
	"syscall"
)

// f_FULLFSYNC is what fcntl needs for a flush that reaches the drive:
// on APFS a plain fsync only promises the page cache, so a power loss
// could leave a renamed file whose data was never written.
const f_FULLFSYNC = 0x33 // syscall.F_FULLFSYNC

// syncData flushes f's bytes to the storage device. Some filesystems
// (network mounts, FAT on USB sticks) reject F_FULLFSYNC; falling back
// to fsync keeps the save working with today's durability rather than
// failing over a stronger guarantee the medium will not give.
func syncData(f *os.File) error {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, f.Fd(), f_FULLFSYNC, 0)
	if errno != 0 {
		return f.Sync()
	}
	return nil
}
