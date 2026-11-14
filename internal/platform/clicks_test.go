//go:build darwin

package platform

import (
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
	"runtime"
	"testing"
)

type smokePoint struct{ X, Y float64 }

func TestMouseObservationAllButtonsAndStop(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := Init(); err != nil {
		t.Fatal(err)
	}
	app := send(class("NSApplication"), "sharedApplication")
	var event func(id, objc.SEL, uint, smokePoint, uint, float64, int, id, int, int, float64) id
	purego.RegisterFunc(&event, msgSendAddr)
	calls := 0
	stop := ObserveMouseDown(0, func(inside bool, button int) {
		if !inside {
			t.Error("Expected local nil-window event")
		}
		calls++
	})
	for _, kind := range []uint{1, 3, 25} {
		ev := event(class("NSEvent"), sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"), kind, smokePoint{}, 0, 0, 0, 0, 1, 1, 1)
		if ev == 0 {
			t.Fatal("event creation failed")
		}
		send(app, "sendEvent:", uintptr(ev))
	}
	stop()
	stop()
	if calls != 3 {
		t.Fatalf("all buttons must be observed: got %d callbacks", calls)
	}
	ev := event(class("NSEvent"), sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:"), 1, smokePoint{}, 0, 0, 0, 0, 1, 1, 1)
	send(app, "sendEvent:", uintptr(ev))
	if calls != 3 {
		t.Fatal("monitor still running after stop")
	}
}
