//go:build windows

package main

import (
	"log"

	"rakko/internal/platform"
)

// logWindowDiagnostics records the window's band, which is the one flag
// Windows exposes for the overlay's stacking order.
func logWindowDiagnostics(handle uintptr) {
	log.Printf("window layer=%d", platform.WindowLayer(handle))
}
