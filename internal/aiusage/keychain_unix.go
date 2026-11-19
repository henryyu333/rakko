//go:build !windows

package aiusage

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

const keychainDenyCooldown = 10 * time.Minute

var (
	keychainDenyMu    sync.Mutex
	keychainDenyUntil = map[string]time.Time{}
	keychainNow       = time.Now
	execSecurity      = func(ctx context.Context, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, "/usr/bin/security", args...).Output()
	}
	errKeychainCooldown = errors.New("keychain lookup cooled down after denial")
)

func isKeychainNotFound(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 44
}

// keychainPassword reads a generic password via /usr/bin/security, which does
// not pop an authorization dialog for the items Claude Code stores. The
// account name is part of the lookup, as Claude Code writes its item under
// $USER. Denied lookups are cooled down so periodic source polling does not
// repeatedly prompt the user.
func keychainPassword(ctx context.Context, service, account string) ([]byte, error) {
	cacheKey := service + "\x00" + account
	now := keychainNow()
	keychainDenyMu.Lock()
	until := keychainDenyUntil[cacheKey]
	keychainDenyMu.Unlock()
	if now.Before(until) {
		return nil, errKeychainCooldown
	}

	args := []string{"find-generic-password", "-s", service, "-w"}
	if account != "" {
		args = append(args, "-a", account)
	}
	out, err := execSecurity(ctx, args...)
	if err != nil {
		if !isKeychainNotFound(err) && ctx.Err() == nil {
			keychainDenyMu.Lock()
			keychainDenyUntil[cacheKey] = now.Add(keychainDenyCooldown)
			keychainDenyMu.Unlock()
		}
		return nil, err
	}
	return trimLineEnd(out), nil
}
