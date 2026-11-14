//go:build !windows

package aiusage

import "testing"

// requireDefaultKeychain: the production environment reads Claude Code's
// sign-in from the Keychain.
func requireDefaultKeychain(t *testing.T, env Env) {
	t.Helper()
	if env.Keychain == nil {
		t.Fatal("Keychain should be set")
	}
}
