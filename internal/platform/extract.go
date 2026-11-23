package platform

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"time"
)

// extractWithTool runs the system's archive tool (bsdtar / tar.exe) with
// separated stdout/stderr and 50ms cancel polling.
func extractWithTool(bin string, args []string, canceled func() bool) ([]byte, error) {
	cmd := exec.Command(bin, args...)
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
