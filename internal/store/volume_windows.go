//go:build windows

package store

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// volumeMissing reports whether path lives on a volume that is not
// available right now: the letter of an unplugged USB drive, an empty
// card reader, or a network share that no longer answers. Such an item
// survives Open flagged Missing and recovers through Refresh once the
// volume is back; a path on a mounted volume that plain does not exist
// reads as deleted, as before. Only "the volume itself is gone or
// unreachable" counts — an access problem on a mounted volume means the
// drive is there, and the item falls through to the usual rules.
func volumeMissing(path string) bool {
	vol := filepath.VolumeName(path) // "C:" or \\server\share
	if vol == "" {
		return false
	}
	root := vol + `\`
	if filepath.IsAbs(vol) { // a UNC volume name already ends at the share
		root = vol
	}
	_, err := os.Stat(root)
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.ERROR_FILE_NOT_FOUND, // 盘符不存在
		syscall.ERROR_PATH_NOT_FOUND, // 路径不存在（未映射的网络盘）
		windows.ERROR_NOT_READY,      // 卡读卡器空槽等无介质驱动器
		windows.ERROR_BAD_NETPATH,    // 网络主机不可达
		windows.ERROR_BAD_NET_NAME:   // 共享名不存在
		return true
	}
	return false
}
