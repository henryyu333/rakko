//go:build windows

package platform

// WinRT plumbing checks that do not need a real document: the async
// operation's reference is the caller's to give back, even when the
// operation fails.

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fakeAsyncOp returns an operation whose IAsyncInfo answers status and
// whose QueryInterface hands that IAsyncInfo out.
func fakeAsyncOp(status int32) (*fakeCOM, *fakeCOM) {
	op, info := newFakeCOM(), newFakeCOM()
	info.slot[7] = windows.NewCallback(func(_, out uintptr) uintptr {
		*(*int32)(wiPtr(out)) = status
		return 0
	})
	info.slot[8] = windows.NewCallback(func(_, out uintptr) uintptr {
		*(*uint32)(wiPtr(out)) = 0x80004005 // E_FAIL
		return 0
	})
	op.slot[0] = windows.NewCallback(func(_, _, out uintptr) uintptr {
		*(*unsafe.Pointer)(wiPtr(out)) = unsafe.Pointer(info)
		return 0
	})
	return op, info
}

func TestWIAwaitAndReleaseGivesBackTheOperation(t *testing.T) {
	op, info := fakeAsyncOp(wiAsyncCompleted)
	if err := wiAwaitAndRelease(op.ptr(), "render"); err != nil {
		t.Fatalf("wiAwaitAndRelease: %v", err)
	}
	if n := op.releases.Load(); n != 1 {
		t.Fatalf("the completed operation was released %d times, want once", n)
	}
	if n := info.releases.Load(); n != 1 {
		t.Fatalf("the IAsyncInfo was released %d times, want once", n)
	}
}

func TestWIAwaitAndReleaseGivesBackAFailedOperation(t *testing.T) {
	op, _ := fakeAsyncOp(wiAsyncError)
	if err := wiAwaitAndRelease(op.ptr(), "render"); err == nil {
		t.Fatal("a failed operation was reported as successful")
	}
	if n := op.releases.Load(); n != 1 {
		t.Fatalf("the failed operation was released %d times, want once", n)
	}
}
