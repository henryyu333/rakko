package aiusage

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ClaudeLoginTimeout bounds one sign-in from its start to its end, so a
// browser the user never finishes cannot keep a Claude Code process alive.
const ClaudeLoginTimeout = 10 * time.Minute

// errNoRakkoClaudeDir says the app was started without its own Claude Code
// directory, so there is no sign-in to manage.
var errNoRakkoClaudeDir = errors.New("Rakko's Claude Code directory is not configured")

// ClaudeAuthLogin signs Rakko's own Claude Code in. Claude Code opens the
// browser and completes the flow through its own local callback; this app
// only waits for the process. The run is bounded by ClaudeLoginTimeout, and
// every process it starts dies when ctx is cancelled. On success the private
// source's cached "signed out" answer is dropped so the next reading sees
// the new credentials.
func ClaudeAuthLogin(ctx context.Context, env Env) error {
	if env.RakkoClaudeDir == "" {
		return errNoRakkoClaudeDir
	}
	ctx, cancel := context.WithTimeout(ctx, ClaudeLoginTimeout)
	defer cancel()
	run := env.RunClaudeLogin
	if run == nil {
		run = func(ctx context.Context) error {
			return runClaudeLogin(ctx, env)
		}
	}
	if err := run(ctx); err != nil {
		return err
	}
	env.RakkoClaudeAccount.Invalidate()
	return nil
}

// runClaudeLogin runs the real `claude auth login` under a pseudo-terminal
// (/usr/bin/script): without one Claude Code offers a paste prompt instead
// of the terminal sign-in it drives. The sign-in itself is the browser's
// local callback; the process's exit is the only result Rakko reads, and
// nothing it prints is kept or logged.
func runClaudeLogin(ctx context.Context, env Env) error {
	path, err := claudeBinary(env)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "rakko-claude-login-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.MkdirAll(env.RakkoClaudeDir, 0o700); err != nil {
		return err
	}

	cmd := exec.Command("/usr/bin/script", "-q", "/dev/null", path, "auth", "login")
	cmd.Dir = dir
	childEnv := cleanClaudeEnv(os.Environ())
	childEnv = setEnv(childEnv, "CLAUDE_CONFIG_DIR", env.RakkoClaudeDir)
	childEnv = setEnv(childEnv, "TERM", "xterm-256color")
	cmd.Env = childEnv
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		killClaudeGroup(cmd.Process.Pid)
		<-done
		return ctx.Err()
	}
}
