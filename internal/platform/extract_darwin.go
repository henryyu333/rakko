//go:build darwin

package platform

import "syscall"

// ExtractWithSystemTool runs the system's archive extractor (bsdtar) with
// its streams captured separately: a listing goes to stdout and is
// returned, while an error comes from stderr only, so a failed listing
// never turns its file names into the message. canceled is polled while
// the tool runs; when it reports true the process is killed. Stdin stays
// closed so a passphrase request can never block.
func ExtractWithSystemTool(args []string, canceled func() bool) ([]byte, error) {
	return extractWithTool("/usr/bin/bsdtar", args, canceled)
}

// FreeSpace reports the free bytes on the volume holding dir.
func FreeSpace(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
