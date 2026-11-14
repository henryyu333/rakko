//go:build darwin

package platform

import (
	"fmt"
	"sync"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// Review P11: a click on a desktop pin took the key window away from the
// app the user was typing in. mygo's shared MyGoPanel class answers YES
// to canBecomeKeyWindow, and setBecomesKeyOnlyIfNeeded: does not stop a
// click from making the window key. ConfigurePinWindow now grafts a
// canBecomeKeyWindow override that answers NO — but only for the marked
// window, like the constrainFrameRect: graft in ConfigureNotchWindow.
//
// Plain borderless NSPanels cannot exercise this: AppKit already answers
// NO for windows without a title bar, so a stock panel would make the
// test green without the graft. keyProbePanel mirrors MyGoPanel — a
// shared NSPanel subclass whose canBecomeKeyWindow unconditionally
// answers YES — so the graft is the only thing standing between a click
// and stolen key focus, and removing it turns the tests red.
var (
	keyProbeOnce  sync.Once
	keyProbeClass id
)

func keyProbePanel() id {
	keyProbeOnce.Do(func() {
		c, err := objc.RegisterClass("ShelfKeyProbePanel", objc.GetClass("NSPanel"), nil, nil,
			[]objc.MethodDef{
				{Cmd: sel("canBecomeKeyWindow"), Fn: func(self id, _ objc.SEL) bool { return true }},
			})
		if err != nil {
			panic(fmt.Sprintf("cannot register probe panel class: %v", err))
		}
		keyProbeClass = id(c)
	})
	return keyProbeClass
}

// newKeyProbe allocs a YES-answering probe panel the way mygo builds a
// MyGoPanel: borderless content rect, nonactivating style. Call on the
// main thread inside withPool.
func newKeyProbe(initPanel func(id, objc.SEL, NSRect, uint, uint, bool) id) id {
	return initPanel(send(keyProbePanel(), "alloc"), sel("initWithContentRect:styleMask:backing:defer:"),
		NSRect{Size: NSSize{Width: 120, Height: 60}}, nsWindowStyleMaskNonactivatingPanel, 2, false)
}

// A configured probe panel must report canBecomeKeyWindow NO, stay
// non-key through an explicit makeKeyWindow: request, and keep the
// level/nonactivating/all-spaces traits the pin relies on — while an
// unconfigured sibling of the same class keeps the stock YES, proving
// the graft stays per-instance. Headless: panel creation and the
// makeKeyWindow: refusal only need AppKit on the main thread.
func TestPinPanelRefusesKeyWindow(t *testing.T) {
	onMain(t, func() error {
		if err := Init(); err != nil {
			return err
		}
		var initPanel func(id, objc.SEL, NSRect, uint, uint, bool) id
		purego.RegisterFunc(&initPanel, msgSendAddr)
		var pin, stock id
		withPool(func() {
			send(class("NSApplication"), "sharedApplication")
			pin, stock = newKeyProbe(initPanel), newKeyProbe(initPanel)
			if pin != 0 {
				send(pin, "setReleasedWhenClosed:", 0)
				ConfigurePinWindow(uintptr(pin))
			}
			if stock != 0 {
				send(stock, "setReleasedWhenClosed:", 0)
			}
		})
		if pin == 0 || stock == 0 {
			return fmt.Errorf("probe panel alloc/init failed")
		}
		defer withPool(func() { send(pin, "orderOut:", 0); send(pin, "close"); send(stock, "close") })

		var pinCan, stockCan, pinKey, appKeyIsPin, onlyIfNeeded bool
		var level Layer
		var behavior, styleMask uint
		withPool(func() {
			// The real capability query — the value that used to be YES.
			pinCan = sendInt(pin, "canBecomeKeyWindow") != 0
			stockCan = sendInt(stock, "canBecomeKeyWindow") != 0
			level = WindowLayer(uintptr(pin))
			behavior = CollectionBehavior(uintptr(pin))
			styleMask = uint(sendInt(pin, "styleMask"))
			onlyIfNeeded = sendInt(pin, "becomesKeyOnlyIfNeeded") != 0
			// Request key through the real AppKit path: order the panel
			// in and ask for key status; the graft must refuse both.
			send(pin, "orderFront:", 0)
			send(pin, "makeKeyWindow")
			pinKey = sendInt(pin, "isKeyWindow") != 0
			appKeyIsPin = send(send(class("NSApplication"), "sharedApplication"), "keyWindow") == pin
		})

		if pinCan {
			return fmt.Errorf("pin window canBecomeKeyWindow = YES; a click would steal key focus from the active app")
		}
		if pinKey || appKeyIsPin {
			return fmt.Errorf("pin window became key after orderFront: + makeKeyWindow: (isKeyWindow=%v NSApp.keyWindow=%v)", pinKey, appKeyIsPin)
		}
		if !stockCan {
			return fmt.Errorf("unconfigured sibling of the shared class reports canBecomeKeyWindow = NO; the graft leaked to unrelated windows")
		}
		if level != LayerFloating {
			return fmt.Errorf("pin window layer = %d, want LayerFloating", level)
		}
		if styleMask&nsWindowStyleMaskNonactivatingPanel == 0 {
			return fmt.Errorf("pin styleMask 0x%x lost NonactivatingPanel", styleMask)
		}
		if behavior&collectionCanJoinAllSpaces == 0 {
			return fmt.Errorf("pin collectionBehavior 0x%x lost CanJoinAllSpaces", behavior)
		}
		if !onlyIfNeeded {
			return fmt.Errorf("pin lost setBecomesKeyOnlyIfNeeded:; the second refusal should stay")
		}
		return nil
	})
}

// The graft must stay per-window even inside a shared class: the
// production notch window is the same MyGoPanel class as a pin and still
// needs key status for the search field. A notch-configured probe must
// keep the stock YES while the pin probe answers NO, and only the pin
// carries the ownership mark.
func TestPinKeyGateStaysPerInstance(t *testing.T) {
	onMain(t, func() error {
		if err := Init(); err != nil {
			return err
		}
		var initPanel func(id, objc.SEL, NSRect, uint, uint, bool) id
		purego.RegisterFunc(&initPanel, msgSendAddr)
		var pin, stock, notch id
		withPool(func() {
			send(class("NSApplication"), "sharedApplication")
			pin, stock, notch = newKeyProbe(initPanel), newKeyProbe(initPanel), newKeyProbe(initPanel)
			for _, p := range []id{pin, stock, notch} {
				if p != 0 {
					send(p, "setReleasedWhenClosed:", 0)
				}
			}
			if pin != 0 {
				ConfigurePinWindow(uintptr(pin))
			}
			if notch != 0 {
				ConfigureNotchWindow(uintptr(notch))
			}
		})
		if pin == 0 || stock == 0 || notch == 0 {
			return fmt.Errorf("probe panel alloc/init failed")
		}
		defer withPool(func() { send(pin, "close"); send(stock, "close"); send(notch, "close") })

		var pinCan, stockCan, notchCan bool
		withPool(func() {
			pinCan = sendInt(pin, "canBecomeKeyWindow") != 0
			stockCan = sendInt(stock, "canBecomeKeyWindow") != 0
			notchCan = sendInt(notch, "canBecomeKeyWindow") != 0
		})

		if pinCan {
			return fmt.Errorf("pin window canBecomeKeyWindow = YES alongside other configured windows")
		}
		if !stockCan {
			return fmt.Errorf("unconfigured sibling reports canBecomeKeyWindow = NO; the graft leaked")
		}
		if !notchCan {
			return fmt.Errorf("notch-configured window reports canBecomeKeyWindow = NO; the pin graft stole key capability from the shared class")
		}
		return nil
	})
}
