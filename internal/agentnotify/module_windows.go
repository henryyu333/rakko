//go:build windows

package agentnotify

// Copilot CLI's hook files in the argv form its reference documents: exec
// names the program and args the arguments, and the tool runs them without a
// shell on every platform. The completion payloads name no event, so each
// entry passes --event itself.

const copilotHooksExec = `{
  "version": 1,
  "hooks": {
    "userPromptSubmitted": [{ __RAKKO_ENTRY:userPromptSubmitted__, "timeoutSec": 5 }],
    "agentStop": [{ __RAKKO_ENTRY:agentStop__, "timeoutSec": 5 }]
  }
}
`

const copilotApproveHooksExec = `{
  "version": 1,
  "hooks": {
    "permissionRequest": [{ __RAKKO_ENTRY:permissionRequest__, "timeoutSec": %d }]
  }
}
`
