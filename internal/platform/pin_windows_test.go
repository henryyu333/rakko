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
