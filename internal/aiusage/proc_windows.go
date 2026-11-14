//go:build windows

package aiusage

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// procGroup is one child process run and the job object that holds it, so
// ending the group ends every process the run started — including one that
// would outlive a killed parent on its own.
type procGroup struct {
	job windows.Handle
}

// startInGroup starts cmd in its own process group inside a job object.
// The process is created suspended and only resumed once the job holds it,
// so nothing it starts — a shell it opens, the browser the sign-in starts —
// can be outside the job. Ending the job is what ends the tree; closing its
// handle on a run that finished on its own leaves the run's own children
// alone, as the Unix session does.
func startInGroup(cmd *exec.Cmd) (*procGroup, error) {
	job, err := newJob()
	if err != nil {
		return nil, err
	}
	attr := &syscall.SysProcAttr{}
	if cmd.SysProcAttr != nil {
		*attr = *cmd.SysProcAttr
	}
	attr.CreationFlags = windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED
	cmd.SysProcAttr = attr
	if err := cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return nil, err
	}
	// A run that cannot be bound to its job is ended and reported: left
	// suspended it would never finish, and resumed without the job its
	// descendants would escape the kill.
	if err := bindToJob(job, uint32(cmd.Process.Pid)); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		windows.CloseHandle(job)
		return nil, err
	}
	return &procGroup{job: job}, nil
}

// bindToJob puts a process created suspended into job and then lets it
// run. The caller ends the process when it returns an error.
func bindToJob(job windows.Handle, pid uint32) error {
	if err := assignToJob(job, pid); err != nil {
		return fmt.Errorf("aiusage: binding the run to its job: %w", err)
	}
	if err := resumeMainThread(pid); err != nil {
		return fmt.Errorf("aiusage: resuming the run: %w", err)
	}
	return nil
}

// kill ends the whole job.
func (g *procGroup) kill() {
	_ = windows.TerminateJobObject(g.job, 1)
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

// assignToJob puts one process into job, before it is allowed to run. It is
// a variable because a test takes the failure path through it, which a
// healthy job never produces.
var assignToJob = func(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}

// resumeMainThread lets a process created suspended run. os/exec closes the
// thread handle CreateProcess returned, so the thread is found by its owner
// instead: a process created suspended has exactly one.
func resumeMainThread(pid uint32) error {
	for range 1000 {
		snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
		if err != nil {
			return err
		}
		var entry windows.ThreadEntry32
		entry.Size = uint32(unsafe.Sizeof(entry))
		err = windows.Thread32First(snap, &entry)
		for err == nil {
			if entry.OwnerProcessID == pid {
				windows.CloseHandle(snap)
				return resumeThread(entry.ThreadID)
			}
			entry.Size = uint32(unsafe.Sizeof(entry))
			err = windows.Thread32Next(snap, &entry)
		}
		windows.CloseHandle(snap)
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("aiusage: the process %d has no thread to resume", pid)
}

// resumeThread resumes one thread and closes the handle it opened.
func resumeThread(id uint32) error {
	h, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, id)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	_, err = windows.ResumeThread(h)
	return err
}
