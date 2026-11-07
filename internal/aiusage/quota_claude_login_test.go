package aiusage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A successful sign-in drops the private source's cached "signed out" so
// the very next reading uses the new credentials.
func TestClaudeAuthLoginInvalidatesPrivateCache(t *testing.T) {
	priv := filepath.Join(t.TempDir(), "claude")
	var asked map[string]int
	env := claudeEnv(t, t.TempDir())
	env.RakkoClaudeDir = priv
	env.RakkoClaudeAccount = &ClaudeAccount{}
	// The private directory is signed out at first: the Mac's answer is
	// cached under the user source and the private miss under its own.
	env.Keychain = sourceKeychain(t, priv, false, &asked)
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env.RunClaude = cli.run
	env.Now = func() time.Time { return fakeNow }
	if q := FetchClaude(context.Background(), env); q.ClaudeSource != ClaudeSourceUser {
		t.Fatalf("before login source = %v, want the Mac's", q.ClaudeSource)
	}

	// The sign-in succeeds and the same keychain now serves the private
	// item; the cached miss must not hide it.
	env.Keychain = sourceKeychain(t, priv, true, &asked)
	urls := make(chan string, 1)
	env.RunClaudeLogin = func(ctx context.Context, onURL func(string), code <-chan string) error {
		onURL("https://claude.com/cai/oauth/authorize?code=true&state=test")
		select {
		case <-code:
		case <-ctx.Done():
			return ctx.Err()
		}
		return nil
	}
	codes := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- ClaudeAuthLogin(context.Background(), env, func(u string) { urls <- u }, codes)
	}()
	var gotURL string
	select {
	case gotURL = <-urls:
	case <-time.After(5 * time.Second):
		t.Fatal("the sign-in never printed its link")
	}
	codes <- "the-code"
	if err := <-done; err != nil {
		t.Fatalf("login: %v", err)
	}
	if gotURL == "" || !strings.Contains(gotURL, "oauth/authorize") {
		t.Fatalf("url = %q", gotURL)
	}

	q := FetchClaude(context.Background(), env)
	if q.ClaudeSource != ClaudeSourceRakko {
		t.Fatalf("after login source = %v, want Rakko's (cache not invalidated)", q.ClaudeSource)
	}
	if q.Status != QuotaOK {
		t.Fatalf("after login status = %v, err %q", q.Status, q.Err)
	}
}

// Logout runs against Rakko's directory only, and its failure propagates.
func TestClaudeAuthLogoutTouchesOnlyPrivateDir(t *testing.T) {
	priv := filepath.Join(t.TempDir(), "claude")
	cli := &fakeClaudeCLI{authOut: `{"loggedIn":true}`}
	env := claudeEnv(t, t.TempDir())
	env.RakkoClaudeDir = priv
	env.RakkoClaudeAccount = &ClaudeAccount{}
	env.RunClaude = cli.run

	if err := ClaudeAuthLogout(context.Background(), env); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if got, want := strings.Join(cli.lastArgs, " "), "auth logout"; got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	if cli.lastDir != priv {
		t.Fatalf("config dir = %q, want %q", cli.lastDir, priv)
	}

	env.RunClaude = func(ctx context.Context, args []string, dir string) (string, string, error) {
		cli.lastArgs, cli.lastDir = args, dir
		return "", "", os.ErrPermission
	}
	if err := ClaudeAuthLogout(context.Background(), env); err == nil {
		t.Fatal("a failed logout must not report success")
	}
}

// The real sign-in runs under a pseudo-terminal: Claude Code prints the
// authorization link, then reads the pasted code from its prompt. A fake
// claude binary exercises that handshake without touching any account.
func TestRunClaudeLoginThroughPseudoTTY(t *testing.T) {
	if _, err := os.Stat("/usr/bin/script"); err != nil {
		t.Skip("no /usr/bin/script on this machine")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude")
	result := filepath.Join(dir, "config-dir.txt")
	body := `#!/bin/sh
echo "Opening browser to sign in…"
echo "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=test"
printf 'Paste code here if prompted > '
read code
printf '%s' "$CLAUDE_CONFIG_DIR" > "$RESULT_FILE"
[ "$code" = "TEST-CODE" ] && exit 0
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RESULT_FILE", result)
	priv := filepath.Join(dir, "private")
	env := Env{
		Home:               dir,
		ClaudePath:         scriptPath,
		RakkoClaudeDir:     priv,
		RakkoClaudeAccount: &ClaudeAccount{},
	}

	urls := make(chan string, 1)
	codes := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- ClaudeAuthLogin(context.Background(), env, func(u string) { urls <- u }, codes)
	}()

	var url string
	select {
	case url = <-urls:
	case <-time.After(10 * time.Second):
		t.Fatal("the fake sign-in never printed its link")
	}
	if !strings.Contains(url, "oauth/authorize") {
		t.Fatalf("url = %q", url)
	}
	codes <- "TEST-CODE"
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("login: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the fake sign-in never finished")
	}

	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatalf("the fake sign-in did not run: %v", err)
	}
	if string(got) != priv {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", got, priv)
	}
}

// A rejected code is a failed sign-in, not a success.
func TestRunClaudeLoginRejectsBadCode(t *testing.T) {
	if _, err := os.Stat("/usr/bin/script"); err != nil {
		t.Skip("no /usr/bin/script on this machine")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude")
	body := `#!/bin/sh
echo "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=test"
printf 'Paste code here if prompted > '
read code
exit 1
`
	if err := os.WriteFile(scriptPath, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	env := Env{
		Home:               dir,
		ClaudePath:         scriptPath,
		RakkoClaudeDir:     filepath.Join(dir, "private"),
		RakkoClaudeAccount: &ClaudeAccount{},
	}
	urls := make(chan string, 1)
	codes := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- ClaudeAuthLogin(context.Background(), env, func(u string) { urls <- u }, codes)
	}()
	select {
	case <-urls:
	case <-time.After(10 * time.Second):
		t.Fatal("the fake sign-in never printed its link")
	}
	codes <- "WRONG"
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a rejected code must not report success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the fake sign-in never finished")
	}
}

// Cancelling the sign-in reaches the runner and comes back as a cancelled
// context, the one outcome the card does not report as a failure.
func TestClaudeAuthLoginCancellation(t *testing.T) {
	env := Env{
		RakkoClaudeDir:     t.TempDir(),
		RakkoClaudeAccount: &ClaudeAccount{},
		RunClaudeLogin: func(ctx context.Context, onURL func(string), code <-chan string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ClaudeAuthLogin(ctx, env, nil, nil) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling did not end the sign-in")
	}
}

// Without a private directory the app refuses to start a sign-in rather
// than writing anywhere else.
func TestClaudeAuthLoginNeedsRakkoDir(t *testing.T) {
	env := Env{RakkoClaudeAccount: &ClaudeAccount{}}
	if err := ClaudeAuthLogin(context.Background(), env, nil, nil); !errors.Is(err, errNoRakkoClaudeDir) {
		t.Fatalf("err = %v, want errNoRakkoClaudeDir", err)
	}
	if err := ClaudeAuthLogout(context.Background(), env); !errors.Is(err, errNoRakkoClaudeDir) {
		t.Fatalf("logout err = %v, want errNoRakkoClaudeDir", err)
	}
}

// claudeAuthorizeURL reads the link out of the terminal line Claude Code
// prints, including the ANSI hyperlink wrapper a pty adds.
func TestClaudeAuthorizeURL(t *testing.T) {
	line := "If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=y"
	if got := claudeAuthorizeURL(line); got != "https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=y" {
		t.Fatalf("url = %q", got)
	}
	wrapped := "\x1b]8;;https://claude.com/cai/oauth/authorize?code=true&state=z\x07https://claude.com/cai/oauth/authorize?code=true&state=z\x1b]8;;\x07"
	if got := claudeAuthorizeURL(wrapped); !strings.Contains(got, "state=z") {
		t.Fatalf("wrapped url = %q", got)
	}
	if got := claudeAuthorizeURL("Paste code here if prompted > "); got != "" {
		t.Fatalf("prompt matched %q", got)
	}
}
