package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/atomicfile"
	"rakko/internal/platform"
)

// diagSnapshot holds the main-thread UI and shortcut state recorded by the
// live diagnostic trigger without quitting the app.
type diagSnapshot struct {
	Time               time.Time
	PID                int
	MainThreadOK       bool
	HasWindow          bool
	IgnoringMouse      bool
	NativeIgnoresMouse bool
	Visible            bool
	Peeking            bool
	ShotActive         bool
	ShortcutsSuspended bool
	ShortcutReg        map[shortcutKind]string
	ShortcutBusy       map[shortcutKind]bool

	ShellStyleIsland bool
	Level            int
	DisplayID        int64
	DisplayBounds    mygo.Rectangle
	CachedFrame      mygo.Rectangle
	TargetFrame      mygo.Rectangle
	HasNativeBounds  bool
	NativeBounds     mygo.Rectangle
	HasOccluded      bool
	WindowOccluded   bool
	BlankFrame       bool
	BlankQueued      bool
	BlankReady       bool
	IdleIslandDrawn  bool
	MainIdleExists   bool
	MainIdleShown    bool
	MainIdleFrame    mygo.Rectangle
	ShortcutFires    uint64
	LastShortcutKind shortcutKind
	LastShortcutAt   time.Time
}

var diagDispatch = mygo.RunOnMain

// collectDiagSnapshot reads shelf state; must run on the main thread.
func collectDiagSnapshot(a *shelf) diagSnapshot {
	s := diagSnapshot{
		Time:               time.Now(),
		PID:                os.Getpid(),
		MainThreadOK:       true,
		HasWindow:          a.win != nil,
		IgnoringMouse:      a.ignoringMouse,
		Visible:            a.visible,
		Peeking:            a.peeking,
		ShotActive:         a.shot != nil,
		ShortcutsSuspended: a.shortcutsSuspended,
	}
	s.ShellStyleIsland, s.Level, s.DisplayID = a.island, a.level, a.dispID
	s.DisplayBounds, s.CachedFrame, s.TargetFrame = a.dispBounds, a.frame, a.targetFrame()
	if a.win != nil {
		s.HasNativeBounds, s.NativeBounds = true, a.win.Bounds()
	}
	if a.winH != 0 {
		s.HasOccluded, s.WindowOccluded = true, windowOccluded(a.winH)
	}
	s.BlankFrame, s.BlankQueued, s.BlankReady, s.IdleIslandDrawn = a.blankFrame, a.blankQueued, a.blankReady, a.idleIslandDrawn
	if a.mainIdle != nil {
		s.MainIdleExists = true
		s.MainIdleShown, s.MainIdleFrame = a.mainIdle.shown, a.mainIdle.frame
	}
	s.ShortcutFires, s.LastShortcutKind, s.LastShortcutAt = a.shortcutFires, a.lastShortcutKind, a.lastShortcutAt
	if a.winH != 0 {
		s.NativeIgnoresMouse = platform.OverlayIgnoresMouse(a.winH)
	}
	if len(a.shortcutReg) > 0 {
		s.ShortcutReg = make(map[shortcutKind]string, len(a.shortcutReg))
		for k, v := range a.shortcutReg {
			s.ShortcutReg[k] = v
		}
	}
	if len(a.shortcutBusy) > 0 {
		s.ShortcutBusy = make(map[shortcutKind]bool, len(a.shortcutBusy))
		for k, v := range a.shortcutBusy {
			s.ShortcutBusy[k] = v
		}
	}
	return s
}

func formatDiagnostics(s diagSnapshot, stacks []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Rakko Diagnostics\n")
	fmt.Fprintf(&b, "Time: %s\n", s.Time.Format(time.RFC3339))
	fmt.Fprintf(&b, "PID: %d\n", s.PID)
	fmt.Fprintf(&b, "MainThreadResponsive: %v\n", s.MainThreadOK)
	if s.MainThreadOK {
		fmt.Fprintf(&b, "HasWindow: %v\n", s.HasWindow)
		fmt.Fprintf(&b, "IgnoringMouse (a.ignoringMouse): %v\n", s.IgnoringMouse)
		fmt.Fprintf(&b, "NativeIgnoresMouse (ignoresMouseEvents): %v\n", s.NativeIgnoresMouse)
		fmt.Fprintf(&b, "Visible (a.visible): %v\n", s.Visible)
		fmt.Fprintf(&b, "Peeking (a.peeking): %v\n", s.Peeking)
		fmt.Fprintf(&b, "ShotActive (a.shot != nil): %v\n", s.ShotActive)
		fmt.Fprintf(&b, "ShellStyleIsland (a.island): %v\n", s.ShellStyleIsland)
		fmt.Fprintf(&b, "Level (a.level): %d\n", s.Level)
		fmt.Fprintf(&b, "DisplayID (a.dispID): %d\n", s.DisplayID)
		fmt.Fprintf(&b, "DisplayBounds (a.dispBounds): %+v\n", s.DisplayBounds)
		fmt.Fprintf(&b, "CachedFrame (a.frame): %+v\n", s.CachedFrame)
		fmt.Fprintf(&b, "TargetFrame (a.targetFrame): %+v\n", s.TargetFrame)
		if s.HasNativeBounds {
			fmt.Fprintf(&b, "NativeBounds (win.Bounds): %+v\n", s.NativeBounds)
		}
		if s.HasOccluded {
			fmt.Fprintf(&b, "WindowOccluded: %v\n", s.WindowOccluded)
		}
		fmt.Fprintf(&b, "BlankState: frame=%v queued=%v ready=%v idleIslandDrawn=%v\n", s.BlankFrame, s.BlankQueued, s.BlankReady, s.IdleIslandDrawn)
		fmt.Fprintf(&b, "MainIdleCapsule: exists=%v shown=%v frame=%+v\n", s.MainIdleExists, s.MainIdleShown, s.MainIdleFrame)
		lastAt := "never"
		if !s.LastShortcutAt.IsZero() {
			lastAt = s.LastShortcutAt.Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "ShortcutFires: count=%d lastKind=%d lastAt=%s\n", s.ShortcutFires, s.LastShortcutKind, lastAt)
		fmt.Fprintf(&b, "ShortcutsSuspended: %v\n", s.ShortcutsSuspended)
		fmt.Fprintf(&b, "ShortcutsRegistered:\n")
		if len(s.ShortcutReg) == 0 {
			fmt.Fprintf(&b, "  (none)\n")
		} else {
			keys := make([]int, 0, len(s.ShortcutReg))
			for k := range s.ShortcutReg {
				keys = append(keys, int(k))
			}
			sort.Ints(keys)
			for _, k := range keys {
				kind := shortcutKind(k)
				busy := s.ShortcutBusy[kind]
				fmt.Fprintf(&b, "  kind=%d accel=%q busy=%v\n", k, s.ShortcutReg[kind], busy)
			}
		}
	}
	fmt.Fprintf(&b, "\n--- Goroutine Stacks ---\n%s\n", stacks)
	return b.String()
}

func allGoroutineStacks() []byte {
	buf := make([]byte, 64*1024)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			return buf[:n]
		}
		buf = make([]byte, len(buf)*2)
	}
}

// writeDiagnostics collects goroutine stacks and (with a 1s timeout) main
// thread state, then writes a timestamped report into dir.
func writeDiagnostics(a *shelf, dir string) (string, error) {
	now := time.Now()
	stacks := allGoroutineStacks()
	ch := make(chan diagSnapshot, 1)
	if a != nil {
		diagDispatch(func() {
			ch <- collectDiagSnapshot(a)
		})
	}
	var snap diagSnapshot
	select {
	case snap = <-ch:
	case <-time.After(1 * time.Second):
		snap = diagSnapshot{Time: now, PID: os.Getpid(), MainThreadOK: false}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := fmt.Sprintf("diagnostics-%s.txt", now.Format("20060102-150405.000"))
	content := []byte(formatDiagnostics(snap, stacks))
	if err := atomicfile.WriteFile(dir, name, content, 0o600); err != nil {
		return "", err
	}
	return filepath.Join(dir, name), nil
}
