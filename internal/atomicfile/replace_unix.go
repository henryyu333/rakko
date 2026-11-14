//go:build !windows

package atomicfile

import "os"

// replace renames src over dst. On Unix the rename replaces an open or
// read-only target; the directory's permissions decide, as always.
func replace(src, dst string) error {
	return os.Rename(src, dst)
}
