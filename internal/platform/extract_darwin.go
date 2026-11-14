//go:build darwin

package platform

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ExtractWithSystemTool runs the system's archive extractor (bsdtar) with
// its streams captured separately: a listing goes to stdout and is
// returned, while an error comes from stderr only, so a failed listing
// never turns its file names into the message. canceled is polled while
// the tool runs; when it reports true the process is killed. Stdin stays
// closed so a passphrase request can never block.
func ExtractWithSystemTool(args []string, canceled func() bool) ([]byte, error) {
	cmd := exec.Command("/usr/bin/bsdtar", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	done := make(chan struct{})
	if canceled != nil {
		go func() {
			tick := time.NewTicker(50 * time.Millisecond)
			defer tick.Stop()
			for {
				select {
				case <-done:
					return
				case <-tick.C:
					if canceled() {
						_ = cmd.Process.Kill()
					}
				}
			}
		}()
	}
	err := cmd.Wait()
	close(done)
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.Bytes(), errors.New(msg)
	}
	return stdout.Bytes(), nil
}

// FreeSpace reports the free bytes on the volume holding dir.
func FreeSpace(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
