//go:build windows

package aiusage

import (
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// procGroup is one child process run and the job object that holds it, so
// ending the group ends every process the run started — including one that
// would outlive a killed parent on its own.
type procGroup struct {
	job     windows.Handle
	process *exec.Cmd
}

// startInGroup starts cmd in its own process group inside a job object.
// Ending the job is what ends the tree; closing its handle on a run that
// finished on its own leaves the run's own children — a browser the
// sign-in opened, for one — alone, as the Unix session does.
func startInGroup(cmd *exec.Cmd) (*procGroup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP}
	job, err := newJob()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// Best effort: a process that could not be placed in the job is still
	// killable on its own, and the run is not aborted for it.
	_ = assignToJob(job, uint32(cmd.Process.Pid))
	return &procGroup{job: job, process: cmd}, nil
}

// kill ends the whole job, then the process itself if it never made it into
// the job.
func (g *procGroup) kill() {
	if g.job != 0 {
		_ = windows.TerminateJobObject(g.job, 1)
		return
	}
	_ = g.process.Process.Kill()
}

// release frees the job handle once the run is over.
func (g *procGroup) release() {
	if g.job != 0 {
		windows.CloseHandle(g.job)
		g.job = 0
	}
}

// newJob creates the job object that groups one run's process tree.
func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("aiusage: creating a process job: %w", err)
	}
	return job, nil
}

// assignToJob puts one running process into job.
func assignToJob(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}
