//go:build windows

package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	diagKernel32           = syscall.NewLazyDLL("kernel32.dll")
	diagCreateEventW       = diagKernel32.NewProc("CreateEventW")
	diagWaitForSingleObj   = diagKernel32.NewProc("WaitForSingleObject")
	diagCloseHandle        = diagKernel32.NewProc("CloseHandle")
)

func diagLogDir(dataDir string) string {
	if local := os.Getenv("LOCALAPPDATA"); local != "" {
		return filepath.Join(local, "Rakko", "Logs")
	}
	if dataDir != "" {
		return filepath.Join(dataDir, "Logs")
	}
	return os.TempDir()
}

// installDiagSignal creates the named Win32 auto-reset event
// Local\RakkoDiagnostics-<pid> as early as main can; signalling it dumps
// live state and goroutine stacks into %LOCALAPPDATA%\Rakko\Logs\
// diagnostics-<time>.txt. Before the shelf exists the report carries the
// stacks alone.
func installDiagSignal() {
	name, err := syscall.UTF16PtrFromString(fmt.Sprintf(`Local\RakkoDiagnostics-%d`, os.Getpid()))
	if err != nil {
		return
	}
	h, _, err := diagCreateEventW.Call(0, 0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		log.Printf("diagnostics: CreateEventW: %v", err)
		return
	}
	go func() {
		defer diagCloseHandle.Call(h)
		const infinite = 0xFFFFFFFF
		const waitObject0 = 0
		for {
			r, _, _ := diagWaitForSingleObj.Call(h, infinite)
			if r != waitObject0 {
				return
			}
			diagReportMu.Lock()
			target := diagTarget
			diagReportMu.Unlock()
			dir := diagLogDir("")
			if target != nil {
				dir = diagLogDir(target.dataDir)
			}
			path, err := writeDiagnostics(target, dir)
			if err != nil {
				log.Printf("diagnostics: %v", err)
			} else {
				log.Printf("diagnostics written to %s", path)
			}
		}
	}()
}
