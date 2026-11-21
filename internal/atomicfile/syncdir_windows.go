//go:build windows

package atomicfile

// SyncDir is a no-op on Windows: a directory cannot be opened as a file to
// be flushed, and NTFS journals its own directory updates, so there is
// nothing to do here. The call exists so shared code can ask.
func SyncDir(dir string) error {
	return nil
}
