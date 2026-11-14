//go:build windows

package aiusage

import "testing"

// requireDefaultKeychain: Windows has no Keychain, and Claude Code keeps no
// credential item there — its ~/.claude/.credentials.json file is the one
// sign-in the reader falls back to.
func requireDefaultKeychain(t *testing.T, env Env) {
	t.Helper()
	if env.Keychain != nil {
		t.Fatal("Windows must not read a Keychain")
	}
}
