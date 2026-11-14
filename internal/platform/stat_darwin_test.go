//go:build darwin

package platform

import (
	"testing"
	"time"
)

// TestCreationTime reads the birth time this Mac keeps for a folder.
func TestCreationTime(t *testing.T) {
	born, ok := CreationTime(t.TempDir())
	if !ok {
		t.Skip("this filesystem keeps no birth time")
	}
	if d := time.Since(born); d < -2*time.Minute || d > 2*time.Minute {
		t.Fatalf("folder birth %v is %v from now", born, d)
	}
}
