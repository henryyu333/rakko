package aiusage

import (
	"net/http"
	"testing"
	"time"
)

func TestPrettyPlan(t *testing.T) {
	for in, want := range map[string]string{
		"plus":      "Plus",
		"pro":       "Pro",
		"prolite":   "Pro Lite",
		"pro_lite":  "Pro Lite",
		"max":       "Max",
		"free":      "Free",
		"team":      "Team",
		"business":  "Business",
		"unknown-x": "Unknown X",
		"":          "",
		"  plus  ":  "Plus",
	} {
		if got := prettyPlan(in); got != want {
			t.Fatalf("prettyPlan(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	now := fakeNow
	if d := retryAfter("30", now); d != 30*time.Second {
		t.Fatalf("seconds: %v", d)
	}
	if d := retryAfter("2.5", now); d != 2500*time.Millisecond {
		t.Fatalf("fractional seconds: %v", d)
	}
	if d := retryAfter("", now); d != 5*time.Minute {
		t.Fatalf("empty: %v", d)
	}
	if d := retryAfter("garbage", now); d != 5*time.Minute {
		t.Fatalf("garbage: %v", d)
	}
	if d := retryAfter(now.Add(60*time.Second).UTC().Format(http.TimeFormat), now); d != time.Minute {
		t.Fatalf("http date: %v", d)
	}
}

func TestNearestSlot(t *testing.T) {
	for d, want := range map[time.Duration]int{
		5 * time.Hour:      slotFiveHour,
		4 * time.Hour:      slotFiveHour,
		24 * time.Hour:     slotFiveHour, // closer to 5h than to 7d
		7 * 24 * time.Hour: slotWeekly,
		6 * 24 * time.Hour: slotWeekly,
		8 * 24 * time.Hour: slotWeekly,
	} {
		if got := nearestSlot(d); got != want {
			t.Fatalf("nearestSlot(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestJWTExpired(t *testing.T) {
	if !jwtExpired(makeJWT(fakeNow.Add(-time.Minute).Unix()), fakeNow) {
		t.Fatal("past exp should be expired")
	}
	if jwtExpired(makeJWT(fakeNow.Add(time.Minute).Unix()), fakeNow) {
		t.Fatal("future exp should not be expired")
	}
	if jwtExpired("not-a-jwt", fakeNow) {
		t.Fatal("non-JWT should not be treated as expired")
	}
	if jwtExpired("a.b", fakeNow) {
		t.Fatal("two segments should not be treated as expired")
	}
}

func TestDefaultEnv(t *testing.T) {
	env := DefaultEnv()
	if env.Home == "" {
		t.Fatal("Home should be set")
	}
	if env.HTTP == nil || env.HTTP.Timeout != 15*time.Second {
		t.Fatalf("HTTP client timeout = %v", env.HTTP)
	}
	requireDefaultKeychain(t, env)
	if env.CodexBaseURL != "https://chatgpt.com/backend-api" {
		t.Fatalf("CodexBaseURL = %q", env.CodexBaseURL)
	}
	if env.ClaudeAccount == nil {
		t.Fatal("ClaudeAccount cache should be set")
	}
	if env.Now == nil {
		t.Fatal("Now should be set")
	}
}
