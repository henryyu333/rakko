//go:build darwin

package main

import (
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func diagLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return os.TempDir()
	}
	return filepath.Join(home, "Library", "Logs", "Rakko")
}

// startDiagnostics listens for SIGUSR1 and dumps live window, shortcut and
// goroutine-stack state into ~/Library/Logs/Rakko/diagnostics-<time>.txt.
func (a *shelf) startDiagnostics() {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			path, err := writeDiagnostics(a, diagLogDir())
			if err != nil {
				log.Printf("diagnostics: %v", err)
			} else {
				log.Printf("diagnostics written to %s", path)
			}
		}
	}()
}
