//go:build !windows

package aiusage

import (
	"context"
	"os/exec"
)

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
