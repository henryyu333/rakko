//go:build windows

package aiusage

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	// closePseudoConsole is kernel32.ClosePseudoConsole; x/sys wraps
	// creating and resizing the console but not closing it.
	closePseudoConsole = windows.NewLazySystemDLL("kernel32.dll").NewProc("ClosePseudoConsole")
	// updateProcThreadAttribute is called directly: the pseudo-console
	// attribute takes the console handle itself in lpValue, while x/sys's
	// attribute-list helper passes a pointer to the value, which the API
	// then stores as a bogus handle. The Windows SDK's ConPTY sample passes
	// the handle the same way this does.
	updateProcThreadAttribute = windows.NewLazySystemDLL("kernel32.dll").NewProc("UpdateProcThreadAttribute")
)

// runClaudeLoginTTY starts the sign-in under a ConPTY pseudo console:
// without a terminal Claude Code offers a paste prompt instead of the
// terminal sign-in it drives. Its output is drained and dropped; only the
// exit code is read.
func runClaudeLoginTTY(ctx context.Context, argv []string, dir string, childEnv []string) error {
	inRead, inWrite, err := pipe()
	if err != nil {
		return err
	}
	outRead, outWrite, err := pipe()
	if err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		return err
	}
	var pc windows.Handle
	if err := windows.CreatePseudoConsole(windows.Coord{X: 120, Y: 40}, inRead, outWrite, 0, &pc); err != nil {
		windows.CloseHandle(inRead)
		windows.CloseHandle(inWrite)
		windows.CloseHandle(outRead)
		windows.CloseHandle(outWrite)
		return fmt.Errorf("aiusage: creating a pseudo console: %w", err)
	}
	// The console owns its copies of the conduits.
	windows.CloseHandle(inRead)
	windows.CloseHandle(outWrite)
	// Draining the console keeps a chatty process from blocking on a full
	// output buffer; what it prints includes the sign-in link, which this
	// app never shows or logs. Closing the console after the process ends
	// gives the drain its EOF.
	outFile := os.NewFile(uintptr(outRead), "conpty")
	drained := make(chan struct{})
	defer func() {
		closePseudoConsole.Call(uintptr(pc))
		windows.CloseHandle(inWrite)
		outFile.Close()
		<-drained
	}()
	go func() {
		_, _ = io.Copy(io.Discard, outFile)
		close(drained)
	}()

	attrs, err := windows.NewProcThreadAttributeList(1)
	if err != nil {
		return fmt.Errorf("aiusage: preparing the pseudo console: %w", err)
	}
	defer attrs.Delete()
	updated, _, callErr := updateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(attrs.List())), 0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(pc), unsafe.Sizeof(pc), 0, 0)
	if updated == 0 {
		return fmt.Errorf("aiusage: preparing the pseudo console: %w", callErr)
	}

	si := windows.StartupInfoEx{
		StartupInfo: windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfoEx{}))},
	}
	si.ProcThreadAttributeList = attrs.List()
	commandLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return err
	}
	currentDir, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	block := envBlock(childEnv)
	var pi windows.ProcessInformation
	err = windows.CreateProcess(nil, commandLine, nil, nil, false,
		windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT,
		&block[0], currentDir, &si.StartupInfo, &pi)
	// CreateProcess copies the block; it must stay reachable until the call
	// returns, and a bare *uint16 is not what the collector follows.
	runtime.KeepAlive(block)
	if err != nil {
		return fmt.Errorf("aiusage: starting the sign-in: %w", err)
	}
	windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	job, jobErr := newJob()
	if jobErr == nil {
		defer windows.CloseHandle(job)
		_ = assignToJob(job, pi.ProcessId)
	}

	done := make(chan error, 1)
	go func() {
		_, _ = windows.WaitForSingleObject(pi.Process, windows.INFINITE)
		var code uint32
		_ = windows.GetExitCodeProcess(pi.Process, &code)
		if code == 0 {
			done <- nil
			return
		}
		done <- fmt.Errorf("aiusage: the sign-in exited with code %d", code)
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		if jobErr == nil {
			_ = windows.TerminateJobObject(job, 1)
		} else {
			_ = windows.TerminateProcess(pi.Process, 1)
		}
		<-done
		return ctx.Err()
	}
}

// pipe makes one anonymous pipe.
func pipe() (read, write windows.Handle, err error) {
	if err := windows.CreatePipe(&read, &write, nil, 0); err != nil {
		return 0, 0, fmt.Errorf("aiusage: creating a pipe: %w", err)
	}
	return read, write, nil
}

// envBlock encodes an environment list as the double-NUL-terminated UTF-16
// block CREATE_UNICODE_ENVIRONMENT expects. The block must be sorted
// alphabetically by name; an unsorted one is what makes a child fail at
// DLL initialization.
func envBlock(env []string) []uint16 {
	sorted := slices.Clone(env)
	slices.SortFunc(sorted, func(a, b string) int {
		return strings.Compare(strings.ToUpper(envName(a)), strings.ToUpper(envName(b)))
	})
	var block []uint16
	for _, e := range sorted {
		block = append(block, utf16.Encode([]rune(e))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	return block
}

// envName is the part of an entry before '='.
func envName(entry string) string {
	name, _, _ := strings.Cut(entry, "=")
	return name
}
