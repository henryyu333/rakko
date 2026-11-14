//go:build windows

package platform

// Sharing on Windows. The system Share panel is the counterpart of the Mac
// share sheet: it lists the installed share targets and includes Nearby
// Sharing, the AirDrop replacement. The WinRT ABI is used directly because
// the project takes no WinRT projection: the manager is fetched for an HWND
// through IDataTransferManagerInterop, a DataRequested handler hands it a
// prepared DataPackage, and ShowShareUIForWindow opens the panel.
//
// The package is complete before the panel opens. The texts share the one text
// field the panel offers, joined with newlines; every path is resolved to an
// IStorageItem (a StorageFile, or a StorageFolder for a directory) through
// GetFileFromPathAsync / GetFolderFromPathAsync on a worker thread, added to
// the package as an IIterable<IStorageItem> implemented here, and only then
// does the panel open. The handler therefore answers immediately, inside the
// request's short deadline, and the main thread never waits for a file.

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// HRESULTs this file returns on the ABI boundaries.
const (
	shareOK              = 0
	shareNoInterface     = 0x80004002
	sharePointer         = 0x80004003
	shareFail            = 0x80004005
	shareBounds          = 0x8000000B
	shareInvalidArgument = 0x80070057
)

// Vtable slots. WinRT interfaces start after IInspectable's six (the three
// IUnknown methods, GetIids, GetRuntimeClassName, GetTrustLevel).
const (
	shareMethodQueryInterface = 0
	shareMethodAddRef         = 1
	shareMethodRelease        = 2

	shareInteropGetForWindow = 3
	shareInteropShowUI       = 4

	shareManagerDataRequested = 6

	shareRequestData         = 6
	shareRequestSetData      = 7
	shareRequestFailWithText = 9

	sharePackageProperties      = 7
	sharePackageSetText         = 16
	sharePackageSetStorageItems = 23

	sharePropertiesSetTitle = 7

	shareStorageFromPath = 6

	shareAsyncGetResults = 8

	shareAsyncInfoStatus    = 7
	shareAsyncInfoErrorCode = 8
	shareAsyncInfoCancel    = 9
)

// RPC_E_CHANGED_MODE: the thread already holds an apartment of the other
// model. The worker can use it, but must not uninitialize it.
const shareRPCChangedMode = 0x80010106

// AsyncStatus values, the result of IAsyncInfo::get_Status.
const (
	shareAsyncStarted   = 0
	shareAsyncCompleted = 1
	shareAsyncCanceled  = 2
	shareAsyncError     = 3
)

// The interfaces this file activates or implements. The plain IIDs are the
// ones the Windows SDK declares. The collection interfaces are parameterized,
// so what belongs here is the IID of the instantiation, not the PIID of the
// generic definition: WinRT builds it as an RFC 4122 version 5 UUID over the
// PIID and the argument signatures ("Guid generation for parameterized types"
// in the WinRT type system documentation). IIterable<IStorageItem> and
// IIterator<IStorageItem> resolve to:
//
//	pinterface({faa585ea-6214-4217-afda-7f46de5869b3};{4207a996-ca2f-42f7-bde8-8b10457a7f30})
//	pinterface({6a79e863-4300-459a-9966-cbb660963ee1};{4207a996-ca2f-42f7-bde8-8b10457a7f30})
//
// The DataRequested handler is a TypedEventHandler<DataTransferManager,
// DataRequestedEventArgs>; its IID is built the same way from the delegate's
// PIID and the runtime-class signatures of its two arguments.
var (
	shareIIDUnknown     = shareGUID("00000000-0000-0000-c000-000000000046")
	shareIIDInspectable = shareGUID("af86e2e0-b12d-4c6a-9c5a-d7aa65101e90")
	shareIIDAgileObject = shareGUID("94ea2b94-e9cc-49e0-c0ff-ee64ca8f5b90")

	shareIIDIterableStorageItem  = shareGUID("bb8b8418-65d1-544b-b083-6d172f568c73")
	shareIIDIteratorStorageItem  = shareGUID("05b487c2-3830-5d3c-98da-25fa11542dbd")
	shareIIDDataRequestedHandler = shareGUID("ec6f9cc8-46d0-5e0e-b4d2-7d7773ae37a0")

	shareIIDDataTransferManager        = shareGUID("a5caee9b-8708-49d1-8d36-67d25a8da00c")
	shareIIDDataTransferManagerInterop = shareGUID("3a3dcd6c-3eab-43dc-bcde-45671ce800c8")
	shareIIDDataPackage                = shareGUID("61ebf5c7-efea-4346-9554-981d7e198ffe")
	shareIIDStorageFileStatics         = shareGUID("5984c710-daf2-43c8-8bb4-a4d3eacfd03f")
	shareIIDStorageFolderStatics       = shareGUID("08f327ff-85d5-48b9-aee9-28511e339f9f")
	shareIIDStorageItem                = shareGUID("4207a996-ca2f-42f7-bde8-8b10457a7f30")
	shareIIDStorageFolder              = shareGUID("72d1cb78-b3ef-4f75-a80b-6fd9dae2944b")
	shareIIDAsyncInfo                  = shareGUID("00000036-0000-0000-c000-000000000046")
)

var (
	procRoGetActivationFactory = sysProc(combase, "RoGetActivationFactory")
	procRoActivateInstance     = sysProc(combase, "RoActivateInstance")
	procWindowsCreateString    = sysProc(combase, "WindowsCreateString")
	procWindowsDeleteString    = sysProc(combase, "WindowsDeleteString")
	procCoTaskMemAlloc         = sysProc(ole32, "CoTaskMemAlloc")
)

// shareIIDError records a malformed identifier instead of panicking: a bad
// constant must not take the whole app down during package init. ShareNearby
// reports it before touching the share service.
var shareIIDError error

func shareGUID(s string) windows.GUID {
	g, err := windows.GUIDFromString("{" + s + "}")
	if err != nil && shareIIDError == nil {
		shareIIDError = fmt.Errorf("platform: bad share identifier %s", s)
	}
	return g
}

func shareFailed(hr uintptr) bool { return int32(uint32(hr)) < 0 }

// shareError describes a failed ABI call in plain language, with the code
// so a log keeps the detail.
func shareError(what string, hr uintptr) error {
	if hr == 0 || hr == shareFail {
		return fmt.Errorf("windows: %s failed", what)
	}
	return fmt.Errorf("windows: %s failed (0x%08x)", what, uint32(hr))
}

// shareUnsafePointer reinterprets a pointer-sized integer the ABI handed
// over. Reading the bits through a pointer keeps the conversion out of
// go vet's unsafeptr check, which objects to a bare uintptr conversion.
func shareUnsafePointer(p uintptr) unsafe.Pointer {
	return *(*unsafe.Pointer)(unsafe.Pointer(&p))
}

// shareComCall calls method index of the COM interface obj. The pointers
// converted to uintptr in args stay valid until the call returns.
//
//go:uintptrescapes
func shareComCall(obj uintptr, index int, args ...uintptr) uintptr {
	vtable := *(*uintptr)(shareUnsafePointer(obj))
	base := *(*unsafe.Pointer)(unsafe.Pointer(&vtable))
	fn := *(*uintptr)(unsafe.Add(base, index*int(unsafe.Sizeof(uintptr(0)))))
	var call [8]uintptr
	call[0] = obj
	n := copy(call[1:], args)
	r, _, _ := syscall.SyscallN(fn, call[:n+1]...)
	return r
}

func shareComRelease(obj uintptr) {
	if obj != 0 {
		shareComCall(obj, shareMethodRelease)
	}
}

// shareQueryInterface returns the queried pointer (0 when absent) and the
// HRESULT.
func shareQueryInterface(obj uintptr, iid windows.GUID) (uintptr, uintptr) {
	var out uintptr
	hr := shareComCall(obj, shareMethodQueryInterface,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out)))
	if shareFailed(hr) {
		return 0, hr
	}
	return out, hr
}

// shareGetter calls a getter that returns one interface pointer.
func shareGetter(obj uintptr, method int) (uintptr, uintptr) {
	var out uintptr
	hr := shareComCall(obj, method, uintptr(unsafe.Pointer(&out)))
	if shareFailed(hr) {
		return 0, hr
	}
	return out, hr
}

// shareNewString makes an HSTRING the ABI calls take. The caller frees it
// with shareFreeString.
func shareNewString(s string) (uintptr, error) {
	utf16 := windows.StringToUTF16(s)
	var h uintptr
	hr, _, _ := procWindowsCreateString.Call(
		uintptr(unsafe.Pointer(&utf16[0])), uintptr(len(utf16)-1), uintptr(unsafe.Pointer(&h)))
	if shareFailed(hr) {
		return 0, shareError("the share text", hr)
	}
	return h, nil
}

func shareFreeString(h uintptr) {
	if h != 0 {
		procWindowsDeleteString.Call(h)
	}
}

// shareActivateInstance creates a WinRT runtime class instance.
func shareActivateInstance(class string) (uintptr, error) {
	h, err := shareNewString(class)
	if err != nil {
		return 0, err
	}
	var out uintptr
	hr, _, _ := procRoActivateInstance.Call(h, uintptr(unsafe.Pointer(&out)))
	shareFreeString(h)
	if shareFailed(hr) || out == 0 {
		return 0, shareError("opening "+class, hr)
	}
	return out, nil
}

// shareActivationFactory asks for one interface on a class's activation
// factory.
func shareActivationFactory(class string, iid windows.GUID) (uintptr, error) {
	h, err := shareNewString(class)
	if err != nil {
		return 0, err
	}
	var out uintptr
	hr, _, _ := procRoGetActivationFactory.Call(h,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&out)))
	shareFreeString(h)
	if shareFailed(hr) || out == 0 {
		return 0, shareError("reaching "+class, hr)
	}
	return out, nil
}

// --- the share session ------------------------------------------------------

// sharePackage is the DataPackage one ShareNearby call built, kept alive
// while the system may still read it. The handler in shareDelegate hands it
// to the panel, and the panel owns a reference of its own after that. Files
// are opened before the panel opens, so the handler always has its data ready
// inside the request's short deadline; failure carries the message the panel
// shows when that preparation produced nothing shareable.
type sharePackage struct {
	pkg     uintptr // IDataPackage
	hasText bool    // the package already carries text
	failure string  // shown by the panel when no data could be prepared
}

func (p *sharePackage) addRef() { shareComCall(p.pkg, shareMethodAddRef) }

func (p *sharePackage) release() {
	if p != nil && p.pkg != 0 {
		shareComRelease(p.pkg)
	}
}

// shareManager is the DataTransferManager of one window. The manager is
// per-window, and registering its handler once keeps a second share from
// stacking another handler on it.
type shareManager struct {
	hwnd    uintptr
	manager uintptr // IDataTransferManager
}

// shareSheetState is the process-wide share state. ShareNearby is a main
// thread call, as is the DataRequested event the handler receives, so the
// state needs no lock. Storage items are resolved on a worker thread that
// holds its own activation factories.
type shareSheetState struct {
	interop       uintptr // IDataTransferManagerInterop
	hwnd          uintptr // the window the panel anchors to
	managerList   []shareManager
	payload       atomic.Pointer[sharePackage] // set before the panel opens
	pending       *sharePackage                // files still being prepared
	hiddenWindow  uintptr
	hiddenWindowE error
	hiddenTried   bool
}

var shareSheet shareSheetState

// shareWakeMain posts the completion of a file request to the main thread.
// A test swaps it out when there is no UI loop to pump.
var shareWakeMain = WakeMain

// shareHandler is the DataRequested delegate. Its reference count starts at
// one for this file and is never removed from the manager: the manager lives
// as long as the process, and re-registering on every share would multiply
// handlers.
var (
	shareSheetHandlerOnce sync.Once
	shareSheetHandler     *shareDelegate
)

// ShareNearby opens the system Share panel for files, folders and texts.
// Empty input is an error, as are a missing share service and unusable
// items. Texts share the panel's text field, joined with newlines. Paths are
// opened on a worker thread first and the panel opens as soon as the items
// are ready, so the DataRequested handler can answer inside the request's
// deadline even when a path sits on a slow disk or a network share; the call
// itself returns at once in that case and reports nothing further.
// The panel is system-owned: delivery happens (or not) after it opens.
// Main thread only.
func ShareNearby(paths []string, texts []string) error {
	if len(paths) == 0 && len(texts) == 0 {
		return errors.New("windows: the share panel needs at least one file or text")
	}
	if shareIIDError != nil {
		return shareIIDError
	}
	if err := Init(); err != nil {
		return err
	}

	hwnd, err := shareWindow()
	if err != nil {
		return err
	}
	interop, err := shareManagerFor(hwnd)
	if err != nil {
		return err
	}
	shareSheet.hwnd, shareSheet.interop = hwnd, interop
	// The manager's handler is registered by the time the package exists, so
	// the DataRequested event the panel raises can always answer it.
	pkg, err := shareNewPackage(texts)
	if err != nil {
		return err
	}

	if len(paths) == 0 {
		// Texts are ready now: clear any preparation still in flight so its
		// completion cannot reopen the panel over this share.
		showErr := shareShowNow(pkg)
		pkg.release()
		return showErr
	}
	// The preparation owns this reference and releases it when it is done.
	shareSheet.pending = pkg
	shareStartPrepare(pkg, append([]string(nil), paths...))
	return nil
}

// shareShowNow opens the panel for a package that is ready and supersedes any
// share whose files are still being prepared.
func shareShowNow(pkg *sharePackage) error {
	shareSheet.pending = nil
	return shareShowPanel(pkg)
}

// shareOpenPanel hands pkg to the system panel. The payload keeps its own
// reference; the previous package is released once the panel has taken this
// one.
func shareOpenPanel(pkg *sharePackage) error {
	pkg.addRef()
	previous := shareSheet.payload.Swap(pkg)
	if hr := shareComCall(shareSheet.interop, shareInteropShowUI, shareSheet.hwnd); shareFailed(hr) {
		shareSheet.payload.Store(previous)
		pkg.release()
		return shareError("showing the share panel", hr)
	}
	previous.release()
	return nil
}

// shareWindow answers an HWND this process owns: the foreground window when
// it is ours, else our first visible top-level window, else a small hidden
// window made for the panel.
func shareWindow() (uintptr, error) {
	if fg, _, _ := procGetForegroundWindow.Call(); fg != 0 && windowPID(fg) == ownProcessID() {
		return fg, nil
	}
	var own uintptr
	enumTopLevel(func(h uintptr) bool {
		if r, _, _ := procIsWindowVisible.Call(h); r == 0 || windowPID(h) != ownProcessID() {
			return true
		}
		own = h
		return false
	})
	if own != 0 {
		return own, nil
	}
	if shareSheet.hiddenTried {
		if shareSheet.hiddenWindow == 0 {
			return 0, shareSheet.hiddenWindowE
		}
		return shareSheet.hiddenWindow, nil
	}
	shareSheet.hiddenTried = true
	class, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		shareSheet.hiddenWindowE = err
		return 0, err
	}
	title, err := windows.UTF16PtrFromString("Rakko")
	if err != nil {
		shareSheet.hiddenWindowE = err
		return 0, err
	}
	const wsPopup = 0x80000000
	h, _, callErr := procCreateWindowEx.Call(0,
		uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)), wsPopup,
		0, 0, 1, 1, 0, 0, 0, 0)
	if h == 0 {
		shareSheet.hiddenWindowE = fmt.Errorf("windows: could not create a window for the share panel: %v", callErr)
		return 0, shareSheet.hiddenWindowE
	}
	shareSheet.hiddenWindow = h
	return h, nil
}

// shareManagerFor answers the DataTransferManager of hwnd, registering the
// DataRequested handler the first time the window is used.
func shareManagerFor(hwnd uintptr) (uintptr, error) {
	interop := shareSheet.interop
	if interop == 0 {
		factory, err := shareActivationFactory(
			"Windows.ApplicationModel.DataTransfer.DataTransferManager",
			shareIIDDataTransferManagerInterop)
		if err != nil {
			return 0, err
		}
		shareSheet.interop, interop = factory, factory
	}
	for _, m := range shareSheet.managerList {
		if m.hwnd == hwnd {
			return interop, nil
		}
	}

	iid := shareIIDDataTransferManager
	var manager uintptr
	hr := shareComCall(interop, shareInteropGetForWindow, hwnd,
		uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&manager)))
	if shareFailed(hr) || manager == 0 {
		return 0, shareError("reaching the share panel for this window", hr)
	}

	var token int64
	hr = shareComCall(manager, shareManagerDataRequested,
		uintptr(unsafe.Pointer(shareHandlerDelegate())), uintptr(unsafe.Pointer(&token)))
	if shareFailed(hr) {
		shareComRelease(manager)
		return 0, shareError("preparing the share panel", hr)
	}
	shareSheet.managerList = append(shareSheet.managerList, shareManager{hwnd: hwnd, manager: manager})
	return interop, nil
}

// shareNewPackage creates the DataPackage with the title and the joined
// texts. Files are added once they are prepared, before the panel opens, so
// the call never touches the file system. The returned package is owned by
// the caller.
func shareNewPackage(texts []string) (*sharePackage, error) {
	instance, err := shareActivateInstance("Windows.ApplicationModel.DataTransfer.DataPackage")
	if err != nil {
		return nil, err
	}
	pkg, hr := shareQueryInterface(instance, shareIIDDataPackage)
	shareComRelease(instance)
	if pkg == 0 {
		return nil, shareError("creating the share data", hr)
	}
	done := false
	defer func() {
		if !done {
			shareComRelease(pkg)
		}
	}()

	properties, hr := shareGetter(pkg, sharePackageProperties)
	if properties == 0 {
		return nil, shareError("preparing the share data", hr)
	}
	title, err := shareNewString("Rakko")
	if err == nil {
		hr = shareComCall(properties, sharePropertiesSetTitle, title)
		shareFreeString(title)
	}
	shareComRelease(properties)
	if err != nil {
		return nil, err
	}
	if shareFailed(hr) {
		return nil, shareError("preparing the share data", hr)
	}

	if len(texts) > 0 {
		text, err := shareNewString(strings.Join(texts, "\n"))
		if err != nil {
			return nil, err
		}
		hr = shareComCall(pkg, sharePackageSetText, text)
		shareFreeString(text)
		if shareFailed(hr) {
			return nil, shareError("adding the text to the share panel", hr)
		}
	}

	done = true
	return &sharePackage{pkg: pkg, hasText: len(texts) > 0}, nil
}

// shareAddFiles hands resolved items to the package. The package keeps its own
// reference to each item it accepts, and the collection takes its own as well;
// the caller still owns the references in the slice and releases them.
func shareAddFiles(pkg uintptr, items []uintptr) error {
	collection := shareNewItems(items)
	hr := shareComCall(pkg, sharePackageSetStorageItems,
		uintptr(unsafe.Pointer(collection)), 0)
	// The package keeps its own reference when it accepted the items;
	// dropping ours now lets the collection go when it is done.
	shareComRelease(uintptr(unsafe.Pointer(collection)))
	if shareFailed(hr) {
		return shareError("adding the files to the share panel", hr)
	}
	return nil
}

// shareStorageItem resolves one path to its IStorageItem: a StorageFolder for
// a directory, a StorageFile otherwise. The activation factories belong to the
// caller and are created on first use, so a worker thread can resolve items
// without touching the main thread's state.
func shareStorageItem(fileFactory, folderFactory *uintptr, path string) (uintptr, error) {
	class := "Windows.Storage.StorageFile"
	iid := shareIIDStorageFileStatics
	cached := fileFactory
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		class = "Windows.Storage.StorageFolder"
		iid = shareIIDStorageFolderStatics
		cached = folderFactory
	}
	statics := *cached
	if statics == 0 {
		factory, err := shareActivationFactory(class, iid)
		if err != nil {
			return 0, err
		}
		*cached, statics = factory, factory
	}
	hpath, err := shareNewString(path)
	if err != nil {
		return 0, err
	}
	var operation uintptr
	hr := shareComCall(statics, shareStorageFromPath, hpath, uintptr(unsafe.Pointer(&operation)))
	shareFreeString(hpath)
	if shareFailed(hr) || operation == 0 {
		return 0, shareError("opening "+path+" for sharing", hr)
	}
	defer shareComRelease(operation)
	if err := shareAwait(operation); err != nil {
		return 0, fmt.Errorf("windows: could not open %s for sharing: %w", path, err)
	}
	var file uintptr
	hr = shareComCall(operation, shareAsyncGetResults, uintptr(unsafe.Pointer(&file)))
	if shareFailed(hr) || file == 0 {
		return 0, shareError("opening "+path+" for sharing", hr)
	}
	defer shareComRelease(file)
	item, hr := shareQueryInterface(file, shareIIDStorageItem)
	if item == 0 {
		return 0, shareError("reading "+path+" for sharing", hr)
	}
	return item, nil
}

// shareResolveItemsFn opens the paths of one request. A test replaces it to
// control when the worker finishes.
var shareResolveItemsFn = shareResolveItems

// shareResolveItems opens every path on the calling thread, skipping the ones
// the shell cannot open. Storage items are agile, so the caller may hand them
// to the main thread afterwards; the caller owns every returned reference.
func shareResolveItems(paths []string) ([]uintptr, error) {
	var fileFactory, folderFactory uintptr
	defer func() { shareComRelease(fileFactory) }()
	defer func() { shareComRelease(folderFactory) }()
	items := make([]uintptr, 0, len(paths))
	var firstErr error
	for _, path := range paths {
		item, err := shareStorageItem(&fileFactory, &folderFactory, path)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		items = append(items, item)
	}
	return items, firstErr
}

// shareAwait waits for the IAsyncOperation from a Storage* static call to
// finish by polling IAsyncInfo, so no completion delegate and no message pump
// are needed. The file operations the runtime starts are free-threaded, so
// polling from the worker thread can make progress.
func shareAwait(operation uintptr) error {
	info, hr := shareQueryInterface(operation, shareIIDAsyncInfo)
	if info == 0 {
		return shareError("the file operation", hr)
	}
	defer shareComRelease(info)
	deadline := time.Now().Add(15 * time.Second)
	for {
		var status int32
		if hr := shareComCall(info, shareAsyncInfoStatus, uintptr(unsafe.Pointer(&status))); shareFailed(hr) {
			return errors.New("the file operation failed")
		}
		switch status {
		case shareAsyncStarted:
			if time.Now().After(deadline) {
				shareComCall(info, shareAsyncInfoCancel)
				return errors.New("the file took too long to open")
			}
			time.Sleep(2 * time.Millisecond)
		case shareAsyncCompleted:
			return nil
		case shareAsyncCanceled:
			return errors.New("the file operation was canceled")
		case shareAsyncError:
			var code uintptr
			shareComCall(info, shareAsyncInfoErrorCode, uintptr(unsafe.Pointer(&code)))
			if shareFailed(code) {
				return shareError("the file operation", code)
			}
			return errors.New("the file operation failed")
		default:
			return errors.New("the file operation failed")
		}
	}
}

// --- the DataRequested delegate --------------------------------------------

// shareDelegate is the TypedEventHandler<DataTransferManager,
// DataRequestedEventArgs> the manager calls when the panel wants data. It is
// a plain COM object: IUnknown only, with an agile identity so the runtime may
// deliver the event off the main thread.
type shareDelegate struct {
	vtable *[4]uintptr
	refs   int32
}

var shareDelegateVtable = [4]uintptr{
	windows.NewCallback(shareDelegateQueryInterface),
	windows.NewCallback(shareDelegateAddRef),
	windows.NewCallback(shareDelegateRelease),
	windows.NewCallback(shareDelegateInvoke),
}

func shareHandlerDelegate() *shareDelegate {
	shareSheetHandlerOnce.Do(func() {
		shareSheetHandler = &shareDelegate{vtable: &shareDelegateVtable, refs: 1}
	})
	return shareSheetHandler
}

func shareDelegateAt(this uintptr) *shareDelegate {
	return (*shareDelegate)(shareUnsafePointer(this))
}

func shareDelegateQueryInterface(this, iid, out uintptr) uintptr {
	if iid == 0 || out == 0 {
		return sharePointer
	}
	id := *(*windows.GUID)(shareUnsafePointer(iid))
	if id == shareIIDUnknown || id == shareIIDAgileObject ||
		id == shareIIDDataRequestedHandler {
		*(*uintptr)(shareUnsafePointer(out)) = this
		atomic.AddInt32(&shareDelegateAt(this).refs, 1)
		return shareOK
	}
	*(*uintptr)(shareUnsafePointer(out)) = 0
	return shareNoInterface
}

func shareDelegateAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&shareDelegateAt(this).refs, 1))
}

func shareDelegateRelease(this uintptr) uintptr {
	// The handler is registered for the process lifetime, so the count never
	// reaches zero; the guard keeps a stray release from underflowing.
	if n := atomic.AddInt32(&shareDelegateAt(this).refs, -1); n > 0 {
		return uintptr(n)
	}
	return 0
}

// shareDelegateInvoke answers the panel's data request. The package is fully
// prepared (texts and storage items) before the panel opens, so setting the
// data here is immediate and stays inside the request's deadline.
// FailWithDisplayText is the only channel left once ShareNearby has returned,
// so a package that could not be prepared reports there.
func shareDelegateInvoke(this, sender, args uintptr) uintptr {
	if args == 0 {
		return sharePointer
	}
	request, hr := shareGetter(args, shareRequestData)
	if request == 0 {
		return hr
	}
	defer shareComRelease(request)

	payload := shareSheet.payload.Load()
	if payload == nil || payload.pkg == 0 {
		shareFailRequest(request, "Rakko has nothing to share")
		return shareOK
	}
	if payload.failure != "" {
		shareFailRequest(request, payload.failure)
		return shareOK
	}
	if hr := shareComCall(request, shareRequestSetData, payload.pkg); shareFailed(hr) {
		shareFailRequest(request, "Rakko could not prepare these items for sharing")
	}
	return shareOK
}

// shareShowPanel is the last step of a share. A test replaces it to observe
// the completion without a real panel.
var shareShowPanel = shareOpenPanel

// shareStartPrepare opens paths on a worker thread and, once they are ready,
// opens the panel for pkg on the main thread. The goroutine owns the
// caller's package reference until the completion runs.
func shareStartPrepare(pkg *sharePackage, paths []string) {
	go func() {
		// WinRT objects are apartment-bound: the worker needs its own
		// apartment, and it must stay on the thread that made one and give
		// the apartment back before the thread returns to the pool.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
		switch {
		case int32(uint32(hr)) == 0, int32(uint32(hr)) == 1:
			// S_OK, S_FALSE: this thread now holds the apartment.
			defer procCoUninitialize.Call()
		case uint32(hr) == shareRPCChangedMode:
			// The thread already held another apartment (a host that made it
			// an STA, for example). Use it and leave it alone.
		default:
			shareWakeMain(func() {
				sharePrepared(pkg, nil,
					fmt.Errorf("windows: the share background thread could not initialize COM (0x%08x)", uint32(hr)))
			})
			return
		}
		items, err := shareResolveItemsFn(paths)
		shareWakeMain(func() { sharePrepared(pkg, items, err) })
	}()
}

// sharePrepared finishes a share on the main thread: the resolved items join
// the package, then the panel opens. It owns the item references from the
// worker and the package reference taken in ShareNearby.
func sharePrepared(pkg *sharePackage, items []uintptr, resolveErr error) {
	defer shareReleaseItems(items)
	defer pkg.release()
	if shareSheet.pending != pkg {
		// A newer share replaced this one while the files were opening.
		return
	}
	shareSheet.pending = nil

	switch {
	case len(items) > 0:
		if err := shareAddFiles(pkg.pkg, items); err != nil {
			pkg.failure = "Rakko could not add these items to the share panel"
		}
	case pkg.hasText:
		// The texts still go over; the files that would not open are left
		// out, as they were when the shell refused one item of a selection.
	default:
		pkg.failure = "Rakko could not open these items for sharing"
		if resolveErr != nil {
			pkg.failure = "Rakko could not open these items for sharing: " + resolveErr.Error()
		}
	}
	if err := shareShowPanel(pkg); err != nil {
		// The anchor window may have gone away while the files were opening;
		// the caller is long back in its own loop, so the failure goes to the
		// app's error notice instead.
		reportError(err)
	}
}

// shareReleaseItems drops the references shareResolveItems handed over.
func shareReleaseItems(items []uintptr) {
	for _, item := range items {
		shareComRelease(item)
	}
}

func shareFailRequest(request uintptr, message string) {
	h, err := shareNewString(message)
	if err != nil {
		return
	}
	defer shareFreeString(h)
	shareComCall(request, shareRequestFailWithText, h)
}

// --- IIterable<IStorageItem> ------------------------------------------------

// shareItems is the IIterable<IStorageItem> handed to DataPackage.
// SetStorageItems. Its first word is the vtable pointer, so the address of
// the struct is the COM interface pointer. The items are StorageFile and
// StorageFolder objects resolved by shareStorageItem; the collection takes one
// reference of its own to each, so the caller keeps the references it holds.
type shareItems struct {
	vtable *[7]uintptr
	items  []uintptr // IStorageItem, one reference each
	refs   int32
}

var shareItemsVtable = [7]uintptr{
	windows.NewCallback(shareItemsQueryInterface),
	windows.NewCallback(shareItemsAddRef),
	windows.NewCallback(shareItemsRelease),
	windows.NewCallback(shareItemsGetIids),
	windows.NewCallback(shareInspectableClass),
	windows.NewCallback(shareInspectableTrust),
	windows.NewCallback(shareItemsFirst),
}

// shareLiveObjects roots the objects COM holds by raw pointer. The Go
// collector does not see COM's references, so every live object stays in
// this map until its last Release.
var shareLiveObjects = struct {
	sync.Mutex
	objects map[uintptr]any
}{objects: make(map[uintptr]any)}

func shareRoot(obj uintptr, value any) {
	shareLiveObjects.Lock()
	shareLiveObjects.objects[obj] = value
	shareLiveObjects.Unlock()
}

func shareUnroot(obj uintptr) {
	shareLiveObjects.Lock()
	delete(shareLiveObjects.objects, obj)
	shareLiveObjects.Unlock()
}

func shareNewItems(items []uintptr) *shareItems {
	held := make([]uintptr, len(items))
	for i, item := range items {
		shareComCall(item, shareMethodAddRef)
		held[i] = item
	}
	collection := &shareItems{vtable: &shareItemsVtable, items: held, refs: 1}
	shareRoot(uintptr(unsafe.Pointer(collection)), collection)
	return collection
}

func shareItemsAt(this uintptr) *shareItems {
	return (*shareItems)(shareUnsafePointer(this))
}

func shareItemsQueryInterface(this, iid, out uintptr) uintptr {
	if iid == 0 || out == 0 {
		return sharePointer
	}
	id := *(*windows.GUID)(shareUnsafePointer(iid))
	if id == shareIIDUnknown || id == shareIIDInspectable ||
		id == shareIIDAgileObject || id == shareIIDIterableStorageItem {
		*(*uintptr)(shareUnsafePointer(out)) = this
		atomic.AddInt32(&shareItemsAt(this).refs, 1)
		return shareOK
	}
	*(*uintptr)(shareUnsafePointer(out)) = 0
	return shareNoInterface
}

func shareItemsAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&shareItemsAt(this).refs, 1))
}

func shareItemsRelease(this uintptr) uintptr {
	collection := shareItemsAt(this)
	n := atomic.AddInt32(&collection.refs, -1)
	if n > 0 {
		return uintptr(n)
	}
	if n < 0 { // a stray release must not free the items twice
		return 0
	}
	for _, item := range collection.items {
		shareComRelease(item)
	}
	collection.items = nil
	shareUnroot(this)
	return 0
}

func shareItemsFirst(this, out uintptr) uintptr {
	if out == 0 {
		return sharePointer
	}
	collection := shareItemsAt(this)
	iterator := &shareIterator{vtable: &shareIteratorVtable, owner: collection, refs: 1}
	atomic.AddInt32(&collection.refs, 1)
	shareRoot(uintptr(unsafe.Pointer(iterator)), iterator)
	*(*uintptr)(shareUnsafePointer(out)) = uintptr(unsafe.Pointer(iterator))
	return shareOK
}

// shareIterator walks the stored items. It matches IIterator<IStorageItem>,
// so the consumers of the collection read the same references.
type shareIterator struct {
	vtable *[10]uintptr
	owner  *shareItems
	index  int
	refs   int32
}

var shareIteratorVtable = [10]uintptr{
	windows.NewCallback(shareIteratorQueryInterface),
	windows.NewCallback(shareIteratorAddRef),
	windows.NewCallback(shareIteratorRelease),
	windows.NewCallback(shareIteratorGetIids),
	windows.NewCallback(shareInspectableClass),
	windows.NewCallback(shareInspectableTrust),
	windows.NewCallback(shareIteratorCurrent),
	windows.NewCallback(shareIteratorHasCurrent),
	windows.NewCallback(shareIteratorMoveNext),
	windows.NewCallback(shareIteratorGetMany),
}

func shareIteratorAt(this uintptr) *shareIterator {
	return (*shareIterator)(shareUnsafePointer(this))
}

func shareIteratorQueryInterface(this, iid, out uintptr) uintptr {
	if iid == 0 || out == 0 {
		return sharePointer
	}
	id := *(*windows.GUID)(shareUnsafePointer(iid))
	if id == shareIIDUnknown || id == shareIIDInspectable ||
		id == shareIIDAgileObject || id == shareIIDIteratorStorageItem {
		*(*uintptr)(shareUnsafePointer(out)) = this
		atomic.AddInt32(&shareIteratorAt(this).refs, 1)
		return shareOK
	}
	*(*uintptr)(shareUnsafePointer(out)) = 0
	return shareNoInterface
}

func shareIteratorAddRef(this uintptr) uintptr {
	return uintptr(atomic.AddInt32(&shareIteratorAt(this).refs, 1))
}

func shareIteratorRelease(this uintptr) uintptr {
	iterator := shareIteratorAt(this)
	n := atomic.AddInt32(&iterator.refs, -1)
	if n > 0 {
		return uintptr(n)
	}
	if n < 0 {
		return 0
	}
	shareComRelease(uintptr(unsafe.Pointer(iterator.owner)))
	iterator.owner = nil
	shareUnroot(this)
	return 0
}

// shareIteratorCurrent answers the item at the cursor, or E_BOUNDS past the
// end as IIterator documents.
func shareIteratorCurrent(this, out uintptr) uintptr {
	if out == 0 {
		return sharePointer
	}
	iterator := shareIteratorAt(this)
	if iterator.owner == nil || iterator.index >= len(iterator.owner.items) {
		*(*uintptr)(shareUnsafePointer(out)) = 0
		return shareBounds
	}
	item := iterator.owner.items[iterator.index]
	shareComCall(item, shareMethodAddRef)
	*(*uintptr)(shareUnsafePointer(out)) = item
	return shareOK
}

func shareIteratorHasCurrent(this, out uintptr) uintptr {
	if out == 0 {
		return sharePointer
	}
	iterator := shareIteratorAt(this)
	has := iterator.owner != nil && iterator.index < len(iterator.owner.items)
	if has {
		*(*uint8)(shareUnsafePointer(out)) = 1
	} else {
		*(*uint8)(shareUnsafePointer(out)) = 0
	}
	return shareOK
}

func shareIteratorMoveNext(this, out uintptr) uintptr {
	if out == 0 {
		return sharePointer
	}
	iterator := shareIteratorAt(this)
	if iterator.owner != nil && iterator.index < len(iterator.owner.items) {
		iterator.index++
	}
	has := iterator.owner != nil && iterator.index < len(iterator.owner.items)
	if has {
		*(*uint8)(shareUnsafePointer(out)) = 1
	} else {
		*(*uint8)(shareUnsafePointer(out)) = 0
	}
	return shareOK
}

func shareIteratorGetMany(this, capacity, items, actual uintptr) uintptr {
	if capacity == 0 || items == 0 || actual == 0 {
		return shareInvalidArgument
	}
	iterator := shareIteratorAt(this)
	want := int(capacity)
	var n int
	if iterator.owner != nil {
		remaining := len(iterator.owner.items) - iterator.index
		if want > remaining {
			want = remaining
		}
		if want < 0 {
			want = 0
		}
		for n = 0; n < want; n++ {
			item := iterator.owner.items[iterator.index+n]
			shareComCall(item, shareMethodAddRef)
			*(*uintptr)(unsafe.Add(shareUnsafePointer(items), n*int(unsafe.Sizeof(uintptr(0))))) = item
		}
		iterator.index += n
	}
	*(*uint32)(shareUnsafePointer(actual)) = uint32(n)
	return shareOK
}

// --- IInspectable's half of the vtable --------------------------------------

// shareGetIids answers IInspectable's interface list for an object that
// implements exactly one WinRT interface. The returned array is COM
// allocated, as IInspectable requires.
func shareGetIids(count, out uintptr, iid windows.GUID) uintptr {
	if count == 0 || out == 0 {
		return sharePointer
	}
	mem, _, _ := procCoTaskMemAlloc.Call(unsafe.Sizeof(iid))
	if mem == 0 {
		return shareFail
	}
	*(*windows.GUID)(shareUnsafePointer(mem)) = iid
	*(*uint32)(shareUnsafePointer(count)) = 1
	*(*uintptr)(shareUnsafePointer(out)) = mem
	return shareOK
}

func shareItemsGetIids(this, count, out uintptr) uintptr {
	return shareGetIids(count, out, shareIIDIterableStorageItem)
}

func shareIteratorGetIids(this, count, out uintptr) uintptr {
	return shareGetIids(count, out, shareIIDIteratorStorageItem)
}

// shareInspectableClass reports no runtime class name, which IInspectable
// allows with a null string.
func shareInspectableClass(this, out uintptr) uintptr {
	if out == 0 {
		return sharePointer
	}
	*(*uintptr)(shareUnsafePointer(out)) = 0
	return shareOK
}

func shareInspectableTrust(this, out uintptr) uintptr {
	if out == 0 {
		return sharePointer
	}
	*(*int32)(shareUnsafePointer(out)) = 0 // BaseTrust
	return shareOK
}
