//go:build windows

package aiusage

import (
	"context"
	"os"
)

// defaultKeychain is nil on Windows: Claude Code keeps no Keychain item
// there, and its credentials file under ~/.claude is the only sign-in.
func defaultKeychain() func(ctx context.Context, service, account string) ([]byte, error) {
	return nil
}

// defaultClaudeUser is the login name Windows reports. Only the Keychain
// lookup uses it, and Windows does not read one.
func defaultClaudeUser() string { return os.Getenv("USERNAME") }
