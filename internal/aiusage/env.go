package aiusage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"rakko/internal/netproxy"
)

// Env injects every machine dependency the quota fetchers touch: the user's
// home directory (auth files live under it), the HTTP client, macOS Keychain
// access, the Codex endpoint, Claude Code's command line and the clock. A
// token read through Env is only sent to its own vendor — never logged or
// persisted. Claude Code's /usage is the one Claude reader: ShelfU never
// asks Anthropic itself, and reads Claude's token only to name the plan.
type Env struct {
	Home         string
	HTTP         *http.Client
	Keychain     func(ctx context.Context, service, account string) ([]byte, error)
	CodexBaseURL string
	Now          func() time.Time

	// Claude Code. ClaudePath overrides the binary (tests and probes);
	// empty searches the usual install locations. ClaudeConfigDir is
	// CLAUDE_CONFIG_DIR ("" means ~/.claude); ClaudeUser is the Keychain
	// account name ("" reads $USER). RunClaude replaces the process run;
	// nil runs the real binary. ClaudeAccount caches credential and
	// auth-status reads across fetches; nil keeps no cache.
	ClaudePath      string
	ClaudeConfigDir string
	ClaudeUser      string
	RunClaude       ClaudeRun
	ClaudeAccount   *ClaudeAccount

	// RakkoClaudeDir is Rakko's own Claude Code config directory (empty
	// when the app has none). A sign-in there takes precedence over the
	// Mac's; RakkoClaudeAccount caches it separately so the two sources
	// never answer for each other. RunClaudeLogin replaces the real
	// `claude auth login` run in tests.
	RakkoClaudeDir     string
	RakkoClaudeAccount *ClaudeAccount
	RunClaudeLogin     ClaudeLoginRun
}

// ClaudeRun runs Claude Code with args in configDir and returns its stdout,
// stderr and process error.
type ClaudeRun func(ctx context.Context, args []string, configDir string) (stdout, stderr string, err error)

// ClaudeLoginRun starts `claude auth login` against Rakko's private config
// directory. It returns the process's error, or ctx's when the sign-in was
// cancelled.
type ClaudeLoginRun func(ctx context.Context) error

const (
	codexDefaultBase = "https://chatgpt.com/backend-api"
	quotaHTTPTimeout = 15 * time.Second
)

// DefaultEnv returns the production environment: real home directory, a 15s
// HTTP timeout over the app's proxy rules (environment, else the macOS system
// proxy: quota requests are the outbound path a Finder-launched process has no
// proxy variables for), the macOS `security` tool for Keychain reads, the
// vendor's real endpoint and the user's own Claude Code.
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	return Env{
		Home:            home,
		HTTP:            netproxy.NewClient(quotaHTTPTimeout),
		Keychain:        keychainPassword,
		CodexBaseURL:    codexDefaultBase,
		Now:             time.Now,
		ClaudeConfigDir: os.Getenv("CLAUDE_CONFIG_DIR"),
		ClaudeUser:      os.Getenv("USER"),
		ClaudeAccount:   &ClaudeAccount{},

		RakkoClaudeAccount: &ClaudeAccount{},
	}
}

// keychainPassword reads a generic password via /usr/bin/security, which does
// not pop an authorization dialog for the items Claude Code stores. The
// account name is part of the lookup, as Claude Code writes its item under
// $USER.
func keychainPassword(ctx context.Context, service, account string) ([]byte, error) {
	args := []string{"find-generic-password", "-s", service, "-w"}
	if account != "" {
		args = append(args, "-a", account)
	}
	out, err := exec.CommandContext(ctx, "/usr/bin/security", args...).Output()
	if err != nil {
		return nil, err
	}
	return trimLineEnd(out), nil
}

func trimLineEnd(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return b
}

func (e Env) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// client is the injected client, or the production one over the app's proxy
// rules: a nil HTTP field must not mean "no system proxy".
func (e Env) client() *http.Client {
	if e.HTTP != nil {
		return e.HTTP
	}
	return netproxy.NewClient(quotaHTTPTimeout)
}

func (e Env) codexBase() string {
	if e.CodexBaseURL != "" {
		return strings.TrimRight(e.CodexBaseURL, "/")
	}
	return codexDefaultBase
}

// httpResult is one finished HTTP exchange.
type httpResult struct {
	status int
	header http.Header
	body   []byte
}

// get issues a GET and returns the response; the body is capped at 1 MiB.
// Redirects are never followed — even when the injected client has its own
// CheckRedirect — so the credential headers can only reach the configured
// endpoint, never a redirected-to host.
func (e Env) get(ctx context.Context, url string, headers [][2]string) (httpResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return httpResult{}, err
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	client := *e.client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	resp, err := client.Do(req)
	if err != nil {
		return httpResult{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{status: resp.StatusCode, header: resp.Header, body: body}, nil
}

// finishStatus maps a non-200 exchange onto q. It reports whether q is done:
// false means the response is a 200 whose body still needs decoding.
func finishStatus(q *Quota, res httpResult, err error) (done bool) {
	if err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return true
	}
	switch res.status {
	case http.StatusOK:
		return false
	case http.StatusUnauthorized, http.StatusForbidden:
		q.Status = QuotaExpired
	case http.StatusTooManyRequests:
		q.Status = QuotaRateLimited
		q.RetryAfter = retryAfter(res.header.Get("Retry-After"), q.FetchedAt)
	default:
		q.Status = QuotaFailed
		q.Err = fmt.Sprintf("HTTP %d", res.status)
	}
	return true
}

// shortErr keeps a one-line, token-free description of a transport error.
func shortErr(err error) string {
	s := err.Error()
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	const max = 160
	if len(s) > max {
		s = s[:max]
	}
	return s
}

// retryAfter parses a Retry-After header value (seconds or HTTP date); the
// vendors' endpoints rate-limit hard, so an unparsable or missing value falls
// back to a conservative 5 minutes.
func retryAfter(v string, now time.Time) time.Duration {
	const fallback = 5 * time.Minute
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	if secs, err := strconv.ParseFloat(v, 64); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
		return 0
	}
	return fallback
}

// errNotLoggedIn marks "no usable credentials on this machine".
var errNotLoggedIn = errors.New("not logged in")

// decodeJSON parses a response body; a short error is returned on failure.
func decodeJSON(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("invalid response: %s", shortErr(err))
	}
	return nil
}

// prettyPlan turns vendor plan ids ("plus", "pro", "prolite", "max", …) into
// display names ("Plus", "Pro", "Pro Lite", "Max"). Unknown words are
// title-cased word by word.
func prettyPlan(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	key := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
	known := map[string]string{
		"free": "Free", "plus": "Plus", "pro": "Pro", "prolite": "Pro Lite",
		"max": "Max", "team": "Team", "business": "Business",
		"enterprise": "Enterprise", "edu": "Edu",
	}
	if name, ok := known[key]; ok {
		return name
	}
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-' || unicode.IsSpace(r)
	})
	for i, w := range words {
		r := []rune(w)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
			words[i] = string(r)
		}
	}
	return strings.Join(words, " ")
}

// readFile reads a file under the env's home; a missing file maps to
// errNotLoggedIn so callers can treat it as "not logged in".
func readFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, errNotLoggedIn
		}
		return nil, err
	}
	return data, nil
}

func joinHome(home string, parts ...string) string {
	return filepath.Join(append([]string{home}, parts...)...)
}
