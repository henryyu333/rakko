//go:build darwin

package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
)

// installDiagSignal arms SIGUSR1 before anything else runs: the default
// action for the signal is to kill the process, and the launch that hangs is
// exactly the one whose report is wanted. The handler holds no shelf: before
// one exists, the report carries the goroutine stacks alone, which is what a
// stuck launch needs.
func installDiagSignal() {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			diagReportMu.Lock()
			target := diagTarget
			diagReportMu.Unlock()
			path, err := writeDiagnostics(target, diagLogDir())
			if err != nil {
				log.Printf("diagnostics: %v", err)
			} else {
				log.Printf("diagnostics written to %s", path)
			}
		}
	}()
}

// diagTarget is the shelf live reports read, registered once it exists.
var (
	diagReportMu sync.Mutex
	diagTarget   *shelf
)

func diagLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	return filepath.Join(home, "Library", "Logs", "Rakko")
}

// startDiagnostics lets live SIGUSR1 reports read this shelf's state; the
// signal itself is armed at the top of main so a launch that never reaches
// here still answers the signal with a goroutine-stack report.
func (a *shelf) startDiagnostics() {
	diagReportMu.Lock()
	diagTarget = a
	diagReportMu.Unlock()
}
