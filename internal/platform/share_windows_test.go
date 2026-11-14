//go:build windows

package platform

import (
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
		{shareIIDIterableStorageItem, 0xbb8b8418},
		{shareIIDIteratorStorageItem, 0x05b487c2},
		{shareIIDDataRequestedHandler, 0xec6f9cc8},
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

// --- interface identities --------------------------------------------------

// shareParameterizedIID builds the IID of a parameterized type instance the
// way WinRT does: an RFC 4122 version 5 UUID over the pinterface namespace and
// the signature "pinterface({piid};arg;...)". The test spells the algorithm
// out so a mistyped constant in the file fails here instead of at the panel.
func shareParameterizedIID(t *testing.T, signature string) windows.GUID {
	t.Helper()
	ns := windows.GUID{Data1: 0x11f47ad5, Data2: 0x7b73, Data3: 0x42c0, Data4: [8]byte{0xab, 0xae, 0x87, 0x8b, 0x1e, 0x16, 0xad, 0xee}}
	// The namespace goes in as RFC 4122 bytes: the first three fields in
	// network order, the last eight as they read.
	name := append(shareGUIDBytes(ns), signature...)
	sum := sha1.Sum(name)
	var raw [16]byte
	copy(raw[:], sum[:16])
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	var g windows.GUID
	g.Data1 = binary.BigEndian.Uint32(raw[0:4])
	g.Data2 = binary.BigEndian.Uint16(raw[4:6])
	g.Data3 = binary.BigEndian.Uint16(raw[6:8])
	copy(g.Data4[:], raw[8:16])
	return g
}

// shareGUIDBytes is guid.ToByteArray(): the in-memory form of a GUID.
func shareGUIDBytes(g windows.GUID) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint32(b[0:4], g.Data1)
	binary.BigEndian.PutUint16(b[4:6], g.Data2)
	binary.BigEndian.PutUint16(b[6:8], g.Data3)
	copy(b[8:], g.Data4[:])
	return b
}

func TestShareParameterizedIIDs(t *testing.T) {
	const item = "{4207a996-ca2f-42f7-bde8-8b10457a7f30}"
	cases := []struct {
		name string
		iid  windows.GUID
		sig  string
	}{
		{
			"IIterable<IStorageItem>",
			shareIIDIterableStorageItem,
			"pinterface({faa585ea-6214-4217-afda-7f46de5869b3};" + item + ")",
		},
		{
			"IIterator<IStorageItem>",
			shareIIDIteratorStorageItem,
			"pinterface({6a79e863-4300-459a-9966-cbb660963ee1};" + item + ")",
		},
		{
			"TypedEventHandler<DataTransferManager, DataRequestedEventArgs>",
			shareIIDDataRequestedHandler,
			"pinterface({9de1c534-6ae1-11e0-84e1-18a905bcc53f};" +
				"rc(Windows.ApplicationModel.DataTransfer.DataTransferManager;{a5caee9b-8708-49d1-8d36-67d25a8da00c});" +
				"rc(Windows.ApplicationModel.DataTransfer.DataRequestedEventArgs;{cb8ba807-6ac5-43c9-8ac5-9ba232163182}))",
		},
	}
	for _, c := range cases {
		if want := shareParameterizedIID(t, c.sig); c.iid != want {
			t.Errorf("%s = %s, want %s from %s", c.name, c.iid, want, c.sig)
		}
	}
}

// TestShareCollectionsUseInstantiatedIIDs checks the identity our collection
// and iterator publish. The PIID of the generic definition is not a valid
// QueryInterface argument, so the runtime saw E_NOINTERFACE before.
func TestShareCollectionsUseInstantiatedIIDs(t *testing.T) {
	instanceIID := windows.GUID{Data1: 0xfaa585ea, Data2: 0x6214, Data3: 0x4217, Data4: [8]byte{0xaf, 0xda, 0x7f, 0x46, 0xde, 0x58, 0x69, 0xb3}}
	collection := shareNewItems(nil)
	ptr := uintptr(unsafe.Pointer(collection))
	defer shareComRelease(ptr)

	if _, hr := shareQueryInterface(ptr, shareIIDIterableStorageItem); hr != shareOK {
		t.Fatalf("collection QI IIterable<IStorageItem>: %#x", uint32(hr))
	}
	if _, hr := shareQueryInterface(ptr, instanceIID); hr != shareNoInterface {
		t.Fatalf("collection QI the generic PIID = %#x, want E_NOINTERFACE", uint32(hr))
	}
	if got := shareTestGetIIDs(t, ptr); got != shareIIDIterableStorageItem {
		t.Fatalf("collection GetIids = %s, want %s", got, shareIIDIterableStorageItem)
	}

	var iter uintptr
	if hr := shareComCall(ptr, 6 /* IIterable.First */, uintptr(unsafe.Pointer(&iter))); shareFailed(hr) || iter == 0 {
		t.Fatalf("First: %#x", uint32(hr))
	}
	defer shareComRelease(iter)
	if _, hr := shareQueryInterface(iter, shareIIDIteratorStorageItem); hr != shareOK {
		t.Fatalf("iterator QI IIterator<IStorageItem>: %#x", uint32(hr))
	}
	if got := shareTestGetIIDs(t, iter); got != shareIIDIteratorStorageItem {
		t.Fatalf("iterator GetIids = %s, want %s", got, shareIIDIteratorStorageItem)
	}
}

func shareTestGetIIDs(t *testing.T, obj uintptr) windows.GUID {
	t.Helper()
	var count uint32
	var iids uintptr
	if hr := shareComCall(obj, 3 /* IInspectable.GetIids */, uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&iids))); shareFailed(hr) {
		t.Fatalf("GetIids: %#x", uint32(hr))
	}
	defer windows.CoTaskMemFree(shareUnsafePointer(iids))
	if count != 1 {
		t.Fatalf("GetIids returned %d interfaces, want 1", count)
	}
	return *(*windows.GUID)(shareUnsafePointer(iids))
}

// TestShareDelegateIdentity checks the DataRequested handler answers for the
// instantiated TypedEventHandler interface, its IUnknown and its agile
// identity.
func TestShareDelegateIdentity(t *testing.T) {
	handler := uintptr(unsafe.Pointer(shareHandlerDelegate()))
	for _, iid := range []windows.GUID{shareIIDUnknown, shareIIDAgileObject, shareIIDDataRequestedHandler} {
		if _, hr := shareQueryInterface(handler, iid); hr != shareOK {
			t.Fatalf("delegate QI %s = %#x, want S_OK", iid, uint32(hr))
		}
	}
}

// TestShareIterableIIDsMatchTheSystem asks Windows for its own collection of
// storage items and checks the IIDs the runtime reports are the ones this file
// queries. The inbox implementation is the authority on the instantiated IID.
func TestShareIterableIIDsMatchTheSystem(t *testing.T) {
	if err := Init(); err != nil {
		t.Skipf("COM unavailable: %v", err)
	}
	var fileFactory, folderFactory uintptr
	item, err := shareStorageItem(&fileFactory, &folderFactory, os.TempDir())
	shareComRelease(fileFactory)
	shareComRelease(folderFactory)
	if err != nil {
		t.Skipf("no storage folder for %s: %v", os.TempDir(), err)
	}
	defer shareComRelease(item)
	folder, hr := shareQueryInterface(item, shareIIDStorageFolder)
	if folder == 0 {
		t.Fatalf("QI IStorageFolder: %#x", uint32(hr))
	}
	defer shareComRelease(folder)
	var operation uintptr
	if hr := shareComCall(folder, 15 /* IStorageFolder.GetItemsAsync */, uintptr(unsafe.Pointer(&operation))); shareFailed(hr) || operation == 0 {
		t.Fatalf("GetItemsAsync: %#x", uint32(hr))
	}
	defer shareComRelease(operation)
	if err := shareAwait(operation); err != nil {
		t.Fatalf("GetItemsAsync: %v", err)
	}
	var view uintptr
	if hr := shareComCall(operation, 8 /* IAsyncOperation.GetResults */, uintptr(unsafe.Pointer(&view))); shareFailed(hr) || view == 0 {
		t.Fatalf("GetResults: %#x", uint32(hr))
	}
	defer shareComRelease(view)
	iterable, hr := shareQueryInterface(view, shareIIDIterableStorageItem)
	if iterable == 0 {
		t.Fatalf("the system collection rejects IIterable<IStorageItem> (%#x): the IID in this file is stale", uint32(hr))
	}
	defer shareComRelease(iterable)
	var iter uintptr
	if hr := shareComCall(iterable, 6 /* IIterable.First */, uintptr(unsafe.Pointer(&iter))); shareFailed(hr) || iter == 0 {
		t.Fatalf("First: %#x", uint32(hr))
	}
	defer shareComRelease(iter)
	if _, hr := shareQueryInterface(iter, shareIIDIteratorStorageItem); hr != shareOK {
		t.Fatalf("the system iterator rejects IIterator<IStorageItem> (%#x): the IID in this file is stale", uint32(hr))
	}
}

// --- the data request runs off the main thread -----------------------------

// The roles a shareFake plays in the request tests.
const (
	shareFakeArgs = iota
	shareFakeRequest
	shareFakeDeferral
	shareFakePackage
	shareFakeItem
)

// shareFake is a COM object the share code can call in a test. Its vtable
// answers the slots those calls land on, and every object stays rooted so the
// collector never frees memory COM still holds by raw pointer.
type shareFake struct {
	vtable   *[24]uintptr
	role     int
	refs     int32
	request  uintptr
	deferral uintptr
	setData  uintptr
	setItem  uintptr
	failed   bool
	done     bool
	mu       sync.Mutex
}

var (
	shareFakeVtable [24]uintptr
	shareTestFakes  []*shareFake
)

func init() {
	shareFakeVtable = [24]uintptr{
		windows.NewCallback(shareFakeQueryInterface),
		windows.NewCallback(shareFakeAddRef),
		windows.NewCallback(shareFakeRelease),
	}
	for i := 3; i < len(shareFakeVtable); i++ {
		switch i {
		case 6:
			shareFakeVtable[i] = windows.NewCallback(shareFakeSlot6)
		case 7:
			shareFakeVtable[i] = windows.NewCallback(shareFakeSlot7)
		case 9:
			shareFakeVtable[i] = windows.NewCallback(shareFakeSlot9)
		case 10:
			shareFakeVtable[i] = windows.NewCallback(shareFakeSlot10)
		case 23:
			shareFakeVtable[i] = windows.NewCallback(shareFakeSlot23)
		default:
			shareFakeVtable[i] = windows.NewCallback(shareFakeUnsupported)
		}
	}
}

func newShareFake(role int) uintptr {
	f := &shareFake{vtable: &shareFakeVtable, role: role, refs: 1}
	shareTestFakes = append(shareTestFakes, f)
	return uintptr(unsafe.Pointer(f))
}

func shareFakeAt(this uintptr) *shareFake { return (*shareFake)(shareUnsafePointer(this)) }

func shareFakeQueryInterface(this, iid, out uintptr) uintptr {
	if out == 0 {
		return sharePointer
	}
	*(*uintptr)(shareUnsafePointer(out)) = this
	atomic.AddInt32(&shareFakeAt(this).refs, 1)
	return shareOK
}

func shareFakeAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&shareFakeAt(this).refs, 1))
}

func shareFakeRelease(this uintptr) uintptr {
	if n := atomic.AddInt32(&shareFakeAt(this).refs, -1); n > 0 {
		return uintptr(n)
	}
	return 0
}

func shareFakeSlot6(this, a, _ uintptr) uintptr {
	f := shareFakeAt(this)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.role {
	case shareFakeArgs:
		if a == 0 {
			return sharePointer
		}
		*(*uintptr)(shareUnsafePointer(a)) = f.request
		shareFakeAddRef(f.request)
		return shareOK
	case shareFakeDeferral:
		f.done = true
		return shareOK
	}
	return shareNoInterface
}

func shareFakeSlot7(this, a, _ uintptr) uintptr {
	f := shareFakeAt(this)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.role == shareFakeRequest {
		f.setData = a
		return shareOK
	}
	return shareNoInterface
}

func shareFakeSlot10(this, a, _ uintptr) uintptr {
	f := shareFakeAt(this)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.role == shareFakeRequest {
		if a == 0 {
			return sharePointer
		}
		*(*uintptr)(shareUnsafePointer(a)) = f.deferral
		shareFakeAddRef(f.deferral)
		return shareOK
	}
	return shareNoInterface
}

func shareFakeSlot9(this, _, _ uintptr) uintptr {
	f := shareFakeAt(this)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.role == shareFakeRequest {
		f.failed = true
		return shareOK
	}
	return shareNoInterface
}

func shareFakeSlot23(this, a, _ uintptr) uintptr {
	f := shareFakeAt(this)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.role == shareFakePackage {
		f.setItem = a
		return shareOK
	}
	return shareNoInterface
}

func shareFakeUnsupported(this, _, _ uintptr) uintptr { return shareNoInterface }

// shareRequestSetup prepares a payload, an args object, a request and a
// deferral, and puts the payload in the sheet for one test.
type shareRequestSetup struct {
	pkg      *sharePackage
	pkgFake  uintptr
	args     uintptr
	request  uintptr
	deferral uintptr
}

func shareNewRequestSetup(t *testing.T, hasText bool) shareRequestSetup {
	t.Helper()
	pkgFake := newShareFake(shareFakePackage)
	args := newShareFake(shareFakeArgs)
	request := newShareFake(shareFakeRequest)
	deferral := newShareFake(shareFakeDeferral)
	shareFakeAt(args).request = request
	shareFakeAt(request).deferral = deferral

	pkg := &sharePackage{pkg: pkgFake, paths: []string{`\\server\share\slow.txt`}, hasText: hasText}
	previous := shareSheet.payload.Swap(pkg)
	t.Cleanup(func() {
		shareSheet.payload.Store(previous)
		shareTestFakes = nil
	})
	return shareRequestSetup{pkg: pkg, pkgFake: pkgFake, args: args, request: request, deferral: deferral}
}

// TestShareRequestDoesNotWaitForFiles checks that answering the panel returns
// before the storage items are opened, and that the data and the deferral are
// settled once they arrive. The old code opened the files on the main thread
// inside the call, so a slow path froze every window until it timed out.
func TestShareRequestDoesNotWaitForFiles(t *testing.T) {
	release := make(chan struct{})
	resolverStarted := make(chan struct{})
	finished := make(chan struct{})
	oldResolve := shareResolveItemsFn
	shareResolveItemsFn = func(paths []string) ([]uintptr, error) {
		close(resolverStarted)
		<-release
		item := newShareFake(shareFakeItem)
		return []uintptr{item}, nil
	}
	oldWake := shareWakeMain
	shareWakeMain = func(fn func()) { fn(); close(finished) }
	t.Cleanup(func() { shareResolveItemsFn = oldResolve; shareWakeMain = oldWake })

	setup := shareNewRequestSetup(t, false)
	returned := make(chan uintptr, 1)
	go func() { returned <- shareDelegateInvoke(0, 0, setup.args) }()
	select {
	case hr := <-returned:
		if hr != shareOK {
			t.Fatalf("DataRequested = %#x, want S_OK", uint32(hr))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DataRequested did not return before the files were opened")
	}
	select {
	case <-resolverStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the worker never started")
	}

	close(release)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never completed")
	}

	request := shareFakeAt(setup.request)
	request.mu.Lock()
	setData, failed := request.setData, request.failed
	request.mu.Unlock()
	if failed {
		t.Fatal("the request reported a failure")
	}
	if setData != setup.pkgFake {
		t.Fatalf("SetData got %#x, want the package", setData)
	}
	if shareFakeAt(setup.pkgFake).setItem == 0 {
		t.Fatal("SetStorageItems was not called with the resolved item")
	}
	if !shareFakeAt(setup.deferral).done {
		t.Fatal("the deferral was never completed")
	}
	if n := atomic.LoadInt32(&request.refs); n != 1 {
		t.Fatalf("request refs = %d, want 1 (the test's own)", n)
	}
	if n := atomic.LoadInt32(&shareFakeAt(setup.pkgFake).refs); n != 1 {
		t.Fatalf("package refs = %d, want 1 (the test's own)", n)
	}
}

// TestShareRequestReportsFilesItCannotOpen checks that a request whose files
// all fail still completes the deferral, and says so in the panel instead of
// leaving it spinning.
func TestShareRequestReportsFilesItCannotOpen(t *testing.T) {
	finished := make(chan struct{})
	oldResolve := shareResolveItemsFn
	shareResolveItemsFn = func([]string) ([]uintptr, error) {
		return nil, errors.New("the share is offline")
	}
	oldWake := shareWakeMain
	shareWakeMain = func(fn func()) { fn(); close(finished) }
	t.Cleanup(func() { shareResolveItemsFn = oldResolve; shareWakeMain = oldWake })

	setup := shareNewRequestSetup(t, false)
	if hr := shareDelegateInvoke(0, 0, setup.args); hr != shareOK {
		t.Fatalf("DataRequested = %#x, want S_OK", uint32(hr))
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never completed")
	}
	request := shareFakeAt(setup.request)
	request.mu.Lock()
	failed := request.failed
	request.mu.Unlock()
	if !failed {
		t.Fatal("the panel was not told the files could not be opened")
	}
	if !shareFakeAt(setup.deferral).done {
		t.Fatal("the deferral was never completed")
	}
}

// TestShareRequestKeepsTextWhenFilesFail checks the text-only fallback: the
// text still goes over when none of the files could be opened.
func TestShareRequestKeepsTextWhenFilesFail(t *testing.T) {
	finished := make(chan struct{})
	oldResolve := shareResolveItemsFn
	shareResolveItemsFn = func([]string) ([]uintptr, error) {
		return nil, errors.New("the share is offline")
	}
	oldWake := shareWakeMain
	shareWakeMain = func(fn func()) { fn(); close(finished) }
	t.Cleanup(func() { shareResolveItemsFn = oldResolve; shareWakeMain = oldWake })

	setup := shareNewRequestSetup(t, true)
	if hr := shareDelegateInvoke(0, 0, setup.args); hr != shareOK {
		t.Fatalf("DataRequested = %#x, want S_OK", uint32(hr))
	}
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("the request never completed")
	}
	request := shareFakeAt(setup.request)
	request.mu.Lock()
	setData, failed := request.setData, request.failed
	request.mu.Unlock()
	if failed {
		t.Fatal("the text-only request reported a failure")
	}
	if setData != setup.pkgFake {
		t.Fatalf("SetData got %#x, want the package with the text", setData)
	}
	if !shareFakeAt(setup.deferral).done {
		t.Fatal("the deferral was never completed")
	}
}
