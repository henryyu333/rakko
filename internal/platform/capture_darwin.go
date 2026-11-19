//go:build darwin

package platform

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

var (
	captureDisplayTimeout = 15 * time.Second
	captureDisplayRun     = func(ctx context.Context, index int, path string) error {
		return exec.CommandContext(ctx, "/usr/sbin/screencapture", "-x", "-D", strconv.Itoa(index), path).Run()
	}
)

// PickScreenRegion runs the system's own region picker: crosshair, Esc to
// cancel, Space for the window mode. The shutter sound stays off. A
// cancelled pick or a denied capture writes nothing, so a missing file
// means the user cancelled.
func PickScreenRegion(path string) error {
	return exec.Command("/usr/sbin/screencapture", "-i", "-x", path).Run()
}

// CaptureDisplay freezes display index as a PNG at path. index is the
// display's one-based position in CGGetActiveDisplayList order, the same
// order the screen APIs hand out. A stalled screencapture process is killed
// after captureDisplayTimeout so the screenshot session never stays stuck.
func CaptureDisplay(index int, path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), captureDisplayTimeout)
	defer cancel()
	if err := captureDisplayRun(ctx, index, path); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// ScreenshotFolder is where the system saves screenshots, or "" when the
// user never changed it and the desktop is the destination.
func ScreenshotFolder() string {
	out, err := exec.Command("/usr/bin/defaults", "read", "com.apple.screencapture", "location").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
