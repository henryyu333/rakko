//go:build !windows

package aiusage

import (
	"context"
	"io"
	"os/exec"
)

// runClaudeLoginTTY starts the sign-in under /usr/bin/script, the pseudo-
// terminal macOS ships: without one Claude Code offers a paste prompt
// instead of the terminal sign-in it drives.
func runClaudeLoginTTY(ctx context.Context, argv []string, dir string, childEnv []string) error {
	cmd := exec.Command("/usr/bin/script", append([]string{"-q", "/dev/null"}, argv...)...)
	cmd.Dir = dir
	cmd.Env = childEnv
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	group, err := startInGroup(cmd)
	if err != nil {
		return err
	}
	defer group.release()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		group.kill()
		<-done
		return ctx.Err()
	}
}
