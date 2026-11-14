//go:build windows

package platform

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ShareNearby rejects empty input before any window or COM work, so the
// check is safe without a desktop.
func TestShareNearbyRejectsEmptyInput(t *testing.T) {
	if err := ShareNearby(nil, nil); err == nil {
		t.Fatal("ShareNearby(nil, nil) = nil, want error")
	}
	if err := ShareNearby(nil, []string{}); err == nil {
		t.Fatal("ShareNearby(nil, no texts) = nil, want error")
	}
}

// TestShareItemsIterator checks the IIterable<IStorageItem> this file builds
// at its own ABI boundary: the slots must line up with the interfaces the
// Windows SDK declares, and an empty collection must end at once.
func TestShareItemsIterator(t *testing.T) {
	collection := shareNewItems(nil)
	ptr := uintptr(unsafe.Pointer(collection))

	var iterator uintptr
	if hr := shareComCall(ptr, 6 /* IIterable.First */, uintptr(unsafe.Pointer(&iterator))); shareFailed(hr) {
		t.Fatalf("First: %#x", uint32(hr))
	}
	if iterator == 0 {
		t.Fatal("First returned no iterator")
	}
	defer shareComRelease(iterator)

	var has uint8
	if hr := shareComCall(iterator, 7 /* IIterator.get_HasCurrent */, uintptr(unsafe.Pointer(&has))); shareFailed(hr) {
		t.Fatalf("HasCurrent: %#x", uint32(hr))
	}
	if has != 0 {
		t.Fatal("empty collection reports a current item")
	}
	var moved uint8
	if hr := shareComCall(iterator, 8 /* IIterator.MoveNext */, uintptr(unsafe.Pointer(&moved))); shareFailed(hr) {
		t.Fatalf("MoveNext: %#x", uint32(hr))
	}
	if moved != 0 {
		t.Fatal("empty collection moved to an item")
	}
	var item uintptr
	if hr := shareComCall(iterator, 6 /* IIterator.get_Current */, uintptr(unsafe.Pointer(&item))); hr != shareBounds {
		t.Fatalf("Current past the end = %#x, want E_BOUNDS", uint32(hr))
	}
	shareComRelease(ptr)
}

// TestShareGUIDs guards the interface identities against a spelling slip:
// the GUID parser wants braces, and a wrong IID reaches E_NOINTERFACE only
// at run time.
func TestShareGUIDs(t *testing.T) {
	cases := []struct {
		iid  windows.GUID
		want uint32
	}{
		{shareIIDDataTransferManagerInterop, 0x3a3dcd6c},
		{shareIIDDataTransferManager, 0xa5caee9b},
		{shareIIDDataPackage, 0x61ebf5c7},
		{shareIIDStorageFileStatics, 0x5984c710},
		{shareIIDStorageFolderStatics, 0x08f327ff},
		{shareIIDStorageItem, 0x4207a996},
		{shareIIDIterable, 0xfaa585ea},
		{shareIIDIterator, 0x6a79e863},
	}
	if shareIIDError != nil {
		t.Fatalf("share identifiers: %v", shareIIDError)
	}
	for _, c := range cases {
		if c.iid.Data1 != c.want {
			t.Fatalf("GUID %s parsed as %#x, want %#x", c.iid, c.iid.Data1, c.want)
		}
	}
	if shareIIDAsyncInfo.Data1 != 0x00000036 || shareIIDAsyncInfo.Data4 != [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46} {
		t.Fatalf("IAsyncInfo parsed as %s", shareIIDAsyncInfo)
	}
}
