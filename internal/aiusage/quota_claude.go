package aiusage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/unicode/norm"
)

// Claude Code answers /usage itself: ShelfU runs
//
//	claude -p /usage --output-format json --tools "" --strict-mcp-config
//	       --setting-sources "" --no-session-persistence
//
// in a throwaway directory and reads the text it printed. This is how
// Magpie reads the same allowance, and the reason ShelfU no longer asks
// Anthropic's usage endpoint with Claude Code's OAuth token: the numbers,
// the windows (including per-model weekly ones) and the reset times must
// be the ones the subscription itself reports.

// claudeCredentials is the JSON Claude Code keeps in the login Keychain
// (service "Claude Code-credentials") and mirrors into
// $CLAUDE_CONFIG_DIR/.credentials.json on some setups. The usage read
// never uses the token — Claude Code refreshes and sends it — the
// credentials only name the account and its plan.
type claudeCredentials struct {
	OAuth struct {
		AccessToken      string `json:"accessToken"`
		SubscriptionType string `json:"subscriptionType"`
	} `json:"claudeAiOauth"`
}

// claudeAuthStatus is `claude auth status --json`'s answer, the plan name
// Magpie trusts over the credentials'.
type claudeAuthStatus struct {
	LoggedIn         bool   `json:"loggedIn"`
	SubscriptionType string `json:"subscriptionType"`
}

const (
	// claudeCredentialTTL keeps a burst of fetches from spawning
	// `security` each time, while a fresh login is still noticed quickly.
	claudeCredentialTTL = 10 * time.Second
	// claudeStatusTTL is how long `claude auth status`'s answer serves.
	claudeStatusTTL = 30 * time.Second
	// claudeDeniedTTL is how long a Keychain read that may have shown the
	// user a dialog is not asked again: with the item's trust list not
	// holding `security`, every ask can show the dialog again.
	claudeDeniedTTL = 10 * time.Minute
	// claudeHomeService is the Keychain item of a plain ~/.claude install.
	claudeHomeService = "Claude Code-credentials"
)

// ClaudeAccount caches credential and auth-status reads across fetches.
// Guarded by mu; the fields are only read and written under it.
type ClaudeAccount struct {
	mu sync.Mutex

	credAt    time.Time
	cred      claudeCredentials
	credFound bool

	askedAt time.Time // a Keychain read that may have shown a dialog

	statusAt time.Time
	status   claudeAuthStatus
	statusOK bool
}

// inDeniedBackoff reports whether a Keychain read may not be retried yet.
func (a *ClaudeAccount) inDeniedBackoff(now time.Time) bool {
	if a == nil || a.askedAt.IsZero() {
		return false
	}
	return now.Sub(a.askedAt) < claudeDeniedTTL
}

// noteDenied records that a Keychain read may have shown a dialog the user
// answered no to.
func (a *ClaudeAccount) noteDenied(now time.Time) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.askedAt = now
	a.mu.Unlock()
}

// Invalidate forgets everything cached about one source: a login or logout
// changed the sign-in, and a Keychain read that backed off must be allowed
// again for the item Claude Code has just rewritten.
func (a *ClaudeAccount) Invalidate() {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.credAt, a.cred, a.credFound = time.Time{}, claudeCredentials{}, false
	a.statusAt, a.status, a.statusOK = time.Time{}, claudeAuthStatus{}, false
	a.askedAt = time.Time{}
	a.mu.Unlock()
}

// keychainDenied says whether a failed `security find-generic-password`
// could have been a dialog the user answered no to: 44 is the item not
// being there, which asks nobody.
func keychainDenied(err error) bool {
	var xe *exec.ExitError
	return errors.As(err, &xe) && xe.ExitCode() != 44
}

// claudeKeychainServiceFor is the Keychain service Claude Code keeps a
// config directory's sign-in in: "Claude Code-credentials", and with
// CLAUDE_CONFIG_DIR set that directory's own, told apart by a digest.
func claudeKeychainServiceFor(configDir string) string {
	if configDir == "" {
		return claudeHomeService
	}
	sum := sha256.Sum256([]byte(norm.NFC.String(configDir)))
	return claudeHomeService + "-" + hex.EncodeToString(sum[:])[:8]
}

var keychainAccountRE = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// claudeKeychainAccount is the account Claude Code keeps its sign-in
// under: $USER, else the login name, and claude-code-user for a name it
// won't use.
func claudeKeychainAccount(env Env) string {
	name := env.ClaudeUser
	if name == "" {
		name = os.Getenv("USER")
	}
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	if !keychainAccountRE.MatchString(name) {
		return "claude-code-user"
	}
	return name
}

// keychainText undoes `security find-generic-password -w` printing a value
// with a character it can't print — a newline — as hex.
func keychainText(out []byte) []byte {
	out = bytes.TrimSpace(out)
	if len(out) == 0 || len(out)%2 != 0 || out[0] == '{' {
		return out
	}
	b, err := hex.DecodeString(string(out))
	if err != nil || !json.Valid(b) {
		return out
	}
	return b
}

// claudeCredentialsFile is Claude Code's credentials file for the
// configured directory (or ~/.claude).
func claudeCredentialsFile(env Env) string {
	if env.ClaudeConfigDir != "" {
		return joinHome(env.ClaudeConfigDir, ".credentials.json")
	}
	return joinHome(env.Home, ".claude", ".credentials.json")
}

// readClaudeCredentials reads Claude Code's sign-in, Keychain first. An
// item that parses is the sign-in Claude Code goes by, emptied or not: a
// found-but-empty item means signed out and is never replaced by an older
// credentials file. Only a missing or unreadable item falls back to the
// file.
func readClaudeCredentials(ctx context.Context, env Env) (claudeCredentials, bool, error) {
	now := env.now()
	if env.Keychain != nil && !env.ClaudeAccount.inDeniedBackoff(now) {
		blob, err := env.Keychain(ctx, claudeKeychainServiceFor(env.ClaudeConfigDir), claudeKeychainAccount(env))
		if err == nil {
			var cred claudeCredentials
			if json.Unmarshal(keychainText(blob), &cred) == nil {
				return cred, true, nil
			}
		} else if keychainDenied(err) {
			env.ClaudeAccount.noteDenied(now)
		}
	}
	// A cancelled context must not fall through to a second credential
	// read: the caller already asked to stop.
	if err := ctx.Err(); err != nil {
		return claudeCredentials{}, false, err
	}
	data, err := os.ReadFile(claudeCredentialsFile(env))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return claudeCredentials{}, false, nil
		}
		return claudeCredentials{}, false, err
	}
	var cred claudeCredentials
	if json.Unmarshal(data, &cred) != nil {
		return claudeCredentials{}, false, nil // unusable file: no sign-in
	}
	return cred, true, nil
}

// claudeCredentialsCached reads the sign-in through env.ClaudeAccount's 10s
// cache; without the cache every fetch reads it afresh.
func claudeCredentialsCached(ctx context.Context, env Env) (claudeCredentials, bool, error) {
	a := env.ClaudeAccount
	if a == nil {
		return readClaudeCredentials(ctx, env)
	}
	now := env.now()
	a.mu.Lock()
	if !a.credAt.IsZero() && now.Sub(a.credAt) < claudeCredentialTTL {
		cred, found := a.cred, a.credFound
		a.mu.Unlock()
		return cred, found, nil
	}
	a.mu.Unlock()

	cred, found, err := readClaudeCredentials(ctx, env)
	if err != nil {
		// A cancelled or failed read says nothing about the sign-in:
		// caching it would show 未登录 for the next 10 seconds.
		return cred, found, err
	}

	a.mu.Lock()
	a.cred, a.credFound, a.credAt = cred, found, now
	a.mu.Unlock()
	return cred, found, nil
}

// claudeAuthStatus reads `claude auth status --json`, cached for 30s.
// statusOK is false when the command failed or printed no JSON.
func claudeAuthStatusOf(ctx context.Context, env Env) (claudeAuthStatus, bool) {
	a := env.ClaudeAccount
	now := env.now()
	if a != nil {
		a.mu.Lock()
		if !a.statusAt.IsZero() && now.Sub(a.statusAt) < claudeStatusTTL {
			st, ok := a.status, a.statusOK
			a.mu.Unlock()
			return st, ok
		}
		a.mu.Unlock()
	}
	out, _, err := runClaudeCode(ctx, env, []string{"auth", "status", "--json"}, env.ClaudeConfigDir)
	var st claudeAuthStatus
	ok := err == nil
	if ok {
		ok = json.Unmarshal(jsonObject(out), &st) == nil
	}
	if a != nil {
		a.mu.Lock()
		a.status, a.statusOK, a.statusAt = st, ok, now
		a.mu.Unlock()
	}
	return st, ok
}

// jsonObject returns the JSON object in s, ignoring any text around it.
func jsonObject(s string) []byte {
	s = strings.TrimSpace(s)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		return []byte(s[i : j+1])
	}
	return []byte(s)
}

// claudeUsageArgs is one /usage read: no tools, no user settings, no MCP
// servers, nothing kept on disk and no session.
func claudeUsageArgs() []string {
	return []string{"-p", "/usage", "--output-format", "json",
		"--tools", "", "--strict-mcp-config", "--setting-sources", "", "--no-session-persistence"}
}

// claudeReadingEnv picks the config directory a reading uses: Rakko's own
// when it holds a sign-in, the Mac's Claude Code otherwise. The two sources
// keep separate account caches, so a cached "signed out" in one can never
// answer for the other.
func claudeReadingEnv(ctx context.Context, env Env) Env {
	if env.RakkoClaudeDir != "" {
		priv := env
		priv.ClaudeConfigDir = env.RakkoClaudeDir
		priv.ClaudeAccount = env.RakkoClaudeAccount
		cred, found, err := claudeCredentialsCached(ctx, priv)
		if err == nil && found && cred.OAuth.AccessToken != "" {
			return priv
		}
	}
	return env
}

// FetchClaude reads the subscription allowance of the account Claude Code
// is signed in to, by running Claude Code's own /usage. Nothing is sent to
// Anthropic by ShelfU; Claude Code does that itself, with its own token,
// and refreshes the token when it must. An answer that says the sign-in is
// gone reports it; no local expiry check ever replaces it, so a refreshed
// token can never leave the card on 登录已过期.
func FetchClaude(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolClaude, FetchedAt: env.now()}

	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	env = claudeReadingEnv(ctx, env)
	cred, found, err := claudeCredentialsCached(ctx, env)
	if err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	if !found || cred.OAuth.AccessToken == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	q.Plan = prettyPlan(cred.OAuth.SubscriptionType)

	if st, ok := claudeAuthStatusOf(ctx, env); ok {
		if !st.LoggedIn {
			q.Status = QuotaNotLoggedIn
			return q
		}
		if name := prettyPlan(st.SubscriptionType); name != "" {
			q.Plan = name
		}
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	out, stderr, runErr := runClaudeCode(ctx, env, claudeUsageArgs(), env.ClaudeConfigDir)
	q.FetchedAt = env.now()
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		q.Status = QuotaFailed
		q.Err = shortErr(ctx.Err())
		return q
	}
	if errors.Is(runErr, errClaudeNotInstalled) {
		q.Status = QuotaNoCLI
		return q
	}

	res, ok := claudeResult(out)
	if !ok {
		msg := strings.TrimSpace(stderr)
		if msg == "" {
			msg = strings.TrimSpace(out)
		}
		if msg == "" && runErr != nil {
			msg = shortErr(runErr)
		}
		claudeFailure(&q, msg)
		return q
	}
	if res.IsError {
		claudeFailure(&q, res.Result)
		return q
	}
	ws, err := parseClaudeUsage(res.Result, q.FetchedAt)
	if err != nil {
		claudeFailure(&q, err.Error())
		return q
	}
	q.Windows = Elapsed(ws, q.FetchedAt)
	q.Status = QuotaOK
	return q
}

// claudeRun is the result Claude Code prints for a -p run with
// --output-format json.
type claudeRun struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// claudeResult reads the result object from what claude -p --output-format
// json printed: one object, or, with Claude Code's verbose output on, an
// array of every message with the result last.
func claudeResult(out string) (claudeRun, bool) {
	b := []byte(strings.TrimSpace(out))
	var res claudeRun
	if json.Unmarshal(b, &res) == nil && (res.Type != "" || res.Result != "") {
		return res, true
	}
	var all []claudeRun
	if json.Unmarshal(b, &all) != nil {
		return claudeRun{}, false
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Type == "result" {
			return all[i], true
		}
	}
	return claudeRun{}, false
}

// claudeFailure fills q from a message Claude Code printed, without any
// number: a sign-in the account lost, a limit it hit, or a read that told
// no allowance.
func claudeFailure(q *Quota, msg string) {
	msg = strings.TrimSpace(msg)
	if status, ok := claudeDeniedStatus(msg); ok {
		q.Status = status
		q.Err = clipLine(msg)
		if status == QuotaLimitReached {
			now := q.FetchedAt
			if now.IsZero() {
				now = time.Now()
			}
			q.LimitResetsAt, _ = claudeLimitReset(msg, now)
		}
		return
	}
	q.Status = QuotaFailed
	if msg == "" {
		msg = "Claude Code's /usage reported no reading"
	}
	q.Err = clipLine(msg)
}

func clipLine(s string) string {
	if r := []rune(s); len(r) > 160 {
		return string(r[:160]) + "…"
	}
	return s
}

// runClaudeCode runs Claude Code (the injected runner, else the real
// binary) with args.
func runClaudeCode(ctx context.Context, env Env, args []string, configDir string) (string, string, error) {
	if env.RunClaude != nil {
		return env.RunClaude(ctx, args, configDir)
	}
	return runClaudeCommand(ctx, env, args, configDir)
}

// errClaudeNotInstalled says no Claude Code binary was found.
var errClaudeNotInstalled = errors.New("Claude Code is not installed")
