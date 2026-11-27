//go:build windows

package platform

// The virtual-desktop manager. Its class and interface identifiers must parse
// in either spelling, a malformed identifier must report instead of turning
// into a zero GUID, and the manager itself must be creatable on a real
// system: pinned cards cannot follow the user without it.

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestGUIDFromStringBothSpellings(t *testing.T) {
	bare := clsidVirtualDesktopManager[1 : len(clsidVirtualDesktopManager)-1]
	braced, err := guidFromString(clsidVirtualDesktopManager)
	if err != nil {
		t.Fatalf("braced identifier: %v", err)
	}
	plain, err := guidFromString(bare)
	if err != nil {
		t.Fatalf("bare identifier: %v", err)
	}
	if braced != plain {
		t.Fatalf("braced %v and bare %v parsed differently", braced, plain)
	}
	if braced == (windows.GUID{}) {
		t.Fatal("the identifier parsed to the nil GUID")
	}
	if braced.Data1 != 0xaa509086 {
		t.Fatalf("Data1 = %#x, want the identifier's first field 0xaa509086", braced.Data1)
	}
	if _, err := guidFromString("not-a-guid"); err == nil {
		t.Fatal("a malformed identifier parsed without an error")
	}
}

// TestVirtualDesktopManagerAvailable checks that both identifiers really do
// create the shell's manager. Before the fix the unbraced constants parsed to
// a nil GUID, CoCreateInstance failed, and the failure was cached for the
// process, so no pinned card ever followed the user's desktop.
func TestVirtualDesktopManagerAvailable(t *testing.T) {
	vdm, err := virtualDesktopManager()
	if err != nil {
		t.Fatalf("the virtual-desktop manager is unavailable: %v", err)
	}
	if vdm == 0 {
		t.Fatal("no manager and no error")
	}
	again, err := virtualDesktopManager()
	if err != nil || again != vdm {
		t.Fatalf("second call = %#x, %v; want the same object %#x", again, err, vdm)
	}
}

// A 64-bit SyscallN result sign-extends a failing HRESULT into the upper
// half of the word; the judgement must still see the 32-bit code.
func TestIsCOMDisconnectedReadsTheLowWord(t *testing.T) {
	extended := func(hr uintptr) uintptr {
		var upper uintptr = ^uintptr(0)
		return upper<<32 | hr
	}
	for _, hr := range []uintptr{
		0x80010108, 0x800706BA,
		extended(0x80010108), extended(0x800706BA),
	} {
		if !isCOMDisconnected(hr) {
			t.Errorf("isCOMDisconnected(%#x) = false, want the disconnect", hr)
		}
	}
	for _, hr := range []uintptr{0, 0x80004005, extended(0x80004005)} {
		if isCOMDisconnected(hr) {
			t.Errorf("isCOMDisconnected(%#x) = true, want an ordinary failure", hr)
		}
	}
}

// newDesktopFakeCOM is a COM object whose GetWindowDesktopID slot answers
// hresult.
func newDesktopFakeCOM(hresult uintptr) *fakeCOM {
	o := newFakeCOM()
	o.slot[virtualDesktopManagerGetWindowDesktopID] = windows.NewCallback(func(_, _, _ uintptr) uintptr {
		return hresult
	})
	return o
}

// A desktop query that lands on a manager whose Explorer restarted must
// release the stale object and ask the rebuilt one, not keep reporting no
// desktop until the app restarts.
func TestWindowDesktopIDRebuildsAcrossAnExplorerRestart(t *testing.T) {
	old := virtualDesktop
	t.Cleanup(func() { virtualDesktop = old })

	first := newDesktopFakeCOM(0x80010108)
	second := newDesktopFakeCOM(0)
	objects := []*fakeCOM{first, second}
	created, released := 0, 0
	virtualDesktop = &comObjectCache{
		create: func() (uintptr, error) {
			o := objects[min(created, len(objects)-1)]
			created++
			return o.ptr(), nil
		},
		release: func(uintptr) { released++ },
		isDead:  isCOMDisconnected,
	}
	_, ok := windowDesktopID(0x1234)
	if !ok {
		t.Fatal("windowDesktopID reported no desktop through the rebuilt object")
	}
	if created != 2 {
		t.Fatalf("created %d objects, want the dead one and its rebuild", created)
	}
	if released != 1 {
		t.Fatalf("released %d objects, want only the dead one", released)
	}
}
