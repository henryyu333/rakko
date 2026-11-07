// Package aiusage is the contract for the AI 用量 page: quota windows and
// local token-usage records shared between the data collectors and the
// panel page. Only types and constants live here.
package aiusage

import "time"

// Tool names one AI tool the page reports on.
type Tool string

const (
	ToolOmp      Tool = "omp"
	ToolClaude   Tool = "claude"
	ToolCodex    Tool = "codex"
	ToolPi       Tool = "pi"
	ToolOpencode Tool = "opencode"
	ToolGrok     Tool = "grok"
)

// Window is one rate-limit window of a subscription quota, as the vendor
// named it: "5 hours", "7 days", or a model scope's "7 days · Opus".
// Model is the normalised scope word ("opus", "sonnet", "" for the
// unscoped weekly window).
type Window struct {
	Name     string
	Model    string
	UsedPct  float64       // 0–100
	Length   time.Duration // 5h or 7d: identified by length, not by position
	ResetsAt time.Time     // zero when the vendor named none
}

// LeftPct is the window's unused share, clamped to 0–100.
func (w Window) LeftPct() float64 {
	return max(0, min(100, 100-w.UsedPct))
}

// QuotaStatus is how a quota fetch went.
type QuotaStatus int

const (
	QuotaOK QuotaStatus = iota
	QuotaNotLoggedIn
	QuotaExpired
	QuotaRateLimited
	QuotaFailed
	// QuotaNoCLI means Claude Code is not installed on this machine, so
	// its /usage can't be read.
	QuotaNoCLI
	// QuotaLimitReached means the account's allowance ran out: the vendor
	// refused the work ("You've hit your session limit"), so older numbers
	// are stale news and the limit itself is what the card must say.
	QuotaLimitReached
)

// Transient reports whether a failed status says nothing about the account
// and may be papered over with the last good reading: a transport error or
// a rate limit on the query itself. An answer about the account (signed
// out, sign-in expired, allowance used up, no CLI) is shown as it came.
func (s QuotaStatus) Transient() bool {
	return s == QuotaRateLimited || s == QuotaFailed
}

// ClaudeSource names the Claude Code configuration directory a reading
// came from: Rakko's own sign-in, the Mac's Claude Code, or neither yet.
type ClaudeSource int

const (
	ClaudeSourceNone ClaudeSource = iota
	ClaudeSourceRakko
	ClaudeSourceUser
)

// Quota is one tool's subscription quota at FetchedAt; Windows is empty
// while the fetch could not produce any (see Status and Err).
type Quota struct {
	Tool Tool
	Plan string
	// ClaudeSource is the config directory Claude's reading came from;
	// only ToolClaude sets it.
	ClaudeSource ClaudeSource
	Windows      []Window
	Status       QuotaStatus
	FetchedAt    time.Time
	// LimitResetsAt is when the allowance a QuotaLimitReached answer
	// blocked comes back, when the vendor's message named it.
	LimitResetsAt time.Time
	RetryAfter    time.Duration
	Err           string
}

// Elapsed returns ws as they stand at now: a window whose reset time has
// passed was renewed, so it reads 0 used with no reset time left to show.
func Elapsed(ws []Window, now time.Time) []Window {
	out := make([]Window, len(ws))
	for i, w := range ws {
		if !w.ResetsAt.IsZero() && !now.Before(w.ResetsAt) {
			w.UsedPct, w.ResetsAt = 0, time.Time{}
		}
		out[i] = w
	}
	return out
}

// PaceInfo compares a window's usage against an even pace. AheadPct > 0
// means running faster than even; RunsOutIn is meaningful only when
// LastsUntilReset is false.
type PaceInfo struct {
	ExpectedLeftPct float64
	AheadPct        float64
	RunsOutIn       time.Duration
	LastsUntilReset bool
}

// Record is one assistant message's token usage, as logged by its tool.
// Cost is the tool's own price; Priced is false when the record carried no
// price (only omp records prices).
type Record struct {
	Tool                     Tool
	At                       time.Time
	Model                    string
	Input, Output, CacheRead int64
	CacheWrite               int64
	Cost                     float64
	Priced                   bool
}

// Range is the usage summary's time span.
type Range int

const (
	RangeToday Range = iota
	Range7d
	Range30d
	RangeAll
	// RangeSpan marks a Summary built by SummarySpan: an arbitrary [from,
	// to) span that none of the fixed ranges names.
	RangeSpan
)

// Totals sums records over a range; Unpriced counts records without a
// price, Cost only sums the ones that had one.
type Totals struct {
	Input, Output, CacheRead, CacheWrite, Requests, Unpriced int64
	Cost                                                     float64
}

// Bucket is one bar of the stacked chart: records grouped by tool.
// Tokens = input+output+cacheRead+cacheWrite.
type Bucket struct {
	Start  time.Time
	Tokens map[Tool]int64
}

// Summary is a range's totals and its stacked-chart buckets: 24 hourly
// buckets for RangeToday, daily buckets for the others (RangeAll caps at
// 365).
type Summary struct {
	Range   Range
	Total   Totals
	ByTool  map[Tool]Totals
	Buckets []Bucket
}
