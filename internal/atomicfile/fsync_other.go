//go:build !darwin

package atomicfile

import "os"

// syncData flushes f's bytes to the storage device.
func syncData(f *os.File) error {
	return f.Sync()
}
