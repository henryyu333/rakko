package aiusage

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeSampleUsage is a reading dated around fakeNow (2027-01-10 12:00
// UTC): a 5-hour session four hours out and a week three days out.
const claudeSampleUsage = `You are currently using your subscription to power your Claude Code usage

Current session: 7% used · resets Jan 10 at 4pm (UTC)
Current week (all models): 41% used · resets Jan 13 at 10am (UTC)`

// claudeResultJSON wraps /usage's text in what -p --output-format json
// prints.
func claudeResultJSON(result string, isError bool) string {
	return fmt.Sprintf(`{"type":"result","is_error":%t,"result":%q}`, isError, result)
}

// keychainFixture serves the named credentials fixture and records the
// service and account it was asked for.
func keychainFixture(t *testing.T, name string, calls *int, service, account *string) func(context.Context, string, string) ([]byte, error) {
	t.Helper()
	return func(ctx context.Context, svc, acc string) ([]byte, error) {
		if calls != nil {
			*calls++
		}
		if service != nil {
			*service = svc
		}
		if account != nil {
			*account = acc
		}
		return os.ReadFile(filepath.Join("testdata", "quota", name))
	}
}

func keychainMissing(context.Context, string, string) ([]byte, error) {
	return nil, errors.New("The specified item could not be found in the keychain.")
}

// fakeClaudeCLI stands in for Claude Code: it answers `auth status` and
// /usage from fields and records how it was called.
type fakeClaudeCLI struct {
	usageOut  string
	usageErr  string
	usageFail error
	authOut   string

	usageCalls int
	authCalls  int
	lastArgs   []string
	lastDir    string
}

func (f *fakeClaudeCLI) run(ctx context.Context, args []string, dir string) (string, string, error) {
	f.lastArgs, f.lastDir = args, dir
	if len(args) > 0 && args[0] == "auth" {
		f.authCalls++
		if f.authOut == "" {
			return "", "", errors.New("no auth answer")
		}
		return f.authOut, "", nil
	}
	f.usageCalls++
	return f.usageOut, f.usageErr, f.usageFail
}

// claudeEnv is the machine a Claude fetch runs on: the logged-in fixtures,
// a fake clock and a fresh account cache.
func claudeEnv(t *testing.T, home string) Env {
	t.Helper()
	return Env{
		Home:          home,
		Keychain:      keychainFixture(t, "claude_credentials.json", nil, nil, nil),
		Now:           func() time.Time { return fakeNow },
		ClaudeUser:    "tester",
		ClaudeAccount: &ClaudeAccount{},
	}
}

func TestFetchClaudeOK(t *testing.T) {
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
	if q.Tool != ToolClaude || q.Plan != "Pro" {
		t.Fatalf("quota = %+v", q)
	}
	if len(q.Windows) != 2 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if got, want := strings.Join(cli.lastArgs, " "), strings.Join(claudeUsageArgs(), " "); got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
	if cli.lastDir != "" {
		t.Fatalf("config dir = %q, want the default", cli.lastDir)
	}
	if q.Windows[0].UsedPct != 7 || q.Windows[1].UsedPct != 41 {
		t.Fatalf("windows = %+v", q.Windows)
	}
	if q.Windows[0].ResetsAt.IsZero() || q.Windows[1].ResetsAt.IsZero() {
		t.Fatalf("reset times missing: %+v", q.Windows)
	}
}

func TestFetchClaudeUsesConfigDir(t *testing.T) {
	dir := t.TempDir()
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	var gotService string
	env := claudeEnv(t, t.TempDir())
	env.ClaudeConfigDir = dir
	env.Keychain = keychainFixture(t, "claude_credentials.json", nil, &gotService, nil)
	env.RunClaude = cli.run

	if q := FetchClaude(context.Background(), env); q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
	if gotService != claudeKeychainServiceFor(dir) {
		t.Fatalf("keychain service = %q, want %q", gotService, claudeKeychainServiceFor(dir))
	}
	if gotService == claudeHomeService {
		t.Fatal("a config dir must not use the plain service")
	}
	if cli.lastDir != dir {
		t.Fatalf("CLAUDE_CONFIG_DIR = %q, want %q", cli.lastDir, dir)
	}
}

func TestFetchClaudePlanFromAuthStatus(t *testing.T) {
	cli := &fakeClaudeCLI{
		usageOut: claudeResultJSON(claudeSampleUsage, false),
		authOut:  `{"loggedIn":true,"subscriptionType":"max"}`,
	}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Plan != "Max" {
		t.Fatalf("plan = %q, want the auth status's Max", q.Plan)
	}
	if cli.authCalls != 1 || cli.usageCalls != 1 {
		t.Fatalf("calls auth=%d usage=%d", cli.authCalls, cli.usageCalls)
	}
}

func TestFetchClaudeAuthStatusSaysLoggedOut(t *testing.T) {
	cli := &fakeClaudeCLI{authOut: `{"loggedIn":false}`}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaNotLoggedIn {
		t.Fatalf("status = %v, want QuotaNotLoggedIn", q.Status)
	}
	if cli.usageCalls != 0 {
		t.Fatalf("usage ran %d times although no one is signed in", cli.usageCalls)
	}
}

func TestFetchClaudeNoCLI(t *testing.T) {
	cli := &fakeClaudeCLI{usageFail: errClaudeNotInstalled}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaNoCLI {
		t.Fatalf("status = %v, want QuotaNoCLI", q.Status)
	}
}

func TestFetchClaudeDeniedMessages(t *testing.T) {
	for _, tc := range []struct {
		text string
		want QuotaStatus
	}{
		{"Not logged in · Please run /login", QuotaNotLoggedIn},
		{"OAuth session expired and could not be refreshed", QuotaExpired},
		{"API Error: 401 Unauthorized", QuotaExpired},
		{"You've hit your limit for now", QuotaLimitReached},
		{"You've hit your session limit · resets 3pm", QuotaLimitReached},
	} {
		cli := &fakeClaudeCLI{usageOut: claudeResultJSON(tc.text, true)}
		env := claudeEnv(t, t.TempDir())
		env.RunClaude = cli.run
		q := FetchClaude(context.Background(), env)
		if q.Status != tc.want {
			t.Fatalf("%q → status %v, want %v", tc.text, q.Status, tc.want)
		}
	}
}

// A limit answer carries the reset its message named, so the card can say
// when the allowance returns.
func TestFetchClaudeLimitReset(t *testing.T) {
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON("You've hit your weekly limit · resets Jan 12 at 10am (UTC)", true)}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaLimitReached {
		t.Fatalf("status = %v, want QuotaLimitReached", q.Status)
	}
	want := time.Date(2027, 1, 12, 10, 0, 0, 0, time.UTC)
	if !q.LimitResetsAt.Equal(want) {
		t.Fatalf("LimitResetsAt = %v, want %v (err %q)", q.LimitResetsAt, want, q.Err)
	}
}

func TestFetchClaudeEmptyUsage(t *testing.T) {
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON("", false)}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaFailed {
		t.Fatalf("status = %v, want QuotaFailed", q.Status)
	}
	if q.Err == "" {
		t.Fatal("an empty answer must carry an error, never 0%")
	}
}

func TestFetchClaudeVerboseArray(t *testing.T) {
	out := `[{"type":"system"},{"type":"assistant","result":""},` +
		claudeResultJSON(claudeSampleUsage, false) + `]`
	cli := &fakeClaudeCLI{usageOut: out}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	if q := FetchClaude(context.Background(), env); q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
}

func TestFetchClaudeCLIErrorKeepsStderr(t *testing.T) {
	cli := &fakeClaudeCLI{usageFail: errors.New("exit status 1"), usageErr: "EACCES: permission denied"}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaFailed || !strings.Contains(q.Err, "EACCES") {
		t.Fatalf("quota = %+v, want QuotaFailed with the CLI's message", q)
	}
}

func TestFetchClaudeIgnoresLocalExpiry(t *testing.T) {
	// Claude Code refreshes its own token; a locally old expiresAt must
	// not read as 登录已过期.
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run
	env.Keychain = keychainFixture(t, "claude_credentials_expired.json", nil, nil, nil)

	if q := FetchClaude(context.Background(), env); q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
}

func TestFetchClaudeEmptyKeychainItemDoesNotFallBack(t *testing.T) {
	// An item that parses but holds no token is a sign-out; the older
	// file beside it must not sign the account back in.
	home := t.TempDir()
	writeClaudeFile(t, home, "claude_credentials_max.json")
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, home)
	env.RunClaude = cli.run
	env.Keychain = func(context.Context, string, string) ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"","refreshToken":""}}`), nil
	}

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaNotLoggedIn {
		t.Fatalf("status = %v, want QuotaNotLoggedIn", q.Status)
	}
	if cli.usageCalls != 0 {
		t.Fatal("a signed-out account must not run /usage")
	}
}

func TestFetchClaudeFileFallback(t *testing.T) {
	home := t.TempDir()
	writeClaudeFile(t, home, "claude_credentials_max.json")
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, home)
	env.Keychain = keychainMissing
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
	if q.Plan != "Max" {
		t.Fatalf("plan = %q", q.Plan)
	}
}

func TestFetchClaudeConfigDirFile(t *testing.T) {
	dir := t.TempDir()
	writeCredentialFile(t, dir, "claude_credentials_max.json")
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.ClaudeConfigDir = dir
	env.Keychain = keychainMissing
	env.RunClaude = cli.run

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaOK || q.Plan != "Max" {
		t.Fatalf("quota = %+v", q)
	}
}

func TestFetchClaudeHexKeychainItem(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "quota", "claude_credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.RunClaude = cli.run
	env.Keychain = func(context.Context, string, string) ([]byte, error) {
		return []byte(hex.EncodeToString(data) + "\n"), nil
	}

	if q := FetchClaude(context.Background(), env); q.Status != QuotaOK {
		t.Fatalf("status = %v, err %q", q.Status, q.Err)
	}
}

func TestFetchClaudeCredentialCache(t *testing.T) {
	now := fakeNow
	var calls int
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.Now = func() time.Time { return now }
	env.Keychain = keychainFixture(t, "claude_credentials.json", &calls, nil, nil)
	env.RunClaude = cli.run

	FetchClaude(context.Background(), env)
	FetchClaude(context.Background(), env)
	if calls != 1 {
		t.Fatalf("keychain reads = %d within the 10s cache, want 1", calls)
	}
	now = now.Add(claudeCredentialTTL + time.Second)
	FetchClaude(context.Background(), env)
	if calls != 2 {
		t.Fatalf("keychain reads = %d after the cache expired, want 2", calls)
	}
}

func TestFetchClaudeKeychainDialogBackoff(t *testing.T) {
	now := fakeNow
	var calls int
	denied := exitError(t, 1)
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.Now = func() time.Time { return now }
	env.Keychain = func(context.Context, string, string) ([]byte, error) {
		calls++
		return nil, denied
	}
	env.RunClaude = cli.run

	if q := FetchClaude(context.Background(), env); q.Status != QuotaNotLoggedIn {
		t.Fatalf("first status = %v", q.Status)
	}
	now = now.Add(time.Minute)
	FetchClaude(context.Background(), env)
	if calls != 1 {
		t.Fatalf("a denied Keychain read was asked again after %d calls", calls)
	}
	now = now.Add(claudeDeniedTTL)
	FetchClaude(context.Background(), env)
	if calls != 2 {
		t.Fatalf("keychain reads = %d after the backoff, want 2", calls)
	}
}

func TestFetchClaudeKeychainMissingDoesNotBackOff(t *testing.T) {
	now := fakeNow
	var calls int
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.Now = func() time.Time { return now }
	env.Keychain = func(context.Context, string, string) ([]byte, error) {
		calls++
		return nil, exitError(t, 44) // the item is not there: asks nobody
	}
	env.RunClaude = cli.run

	FetchClaude(context.Background(), env)
	now = now.Add(claudeCredentialTTL + time.Second)
	FetchClaude(context.Background(), env)
	if calls != 2 {
		t.Fatalf("keychain reads = %d, want 2: a missing item must not stop the next read", calls)
	}
}

func TestFetchClaudeCancelledContext(t *testing.T) {
	touched := false
	env := claudeEnv(t, t.TempDir())
	env.Keychain = func(context.Context, string, string) ([]byte, error) {
		touched = true
		return nil, errors.New("not read")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	q := FetchClaude(ctx, env)
	if q.Status != QuotaFailed {
		t.Fatalf("status = %v, want QuotaFailed", q.Status)
	}
	if touched {
		t.Fatal("credentials were read despite a cancelled context")
	}
}

func TestClaudeKeychainServiceFor(t *testing.T) {
	if got := claudeKeychainServiceFor(""); got != claudeHomeService {
		t.Fatalf("plain service = %q", got)
	}
	a := claudeKeychainServiceFor("/one")
	b := claudeKeychainServiceFor("/two")
	if a == b || !strings.HasPrefix(a, claudeHomeService+"-") {
		t.Fatalf("derived services = %q, %q", a, b)
	}
	if len(strings.TrimPrefix(a, claudeHomeService+"-")) != 8 {
		t.Fatalf("service suffix = %q", a)
	}
}

func TestCleanClaudeEnv(t *testing.T) {
	env := cleanClaudeEnv([]string{
		"PATH=/usr/bin",
		"ANTHROPIC_BASE_URL=https://evil.example",
		"ANTHROPIC_AUTH_TOKEN=x",
		"ANTHROPIC_API_KEY=y",
		"CLAUDE_CODE_OAUTH_TOKEN=z",
		"HOME=/Users/x",
		"DISABLE_TELEMETRY=0",
	})
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "DISABLE_TELEMETRY=0"} {
		if strings.Contains(joined, bad) {
			t.Fatalf("%s survived the clean environment:\n%s", bad, joined)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/Users/x", "DISABLE_TELEMETRY=1", "DISABLE_ERROR_REPORTING=1", "DISABLE_AUTOUPDATER=1"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("%s missing from the clean environment:\n%s", want, joined)
		}
	}
}

// writeClaudeFile drops a credentials file under home/.claude/.
func writeClaudeFile(t *testing.T, home, fixtureName string) {
	t.Helper()
	writeCredentialFile(t, filepath.Join(home, ".claude"), fixtureName)
}

func writeCredentialFile(t *testing.T, dir, fixtureName string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("testdata", "quota", fixtureName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".credentials.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A credential read cut short by cancellation must not be cached: the next
// look has to ask the Keychain again instead of showing 未登录 for 10s.
func TestFetchClaudeCancelledCredentialReadNotCached(t *testing.T) {
	var calls int
	var cancel context.CancelFunc
	cli := &fakeClaudeCLI{usageOut: claudeResultJSON(claudeSampleUsage, false)}
	env := claudeEnv(t, t.TempDir())
	env.Keychain = func(context.Context, string, string) ([]byte, error) {
		calls++
		if calls == 1 {
			cancel()
			return nil, errNotLoggedIn
		}
		return os.ReadFile(filepath.Join("testdata", "quota", "claude_credentials.json"))
	}
	env.RunClaude = cli.run

	ctx, c := context.WithCancel(context.Background())
	cancel = c
	if q := FetchClaude(ctx, env); q.Status != QuotaFailed {
		t.Fatalf("cancelled read status = %v", q.Status)
	}
	if q := FetchClaude(context.Background(), env); q.Status != QuotaOK {
		t.Fatalf("the read after the cancel = %v, err %q (cached a cancelled read)", q.Status, q.Err)
	}
	if calls != 2 {
		t.Fatalf("keychain reads = %d, want 2", calls)
	}
}

func TestFetchClaudeSignedOutWithoutCLIReturnsNoCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	env := claudeEnv(t, t.TempDir())
	env.Keychain = keychainMissing
	env.RunClaude = nil

	q := FetchClaude(context.Background(), env)
	if q.Status != QuotaNoCLI {
		t.Fatalf("status = %v, want QuotaNoCLI", q.Status)
	}
}
