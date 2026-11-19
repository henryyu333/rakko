//go:build darwin

package platform

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestCaptureDisplayTimeoutKillsStalledProcess(t *testing.T) {
	oldTimeout, oldRun := captureDisplayTimeout, captureDisplayRun
	t.Cleanup(func() {
		captureDisplayTimeout = oldTimeout
		captureDisplayRun = oldRun
	})

	captureDisplayTimeout = 80 * time.Millisecond
	captureDisplayRun = func(ctx context.Context, _ int, _ string) error {
		return exec.CommandContext(ctx, "/bin/sleep", "5").Run()
	}

	start := time.Now()
	err := CaptureDisplay(1, filepath.Join(t.TempDir(), "shot.png"))
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CaptureDisplay err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("CaptureDisplay took %v, expected timeout kill around 80ms", elapsed)
	}
}

func TestMainLoopUsesNSEventTrackingRunLoopMode(t *testing.T) {
	if err := loadMainLoop(); err != nil {
		t.Fatalf("loadMainLoop: %v", err)
	}
	if len(modes) != 2 {
		t.Fatalf("len(modes) = %d, want 2", len(modes))
	}
	var got []string
	withPool(func() {
		for _, m := range modes {
			got = append(got, goString(id(m)))
		}
	})
	if got[0] != "kCFRunLoopDefaultMode" {
		t.Fatalf("modes[0] = %q, want kCFRunLoopDefaultMode", got[0])
	}
	if got[1] != "NSEventTrackingRunLoopMode" {
		t.Fatalf("modes[1] = %q, want NSEventTrackingRunLoopMode", got[1])
	}
}
