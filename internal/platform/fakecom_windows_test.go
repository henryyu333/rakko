//go:build windows

package platform

// Scripted COM objects for tests. A fake's vtable slots are Go callbacks,
// so a test can pin which method a raw vtable call reaches and what it
// passes — the slot numbers are the part of the ABI no compiler checks.

import (
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fakeCOMSlots is as many vtable entries as a test can script; every
// interface the platform calls by slot fits far below it.
const fakeCOMSlots = 32

// fakeCOM is a COM object whose first word points at the callback slots.
type fakeCOM struct {
	vtbl *[fakeCOMSlots]uintptr
	slot [fakeCOMSlots]uintptr
	// releases counts IUnknown::Release calls.
	releases atomic.Int32
}

// newFakeCOM returns an object whose IUnknown answers: QueryInterface
// fails, AddRef returns 1 and Release counts.
func newFakeCOM() *fakeCOM {
	o := &fakeCOM{}
	o.vtbl = &o.slot
	o.slot[0] = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0x80004005 })
	o.slot[1] = windows.NewCallback(func(_ uintptr) uintptr { return 1 })
	o.slot[2] = windows.NewCallback(func(_ uintptr) uintptr { return uintptr(o.releases.Add(1)) })
	return o
}

// ptr is the interface pointer the platform's vtable calls take.
func (o *fakeCOM) ptr() uintptr { return uintptr(unsafe.Pointer(o)) }

// pinSlot makes every slot in [from, to] record the slot that was called,
// so a test can prove a call reached the method it names. The slots answer
// E_FAIL: a call that lands on one changed the object's observable result.
func (o *fakeCOM) pinSlot(from, to int, called *atomic.Int32) {
	for slot := from; slot <= to; slot++ {
		o.slot[slot] = windows.NewCallback(func(_, _, _ uintptr) uintptr {
			called.Store(int32(slot))
			return 0x80004005
		})
	}
}
