package aiusage

import (
	"context"
	"crypto/rand"
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
	if len(q.Windows) == 0 {
		// A schema drift or a non-subscription answer dressed as 200:
		// saying OK would freeze the last good numbers and hide it.
		q.Status = QuotaFailed
		q.Err = "no usage windows"
		return q
	}
	q.Status = QuotaOK
	return q
}

// FetchCodexResetCredits queries Codex's available rate-limit reset credits
// and returns the count and soonest expiration.
func FetchCodexResetCredits(ctx context.Context, env Env) (*ResetCredits, error) {
	auth, err := readCodexAuth(env.Home)
	if err != nil {
		return nil, err
	}
	if auth.Tokens.AccessToken == "" || auth.Tokens.AccountID == "" {
		return nil, errNotLoggedIn
	}
	if jwtExpired(auth.Tokens.AccessToken, env.now()) {
		return nil, errors.New("codex sign-in expired")
	}
	return fetchCodexResetCredits(ctx, env, auth.Tokens.AccessToken, auth.Tokens.AccountID)
}

type codexResetCreditsWire struct {
	AvailableCount int                           `json:"available_count"`
	Credits        []codexResetCreditsWireCredit `json:"credits"`
}

type codexResetCreditsWireCredit struct {
	ID        string  `json:"id"`
	ResetType string  `json:"reset_type"`
	Status    string  `json:"status"`
	GrantedAt string  `json:"granted_at"`
	ExpiresAt *string `json:"expires_at"`
}

func (w codexResetCreditsWire) first() (id string, soonest time.Time, count int) {
	for _, c := range w.Credits {
		if c.Status != "available" || c.ID == "" {
			continue
		}
		count++
		var t time.Time
		if c.ExpiresAt != nil {
			if p, err := time.Parse(time.RFC3339, *c.ExpiresAt); err == nil {
				t = p
			}
		}
		if id == "" || (!t.IsZero() && (soonest.IsZero() || t.Before(soonest))) {
			id, soonest = c.ID, t
		}
	}
	if w.AvailableCount > count {
		count = w.AvailableCount
	}
	return id, soonest, count
}

func codexHeaders(token, accountID string) [][2]string {
	return [][2]string{
		{"Authorization", "Bearer " + token},
		{"chatgpt-account-id", accountID},
		{"Accept", "application/json"},
	}
}

func fetchCodexResetCredits(ctx context.Context, env Env, token, accountID string) (*ResetCredits, error) {
	rctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	res, err := env.get(rctx, env.codexBase()+"/wham/rate-limit-reset-credits", codexHeaders(token, accountID))
	if err != nil {
		return nil, err
	}
	if res.status < 200 || res.status >= 300 {
		return nil, fmt.Errorf("HTTP %d", res.status)
	}
	var wire codexResetCreditsWire
	if err := decodeJSON(res.body, &wire); err != nil {
		return nil, err
	}
	id, soonest, count := wire.first()
	if count <= 0 {
		return nil, nil
	}
	return &ResetCredits{
		Count:     count,
		SoonestAt: soonest,
		FirstID:   id,
	}, nil
}

// UseCodexReset consumes one Codex rate-limit reset credit (the one expiring
// soonest). If no available credit exists, it returns code "no_credit" without
// sending a consume request.
func UseCodexReset(ctx context.Context, env Env) (ResetOutcome, error) {
	auth, err := readCodexAuth(env.Home)
	if err != nil {
		return ResetOutcome{}, err
	}
	if auth.Tokens.AccessToken == "" || auth.Tokens.AccountID == "" {
		return ResetOutcome{}, errNotLoggedIn
	}
	if jwtExpired(auth.Tokens.AccessToken, env.now()) {
		return ResetOutcome{}, errors.New("codex sign-in expired")
	}
	rc, err := fetchCodexResetCredits(ctx, env, auth.Tokens.AccessToken, auth.Tokens.AccountID)
	if err != nil {
		return ResetOutcome{}, err
	}
	if rc == nil || rc.Count <= 0 || rc.FirstID == "" {
		return ResetOutcome{Code: "no_credit"}, nil
	}
	redeemID := newUUIDv4()
	body, err := json.Marshal(map[string]string{
		"credit_id":         rc.FirstID,
		"redeem_request_id": redeemID,
	})
	if err != nil {
		return ResetOutcome{}, err
	}
	headers := append(codexHeaders(auth.Tokens.AccessToken, auth.Tokens.AccountID),
		[2]string{"Content-Type", "application/json"},
	)
	res, err := env.post(ctx, env.codexBase()+"/wham/rate-limit-reset-credits/consume", headers, body)
	if err != nil {
		return ResetOutcome{PostSent: true}, err
	}
	if res.status < 200 || res.status >= 300 {
		return ResetOutcome{PostSent: true}, fmt.Errorf("HTTP %d", res.status)
	}
	var out ResetOutcome
	if err := decodeJSON(res.body, &out); err != nil {
		return ResetOutcome{PostSent: true}, err
	}
	out.PostSent = true
	return out, nil
}

func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
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
