//go:build !windows

package aiusage

import (
	"os/exec"
	"syscall"
	"time"
)

// procGroup is one child process run and every process it starts. The
// child gets its own session, so the group id is its pid and a signal to
// the negative id reaches the whole tree.
type procGroup struct {
	pid int
}

// claudeCommand builds the run for one Claude Code invocation.
func claudeCommand(path string, args, env []string) (*exec.Cmd, error) {
	cmd := exec.Command(path, args...)
	cmd.Env = env
	return cmd, nil
}

// startInGroup starts cmd in a new session and returns the handle that can
// end everything it starts.
func startInGroup(cmd *exec.Cmd) (*procGroup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procGroup{pid: cmd.Process.Pid}, nil
}

// kill ends every process of the run's session. The group keeps its id
// while any member lives, so it is signalled until none is left: a signal
// can miss a process being forked as it is sent.
func (g *procGroup) kill() {
	for range 200 {
		if syscall.Kill(-g.pid, syscall.SIGKILL) != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// release drops whatever the group still holds after the run ended.
func (g *procGroup) release() {}
