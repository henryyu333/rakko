package aiusage

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// ClaudeLoginTimeout bounds one sign-in from its start to its end, so a
// browser the user never finishes cannot keep a Claude Code process alive.
const ClaudeLoginTimeout = 10 * time.Minute

// claudeLogoutTimeout bounds the short `claude auth logout` run.
const claudeLogoutTimeout = 30 * time.Second

// errNoRakkoClaudeDir says the app was started without its own Claude Code
// directory, so there is no sign-in to manage.
var errNoRakkoClaudeDir = errors.New("Rakko's Claude Code directory is not configured")

// ClaudeAuthLogin signs Rakko's own Claude Code in: Claude Code opens the
// browser, and the user pastes the authorization code it asks for into
// code. The whole run is bounded by ClaudeLoginTimeout and every process
// it starts dies when ctx is cancelled. On success the private source's
// cached "signed out" answer is dropped so the next reading sees it.
func ClaudeAuthLogin(ctx context.Context, env Env, onURL func(string), code <-chan string) error {
	if env.RakkoClaudeDir == "" {
		return errNoRakkoClaudeDir
	}
	if onURL == nil {
		onURL = func(string) {}
	}
	ctx, cancel := context.WithTimeout(ctx, ClaudeLoginTimeout)
	defer cancel()
	run := env.RunClaudeLogin
	if run == nil {
		run = func(ctx context.Context, onURL func(string), code <-chan string) error {
			return runClaudeLogin(ctx, env, onURL, code)
		}
	}
	if err := run(ctx, onURL, code); err != nil {
		return err
	}
	env.RakkoClaudeAccount.Invalidate()
	return nil
}

// ClaudeAuthLogout signs Rakko's own Claude Code out. Only the private
// config directory is touched: the Mac's own ~/.claude sign-in stays as it
// is. On success the private source's cache is dropped.
func ClaudeAuthLogout(ctx context.Context, env Env) error {
	if env.RakkoClaudeDir == "" {
		return errNoRakkoClaudeDir
	}
	if err := os.MkdirAll(env.RakkoClaudeDir, 0o700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, claudeLogoutTimeout)
	defer cancel()
	if _, _, err := runClaudeCode(ctx, env, []string{"auth", "logout"}, env.RakkoClaudeDir); err != nil {
		return err
	}
	env.RakkoClaudeAccount.Invalidate()
	return nil
}

// claudeAuthorizeRE finds the manual authorization link Claude Code prints
// beside its paste prompt. The URL is one line; the ANSI hyperlink wrapper
// around it stops the match.
var claudeAuthorizeRE = regexp.MustCompile(`https://claude\.com/cai/oauth/authorize\?[^\s\x1b\x07]*`)

// claudeAuthorizeURL returns the authorization link in one line of Claude
// Code's output, or "".
func claudeAuthorizeURL(line string) string {
	return claudeAuthorizeRE.FindString(line)
}

// runClaudeLogin runs the real `claude auth login` under a pseudo-terminal
// (/usr/bin/script): without one Claude Code either refuses or waits on a
// prompt a background process can never answer. It keeps the process in its
// own session so cancellation kills whatever Claude Code forked, drains
// stdout to announce the link, and writes each pasted code to the prompt.
// Nothing printed is kept or logged.
func runClaudeLogin(ctx context.Context, env Env, onURL func(string), code <-chan string) error {
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
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		br := bufio.NewReaderSize(stdout, 64*1024)
		announced := false
		for {
			line, readErr := br.ReadString('\n')
			if !announced {
				if u := claudeAuthorizeURL(line); u != "" {
					announced = true
					onURL(u)
				}
			}
			if readErr != nil {
				return
			}
		}
	}()

	go func() {
		for {
			select {
			case c, ok := <-code:
				if !ok {
					return
				}
				c = strings.TrimSpace(c)
				if c == "" {
					continue
				}
				if _, err := io.WriteString(stdin, c+"\n"); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		killClaudeGroup(cmd.Process.Pid)
		<-done
		waitErr = ctx.Err()
	}
	stdin.Close()
	<-readDone
	return waitErr
}
