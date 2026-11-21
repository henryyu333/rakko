//go:build !windows

package atomicfile

import "os"

// SyncDir flushes dir's own entry list to the storage device, so a rename
// or a new name in it survives a crash or a power loss. Best-effort in the
// callers that only need the durability; the error is returned for callers
// that must know.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = syncData(d)
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
