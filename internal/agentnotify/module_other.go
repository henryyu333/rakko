//go:build !windows

package agentnotify

// Copilot CLI's hook files keep the shell form on macOS, where the tool
// documents bash as the interpreter.

const (
	copilotHooksExec        = copilotHooks
	copilotApproveHooksExec = copilotApproveHooks
)
