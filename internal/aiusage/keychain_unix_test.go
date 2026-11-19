//go:build !windows

package aiusage

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeychainDenialCooldown(t *testing.T) {
	keychainDenyMu.Lock()
	keychainDenyUntil = map[string]time.Time{}
	keychainDenyMu.Unlock()

	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	origNow := keychainNow
	origExec := execSecurity
	keychainNow = func() time.Time { return now }
	var calls atomic.Int32
	execSecurity = func(context.Context, ...string) ([]byte, error) {
		calls.Add(1)
		return nil, errors.New("user canceled")
	}
	t.Cleanup(func() {
		keychainNow = origNow
		execSecurity = origExec
		keychainDenyMu.Lock()
		keychainDenyUntil = map[string]time.Time{}
		keychainDenyMu.Unlock()
	})

	if _, err := keychainPassword(context.Background(), "svc-test", "acct"); err == nil {
		t.Fatal("expected error on first denied call")
	}
	if _, err := keychainPassword(context.Background(), "svc-test", "acct"); !errors.Is(err, errKeychainCooldown) {
		t.Fatalf("second call err = %v, want errKeychainCooldown", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("execSecurity calls = %d, want 1 during cooldown", calls.Load())
	}

	// After cooldown expires, it tries again.
	now = now.Add(keychainDenyCooldown + time.Second)
	if _, err := keychainPassword(context.Background(), "svc-test", "acct"); err == nil || errors.Is(err, errKeychainCooldown) {
		t.Fatalf("after cooldown err = %v, want real lookup error", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("execSecurity calls after cooldown = %d, want 2", calls.Load())
	}
}
