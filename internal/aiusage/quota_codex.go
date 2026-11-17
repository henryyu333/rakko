package aiusage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// codexAuth is ~/.codex/auth.json (Codex CLI login state).
type codexAuth struct {
	Tokens struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

// codexUsage is GET {base}/wham/usage. Windows are identified by their
// limit_window_seconds length (18000 = 5h, 604800 = 7d), never by which slot
// (primary/secondary) they arrive in — the vendor has reordered them before.
type codexUsage struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		Primary   *codexWindow `json:"primary_window"`
		Secondary *codexWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

type codexWindow struct {
	// used_percent absent must not read as 0% used (a fake "100% left"):
	// presence is part of the value.
	UsedPct   *float64 `json:"used_percent"`
	WindowSec int64    `json:"limit_window_seconds"`
	ResetAt   int64    `json:"reset_at"`
}

const (
	codex5hLen = 18000 * time.Second
	codex7dLen = 604800 * time.Second
)

// FetchCodex reads Codex's local login ($CODEX_HOME or ~/.codex/auth.json) and
// asks ChatGPT's backend for the subscription's rate-limit windows. An
// expired access token returns QuotaExpired without any request — Rakko never
// refreshes tokens, because refreshing would rotate Codex's own session.
// FetchedAt is stamped when the response arrives, so a Retry-After delay
// counts from receipt; ctx cancellation short-circuits before any read.
func FetchCodex(ctx context.Context, env Env) Quota {
	q := Quota{Tool: ToolCodex, FetchedAt: env.now()}

	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	auth, err := readCodexAuth(env.Home)
	if err != nil {
		if errors.Is(err, errNotLoggedIn) {
			q.Status = QuotaNotLoggedIn
		} else {
			q.Status = QuotaFailed
			q.Err = shortErr(err)
		}
		return q
	}
	if auth.Tokens.AccessToken == "" || auth.Tokens.AccountID == "" {
		q.Status = QuotaNotLoggedIn
		return q
	}
	if jwtExpired(auth.Tokens.AccessToken, q.FetchedAt) {
		q.Status = QuotaExpired
		return q
	}
	if err := ctx.Err(); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}

	res, err := env.get(ctx, env.codexBase()+"/wham/usage", [][2]string{
		{"Authorization", "Bearer " + auth.Tokens.AccessToken},
		{"chatgpt-account-id", auth.Tokens.AccountID},
		{"Accept", "application/json"},
	})
	q.FetchedAt = env.now() // response received (or the request failed) now
	if finishStatus(&q, res, err) {
		return q
	}

	var usage codexUsage
	if err := decodeJSON(res.body, &usage); err != nil {
		q.Status = QuotaFailed
		q.Err = shortErr(err)
		return q
	}
	q.Plan = prettyPlan(usage.PlanType)
	var five, week *Window
	for _, w := range []*codexWindow{usage.RateLimit.Primary, usage.RateLimit.Secondary} {
		if w == nil || w.UsedPct == nil || w.WindowSec <= 0 {
			// A window without a known utilization or a known length is
			// unknown data, not a healthy 100%-left one: skip it.
			continue
		}
		win := &Window{UsedPct: *w.UsedPct, Length: time.Duration(w.WindowSec) * time.Second}
		if w.ResetAt > 0 {
			win.ResetsAt = time.Unix(w.ResetAt, 0)
		}
		if nearestSlot(win.Length) == slotWeekly {
			setWindow(&week, win)
		} else {
			setWindow(&five, win)
		}
	}
	if five != nil {
		five.Name = "5 hours"
		q.Windows = append(q.Windows, *five)
	}
	if week != nil {
		week.Name = "7 days"
		q.Windows = append(q.Windows, *week)
	}
	q.Status = QuotaOK
	return q
}

// readCodexAuth loads $CODEX_HOME/auth.json, or ~/.codex/auth.json when
// CODEX_HOME is unset. A missing file or unusable JSON means not logged in.
func readCodexAuth(home string) (codexAuth, error) {
	var auth codexAuth
	dir := os.Getenv("CODEX_HOME")
	if dir == "" {
		dir = joinHome(home, ".codex")
	}
	data, err := readFile(joinHome(dir, "auth.json"))
	if err != nil {
		return auth, err
	}
	if err := json.Unmarshal(data, &auth); err != nil {
		return auth, fmt.Errorf("%w: unreadable auth.json", errNotLoggedIn)
	}
	return auth, nil
}

// window slots for nearest-length classification.
const (
	slotFiveHour = iota
	slotWeekly
)

// nearestSlot returns the canonical window (5h or 7d) a length is closest to.
func nearestSlot(d time.Duration) int {
	if abs(d-codex5hLen) <= abs(d-codex7dLen) {
		return slotFiveHour
	}
	return slotWeekly
}

func abs(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// setWindow keeps the first window mapped to a slot when two windows have
// the same length class.
func setWindow(dst **Window, w *Window) {
	if *dst == nil {
		*dst = w
	}
}

// jwtExpired reports whether a JWT's exp claim is in the past. Tokens that
// are not JWTs, or carry no exp, are treated as not expired — the server
// answers 401 and we map that to QuotaExpired anyway.
func jwtExpired(token string, now time.Time) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return false
	}
	return now.Unix() >= claims.Exp
}
