//go:build !windows

package agentnotify

// The macOS entry is the guarded shell line Rakko has always written: a
// removed app leaves the agent's own flow intact. There is no shim, because
// the line itself carries everything.

// hookEntryOS renders one completion entry for macOS.
func hookEntryOS(exe string, tool Tool, dataDir, event string) hookEntry {
	return hookEntry{fields: []hookField{{"command", posixHookCommand(exe, tool, dataDir, event)}}}
}

// approveEntryOS renders one approval entry for macOS.
func approveEntryOS(exe string, tool Tool, dataDir string) hookEntry {
	return hookEntry{fields: []hookField{{"command", posixApproveCommand(exe, tool, dataDir)}}}
}

// execFormField is empty on macOS: every entry is a shell line.
func execFormField(Tool) string { return "" }

// expectedShellCommands is empty on macOS: its entries are recognized by the
// legacy marker rule.
func expectedShellCommands(string, Tool, hookKind) []string { return nil }

// prepareHookShim writes nothing on macOS.
func prepareHookShim(hookKind, spec, string, string) error { return nil }

// removeShim removes nothing on macOS.
func removeShim(string, hookKind, Tool) error { return nil }

// shimExisted is always false on macOS: there are no shims.
func shimExisted(string, hookKind, Tool) bool { return false }

// validateShimFallbacks has nothing to check away from cmd.exe.
func validateShimFallbacks() error { return nil }
