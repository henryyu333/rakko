//go:build !windows

package store

import (
	"os"
	"path/filepath"
	"strings"
)

// volumeMissing reports whether path lives on a volume that is not
// mounted right now. macOS mounts external and network volumes under
// /Volumes/<name>, so a path there whose volume directory no longer
// exists means the drive was unplugged, not that the file was deleted;
// such an item survives Open flagged Missing and recovers through
// Refresh once the drive is back. Anywhere else — a missing directory
// on the system disk reads as a deleted file, as before.
func volumeMissing(path string) bool {
	const mountRoot = "/Volumes/"
	if !strings.HasPrefix(path, mountRoot) {
		return false
	}
	name := strings.SplitN(strings.TrimPrefix(path, mountRoot), "/", 2)[0]
	if name == "" {
		return false
	}
	if _, err := os.Lstat(filepath.Join(mountRoot, name)); os.IsNotExist(err) {
		return true
	}
	// 其它错误（权限等）按“盘在”处理：宁可多保留一行，也不误判离线。
	return false
}
