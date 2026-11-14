//go:build windows

package platform

// Archive extraction through the system's tar.exe, which is libarchive's
// bsdtar: it reads zip, the tar family, 7z and RAR, the same set macOS'
// bsdtar reads (the shared extractor keeps .zip on archive/zip for the
// exact size and encoding checks, and hands the rest here).

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// systemTar is the extractor Windows ships. Resolving it under the system
// directory rather than through PATH keeps a tool the user put on PATH from
// standing in for it.
func systemTar() string {
	dir := os.Getenv("SystemRoot")
	if dir == "" {
		dir = `C:\Windows`
	}
	return filepath.Join(dir, "System32", "tar.exe")
}

// ExtractWithSystemTool runs the system's extractor with its streams
// captured separately: a listing goes to stdout and is returned, while an
// error comes from stderr only, so a failed listing never turns its file
// names into the message. canceled is polled while the tool runs; when it
// reports true the process is killed. Stdin stays closed so a passphrase
// request can never block.
func ExtractWithSystemTool(args []string, canceled func() bool) ([]byte, error) {
	tar := systemTar()
	if _, err := os.Stat(tar); err != nil {
		return nil, errors.New("Windows 的 tar.exe 不可用: " + err.Error())
	}
	cmd := exec.Command(tar, args...)
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
