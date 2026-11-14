//go:build !windows

package aiusage

import (
	"context"
	"os"
)

// defaultKeychain reads Claude Code's sign-in from the macOS Keychain.
func defaultKeychain() func(ctx context.Context, service, account string) ([]byte, error) {
	return keychainPassword
}

// defaultClaudeUser is the account name Claude Code writes its Keychain item
// under.
func defaultClaudeUser() string { return os.Getenv("USER") }
