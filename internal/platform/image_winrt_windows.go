//go:build windows

package platform

// The Windows Runtime and COM plumbing the image, OCR and PDF paths share.
// The calls are made through vtable slots: WinRT interfaces are IUnknown
// layouts, so a method is its slot number in the interface's vtable, which
// the comments name next to every constant (taken from the Windows SDK
// metadata). Only the members this package calls are used.
//
// Every entry point that touches COM runs on a locked OS thread inside an
// apartment (wiApartment): a WinRT object is bound to the apartment that
// created it, and a Go goroutine may otherwise resume on another thread.

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wiGUID mirrors the Windows GUID layout.
type wiGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// wiParseGUID builds a GUID from its canonical "8-4-4-4-12" text.
func wiParseGUID(s string) wiGUID {
	var g wiGUID
	var b [16]byte
	i := 0
	for k := range len(s) {
		c := s[k]
		if c == '-' {
			continue
		}
		var v byte
		switch {
		case c >= '0' && c <= '9':
			v = c - '0'
		case c >= 'a' && c <= 'f':
			v = c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v = c - 'A' + 10
		default:
			panic("platform: bad GUID " + s)
		}
		if i < len(b)*2 {
			b[i/2] = b[i/2]<<4 | v
		}
		i++
	}
	if i != 32 {
		panic("platform: bad GUID " + s)
	}
	g.Data1 = uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	g.Data2 = uint16(b[4])<<8 | uint16(b[5])
	g.Data3 = uint16(b[6])<<8 | uint16(b[7])
	copy(g.Data4[:], b[8:])
	return g
}

var (
	wiIIDUnknown     = wiParseGUID("00000000-0000-0000-c000-000000000046")
	wiIIDInspectable = wiParseGUID("af86e2e0-b12d-4c6a-9c5a-d7aa65101e90")
	// wiIIDAsyncInfo is the interface an async operation answers beside
	// itself; Status and ErrorCode live there, not in the operation's own
	// vtable.
	wiIIDAsyncInfo = wiParseGUID("00000036-0000-0000-c000-000000000046")
	// wiIIDAgileObject lets a local object be used from any apartment.
	wiIIDAgileObject = wiParseGUID("94ea2b94-e9cc-49e0-c0ff-ee64ca8f5b90")
)

// Failed reports whether a COM call returned an error code.
func wiFailed(hr uintptr) bool { return int32(hr) < 0 }

// wiErr turns a failed HRESULT into an error naming the call.
func wiErr(what string, hr uintptr) error {
	return fmt.Errorf("windows: %s failed (0x%08X)", what, uint32(hr))
}

// wiPtr views a COM handle or a raw address as a pointer. Handles are
// addresses returned in registers; the conversion is the usual unsafe
// round trip, kept here so `go vet` audits one place.
func wiPtr(u uintptr) unsafe.Pointer {
	if u == 0 {
		return nil
	}
	return *(*unsafe.Pointer)(unsafe.Pointer(&u))
}

// wiSlot returns the function pointer of method index in obj's vtable.
func wiSlot(obj uintptr, index int) uintptr {
	vtbl := *(*uintptr)(wiPtr(obj))
	return *(*uintptr)(unsafe.Add(wiPtr(vtbl), uintptr(index)*unsafe.Sizeof(uintptr(0))))
}

// wiInvoke calls obj's method index with args. Every vtable method takes
// the interface pointer first, so the arguments follow it.
func wiInvoke(obj uintptr, index int, args ...uintptr) uintptr {
	all := make([]uintptr, 0, len(args)+1)
	all = append(all, obj)
	all = append(all, args...)
	r, _, _ := syscall.SyscallN(wiSlot(obj, index), all...)
	return r
}

// wiAddRef and wiRelease are IUnknown::AddRef / Release.
func wiAddRef(obj uintptr) { wiInvoke(obj, 1) }

// wiRelease drops one reference to a COM object.
func wiRelease(obj uintptr) {
	if obj != 0 {
		wiInvoke(obj, 2)
	}
}

// wiQueryInterface calls IUnknown::QueryInterface.
func wiQueryInterface(obj uintptr, iid *wiGUID, out *uintptr) uintptr {
	return wiInvoke(obj, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(out)))
}

// The system libraries, resolved on first use.
var (
	wiCombase, wiOle32, wiUser32 lazyDLL
	wiOnce                       sync.Once
	wiProcs                      wiProcsT
)

type lazyDLL struct {
	dll  *windows.LazyDLL
	proc map[string]*windows.LazyProc
}

func (l *lazyDLL) p(name string) *windows.LazyProc {
	if l.proc == nil {
		l.proc = map[string]*windows.LazyProc{}
	}
	if p, ok := l.proc[name]; ok {
		return p
	}
	p := l.dll.NewProc(name)
	l.proc[name] = p
	return p
}

type wiProcsT struct {
	roInitialize, roUninitialize, roGetActivationFactory, roActivateInstance *windows.LazyProc
	windowsCreateString, windowsDeleteString, windowsGetStringRawBuffer      *windows.LazyProc
	coCreateInstance                                                         *windows.LazyProc
	createRandomAccessStreamOverStream                                       *windows.LazyProc
	shCreateStreamOnFileEx                                                   *windows.LazyProc
}

func wiLoad() {
	wiOnce.Do(func() {
		wiCombase.dll = windows.NewLazySystemDLL("combase.dll")
		wiOle32.dll = windows.NewLazySystemDLL("ole32.dll")
		wiUser32.dll = windows.NewLazySystemDLL("user32.dll")
		wiProcs = wiProcsT{
			roInitialize:           wiCombase.p("RoInitialize"),
			roUninitialize:         wiCombase.p("RoUninitialize"),
			roGetActivationFactory: wiCombase.p("RoGetActivationFactory"),
			roActivateInstance:     wiCombase.p("RoActivateInstance"),
			windowsCreateString:    wiCombase.p("WindowsCreateString"),
			windowsDeleteString:    wiCombase.p("WindowsDeleteString"),
			windowsGetStringRawBuffer: wiCombase.p(
				"WindowsGetStringRawBuffer"),
			coCreateInstance: wiOle32.p("CoCreateInstance"),
		}
	})
}

// RO_INIT_MULTITHREADED: the apartment the workers call WinRT from.
const wiROInitMultithreaded = 0

// wiApartment runs fn with COM initialized on a locked thread. Objects
// created inside and the calls on them therefore share one apartment.
func wiApartment(fn func() error) error {
	wiLoad()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := wiProcs.roInitialize.Call(wiROInitMultithreaded)
	switch {
	case !wiFailed(hr):
		defer wiProcs.roUninitialize.Call()
	case uint32(hr) == 0x80010106:
		// RPC_E_CHANGED_MODE: the thread is already in another apartment
		// (some other library initialized it as STA). In-process WinRT
		// calls still work there; do not uninitialize a count we do not own.
	default:
		return wiErr("RoInitialize", hr)
	}
	return fn()
}

// wiNewHString makes a Windows Runtime string; the caller deletes it with
// wiDeleteHString.
func wiNewHString(s string) (uintptr, error) {
	u := windows.StringToUTF16(s)
	var h uintptr
	hr, _, _ := wiProcs.windowsCreateString.Call(
		uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&h)))
	if wiFailed(hr) {
		return 0, wiErr("WindowsCreateString", hr)
	}
	return h, nil
}

func wiDeleteHString(h uintptr) {
	if h != 0 {
		wiProcs.windowsDeleteString.Call(h)
	}
}

// wiHString converts a WinRT string to Go text.
func wiHString(h uintptr) string {
	if h == 0 {
		return ""
	}
	var n uint32
	p, _, _ := wiProcs.windowsGetStringRawBuffer.Call(h, uintptr(unsafe.Pointer(&n)))
	if p == 0 || n == 0 {
		return ""
	}
	return windows.UTF16ToString(unsafe.Slice((*uint16)(wiPtr(p)), n))
}

// wiClassFactory returns the statics of a WinRT class as iid, which the
// caller releases.
func wiClassFactory(class string, iid wiGUID) (uintptr, error) {
	h, err := wiNewHString(class)
	if err != nil {
		return 0, err
	}
	defer wiDeleteHString(h)
	var f uintptr
	hr, _, _ := wiProcs.roGetActivationFactory.Call(
		h, uintptr(unsafe.Pointer(&iid)), uintptr(unsafe.Pointer(&f)))
	if wiFailed(hr) {
		return 0, wiErr("RoGetActivationFactory("+class+")", hr)
	}
	return f, nil
}

// wiActivate creates an instance of an activatable WinRT class, as its
// IInspectable. The caller releases it.
func wiActivate(class string) (uintptr, error) {
	h, err := wiNewHString(class)
	if err != nil {
		return 0, err
	}
	defer wiDeleteHString(h)
	var obj uintptr
	hr, _, _ := wiProcs.roActivateInstance.Call(h, uintptr(unsafe.Pointer(&obj)))
	if wiFailed(hr) {
		return 0, wiErr("RoActivateInstance("+class+")", hr)
	}
	return obj, nil
}

// IAsyncInfo status values.
const (
	wiAsyncStarted   = 0
	wiAsyncCompleted = 1
	wiAsyncCanceled  = 2
	wiAsyncError     = 3
)

// IAsyncInfo vtable slots after IInspectable's six: Id, Status, ErrorCode,
// Cancel, Close.
const (
	wiAsyncInfoStatus = 7
	wiAsyncInfoError  = 8
)

// IAsyncOperation<T>'s own vtable after IInspectable's six: SetCompleted,
// Completed, GetResults.
const wiAsyncOperationResults = 8

// wiAwait waits for a WinRT asynchronous operation to finish without a
// completion handler: IAsyncInfo's status is polled until it leaves
// Started. Polling costs a couple of milliseconds per operation and needs
// no callback object, which a completion delegate would otherwise require.
func wiAwait(op uintptr, what string) error {
	var info uintptr
	hr := wiQueryInterface(op, &wiIIDAsyncInfo, &info)
	if wiFailed(hr) || info == 0 {
		return wiErr(what+": IAsyncInfo", hr)
	}
	defer wiRelease(info)
	for i := 0; ; i++ {
		var status int32
		hr := wiInvoke(info, wiAsyncInfoStatus, uintptr(unsafe.Pointer(&status)))
		if wiFailed(hr) {
			return wiErr(what+": IAsyncInfo.Status", hr)
		}
		switch status {
		case wiAsyncCompleted:
			return nil
		case wiAsyncCanceled:
			return fmt.Errorf("windows: %s was canceled", what)
		case wiAsyncError:
			var code uintptr
			wiInvoke(info, wiAsyncInfoError, uintptr(unsafe.Pointer(&code)))
			return wiErr(what, code)
		}
		if i > 300000 { // ~10 minutes at 2 ms
			return fmt.Errorf("windows: %s did not finish", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// wiAwaitResult waits for op and returns its GetResults value, which the
// caller releases.
func wiAwaitResult(op uintptr, what string) (uintptr, error) {
	defer wiRelease(op)
	if err := wiAwait(op, what); err != nil {
		return 0, err
	}
	var out uintptr
	hr := wiInvoke(op, wiAsyncOperationResults, uintptr(unsafe.Pointer(&out)))
	if wiFailed(hr) {
		return 0, wiErr(what+": GetResults", hr)
	}
	return out, nil
}

// String renders a GUID in its canonical text form, for errors.
func (g wiGUID) String() string {
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3], g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}
