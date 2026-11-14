//go:build darwin

package main

import (
	"log"

	"rakko/internal/platform"
)

// logWindowDiagnostics records the window flags that decide how Mission
// Control and Show Desktop treat the overlay.
func logWindowDiagnostics(handle uintptr) {
	log.Printf("window collectionBehavior=0x%x", platform.CollectionBehavior(handle))
}
