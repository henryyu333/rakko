//go:build !windows

package agentnotify

// platformDecision leaves the decision bytes alone away from Windows: the
// hook's stdout goes straight to the tool.
func platformDecision(s string) string { return s }
