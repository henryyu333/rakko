package aiusage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	if q := FetchClaude(context.Background(), env); q.Status != QuotaOK || cli.lastDir != "" {
		t.Fatalf("before login status=%v dir=%q, want the Mac's reading", q.Status, cli.lastDir)
	}

	// The sign-in succeeds and the same keychain now serves the private
	// item; the cached miss must not hide it.
	env.Keychain = sourceKeychain(t, priv, true, &asked)
	env.RunClaudeLogin = func(context.Context) error { return nil }
	if err := ClaudeAuthLogin(context.Background(), env); err != nil {
		t.Fatalf("login: %v", err)
	}

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("after login status = %v, err %q", q.Status, q.Err)
	}
	if cli.lastDir != priv {
		t.Fatalf("after login config dir = %q, want %q (cache not invalidated)", cli.lastDir, priv)
	}
}

// The real sign-in runs Claude Code under a pseudo-terminal and takes the
// process's exit as its result. A fake claude binary exercises the launch
// without touching any account.
func TestRunClaudeLoginThroughPseudoTTY(t *testing.T) {
	if _, err := os.Stat("/usr/bin/script"); err != nil {
		t.Skip("no /usr/bin/script on this machine")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude")
	result := filepath.Join(dir, "config-dir.txt")
	body := `#!/bin/sh
printf '%s' "$CLAUDE_CONFIG_DIR" > "$RESULT_FILE"
exit 0
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
	done := make(chan error, 1)
	go func() { done <- ClaudeAuthLogin(context.Background(), env) }()
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

// A non-zero exit is a failed sign-in, not a success.
func TestRunClaudeLoginProcessFailure(t *testing.T) {
	if _, err := os.Stat("/usr/bin/script"); err != nil {
		t.Skip("no /usr/bin/script on this machine")
	}
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "claude")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := Env{
		Home:               dir,
		ClaudePath:         scriptPath,
		RakkoClaudeDir:     filepath.Join(dir, "private"),
		RakkoClaudeAccount: &ClaudeAccount{},
	}
	if err := ClaudeAuthLogin(context.Background(), env); err == nil {
		t.Fatal("a failed process must not report success")
	}
}

// Cancelling the sign-in reaches the runner and comes back as a cancelled
// context, the one outcome that is not a failure.
func TestClaudeAuthLoginCancellation(t *testing.T) {
	env := Env{
		RakkoClaudeDir:     t.TempDir(),
		RakkoClaudeAccount: &ClaudeAccount{},
		RunClaudeLogin: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ClaudeAuthLogin(ctx, env) }()
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
	if err := ClaudeAuthLogin(context.Background(), env); !errors.Is(err, errNoRakkoClaudeDir) {
		t.Fatalf("err = %v, want errNoRakkoClaudeDir", err)
	}
}
