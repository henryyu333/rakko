//go:build windows

package platform

import (
	"runtime"
	"testing"
	"time"
	"unsafe"
)

func TestDragGuardDoesNotSpawnClampOrMoveCursor(t *testing.T) {
	r := DragGuardRegion{X: 0, Y: 0, Width: 2000, Height: 2000}
	var beforePt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&beforePt)))

	runtime.Gosched()
	time.Sleep(10 * time.Millisecond)
	beforeG := runtime.NumGoroutine()

	stop, err := StartDragGuard(r)
	if err != nil {
		t.Fatalf("StartDragGuard: %v", err)
	}
	if stop == nil {
		t.Fatal("StartDragGuard returned nil stop")
	}
	SetDragGuardRegion(DragGuardRegion{X: 100, Y: 0, Width: 500, Height: 48})
	ParkDragGuard(false)
	SetDragPin(r, true)

	time.Sleep(20 * time.Millisecond)
	afterG := runtime.NumGoroutine()

	var afterPt winPoint
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&afterPt)))

	SetDragPin(DragGuardRegion{}, false)
	stop()
	stop() // idempotent

	if afterG > beforeG {
		t.Fatalf("Windows drag guard spawned %d polling goroutine(s) (before=%d after=%d)", afterG-beforeG, beforeG, afterG)
	}
	if afterPt != beforePt {
		t.Fatalf("drag guard moved cursor from %+v to %+v", beforePt, afterPt)
	}
}
