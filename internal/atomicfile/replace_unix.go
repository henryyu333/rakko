//go:build !windows

package atomicfile

import (
	"os"
	"path/filepath"
)

// replace renames src over dst. On Unix the rename replaces an open or
// read-only target; the directory's permissions decide, as always. The
// renamed directory entry is flushed afterwards — best-effort, since the
// rename already happened — so a power loss cannot leave the new name
// pointing at data the drive never saw.
func replace(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(dst)); err == nil {
		_ = syncData(dir)
		dir.Close()
	}
	return nil
}
