package aiusage

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// privateSignIn writes Rakko's own credentials file into dir — the fallback
// Claude Code reads when the Keychain item is absent.
func privateSignIn(t *testing.T, dir, plan string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "quota", "claude_credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cred map[string]any
	if err := json.Unmarshal(data, &cred); err != nil {
		t.Fatal(err)
	}
	cred["claudeAiOauth"].(map[string]any)["subscriptionType"] = plan
	blob, err := json.Marshal(cred)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
}

// sourceKeychain answers per config directory: the Keychain service a
// private directory uses is missing (its credentials come from the file),
// while the Mac's own service has a sign-in.
func sourceKeychain(t *testing.T, privateDir string, privateFound bool, asked *map[string]int) func(context.Context, string, string) ([]byte, error) {
	t.Helper()
	if asked != nil {
		*asked = map[string]int{}
	}
	home := filepath.Join("testdata", "quota", "claude_credentials.json")
	return func(ctx context.Context, service, account string) ([]byte, error) {
		if asked != nil {
			(*asked)[service]++
		}
		if service == claudeKeychainServiceFor(privateDir) {
			if privateFound {
				return os.ReadFile(home)
			}
			return nil, exitError(t, 44)
		}
		if service == claudeHomeService {
			return os.ReadFile(home)
		}
		return nil, exitError(t, 44)
	}
}

// Rakko's own sign-in wins over the Mac's Claude Code, and its auth status
// and /usage run against the private config directory.
func TestFetchClaudePrefersRakkoSignIn(t *testing.T) {
	priv := filepath.Join(t.TempDir(), "claude")
	privateSignIn(t, priv, "max")
	cli := &fakeClaudeCLI{
		usageOut: claudeResultJSON(claudeSampleUsage, false),
		authOut:  `{"loggedIn":true,"subscriptionType":"max"}`,
	}
	env := claudeEnv(t, t.TempDir())
	env.RakkoClaudeDir = priv
	env.RakkoClaudeAccount = &ClaudeAccount{}
	var asked map[string]int
	env.Keychain = sourceKeychain(t, priv, false, &asked)
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
	if q.ClaudeSource != ClaudeSourceRakko {
		t.Fatalf("source = %v, want Rakko's", q.ClaudeSource)
	}
	if cli.lastDir != priv {
		t.Fatalf("config dir = %q, want %q", cli.lastDir, priv)
	}
	if q.Plan != "Max" {
		t.Fatalf("plan = %q, want the private account's Max", q.Plan)
	}
	if asked[claudeKeychainServiceFor(priv)] == 0 {
		t.Fatalf("keychain services asked = %v, want the private directory's", asked)
	}
}

// Without a sign-in in Rakko's directory the reading falls back to the
// Mac's own Claude Code, exactly as before.
func TestFetchClaudeFallsBackToUserSignIn(t *testing.T) {
	priv := filepath.Join(t.TempDir(), "claude")
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.RakkoClaudeDir = priv
	env.RakkoClaudeAccount = &ClaudeAccount{}
	env.Keychain = sourceKeychain(t, priv, false, nil)
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
	if q.ClaudeSource != ClaudeSourceUser {
		t.Fatalf("source = %v, want the Mac's", q.ClaudeSource)
	}
	if cli.lastDir != "" {
		t.Fatalf("config dir = %q, want the Mac's default", cli.lastDir)
	}
	if q.Plan != "Pro" {
		t.Fatalf("plan = %q, want Pro from the Mac's credentials", q.Plan)
	}
}

// Neither directory signed in: the card says so and no process runs.
func TestFetchClaudeNeitherSourceSignedIn(t *testing.T) {
	priv := filepath.Join(t.TempDir(), "claude")
	cli := &fakeClaudeCLI{}
	env := claudeEnv(t, t.TempDir())
	env.RakkoClaudeDir = priv
	env.RakkoClaudeAccount = &ClaudeAccount{}
	env.Keychain = func(context.Context, string, string) ([]byte, error) {
		return nil, exitError(t, 44)
	}
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaNotLoggedIn {
		t.Fatalf("status = %v, want QuotaNotLoggedIn", q.Status)
	}
	if cli.authCalls != 0 || cli.usageCalls != 0 {
		t.Fatalf("ran claude with no sign-in: auth=%d usage=%d", cli.authCalls, cli.usageCalls)
	}
}

// The two sources keep separate 10s caches: a private miss cannot answer
// for the Mac's sign-in.
func TestFetchClaudeSourceCachesSeparate(t *testing.T) {
	priv := filepath.Join(t.TempDir(), "claude")
	var asked map[string]int
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.RakkoClaudeDir = priv
	env.RakkoClaudeAccount = &ClaudeAccount{}
	env.Keychain = sourceKeychain(t, priv, false, &asked)
	env.RunClaude = cli.run

	for range 2 {
		if q := FetchClaude(context.Background(), env); q.ClaudeSource != ClaudeSourceUser {
			t.Fatalf("source = %v, want the Mac's", q.ClaudeSource)
		}
	}
	if asked[claudeKeychainServiceFor(priv)] != 1 {
		t.Fatalf("private keychain reads = %d within the 10s cache, want 1",
			asked[claudeKeychainServiceFor(priv)])
	}
	if asked[claudeHomeService] != 1 {
		t.Fatalf("home keychain reads = %d within the 10s cache, want 1", asked[claudeHomeService])
	}
}
