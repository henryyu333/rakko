package aiusage

import (
	"net/http"
	"os"
	"path/filepath"
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

// A token configured through ~/.claude/settings.json is claimed for a
// vendor only when the base URL's host really is that vendor's: a proxy
// or a look-alike whose name merely mentions the vendor must never have
// its token sent to the vendor's real endpoint.
func TestVendorTokenClaimNeedsVendorHost(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(base string) {
		if err := os.WriteFile(filepath.Join(claude, "settings.json"),
			[]byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"tok-1","ANTHROPIC_BASE_URL":"`+base+`"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, base := range []string{
		"https://open.bigmodel.cn/api/anthropic",
		"https://api.z.ai/v1",
	} {
		write(base)
		env := testEnv(home)
		if _, _, ok := readGLMKey(env); !ok {
			t.Errorf("readGLMKey rejected the vendor's own host %q", base)
		}
		if _, ok := readDeepSeekKey(env); ok {
			t.Errorf("readDeepSeekKey claimed a GLM host %q", base)
		}
	}
	write("https://api.deepseek.com/anthropic")
	env := testEnv(home)
	if _, ok := readDeepSeekKey(env); !ok {
		t.Error("readDeepSeekKey rejected the vendor's own host")
	}
	if _, _, ok := readGLMKey(env); ok {
		t.Error("readGLMKey claimed a DeepSeek host")
	}
	for _, base := range []string{
		"https://evil-deepseek.com.example/anthropic",
		"https://bigmodel.cn.evil.example/api/anthropic",
		"https://z.ai-proxy.example/v1",
		"http://deepseek.com.fake/",
	} {
		write(base)
		env := testEnv(home)
		if _, _, ok := readGLMKey(env); ok {
			t.Errorf("readGLMKey claimed a token configured for %q", base)
		}
		if _, ok := readDeepSeekKey(env); ok {
			t.Errorf("readDeepSeekKey claimed a token configured for %q", base)
		}
	}
}
