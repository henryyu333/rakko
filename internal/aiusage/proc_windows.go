//go:build windows

package aiusage

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

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
// The job kills what is left in it when its last handle closes, so a Rakko
// that dies mid-run does not leak Claude Code.
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

// newJob creates a job object that kills whatever is still in it when its
// last handle closes, so a Rakko that dies mid-run leaves nothing behind.
func newJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("aiusage: creating a process job: %w", err)
	}
	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("aiusage: setting up the process job: %w", err)
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
