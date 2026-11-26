//go:build windows

package platform

// Audio. The output device list and the volume come from Core Audio's
// MMDevice enumerator and IAudioEndpointVolume; a Bluetooth headset's
// charge is the value Windows' own Hands-Free Profile reader stores in a
// hidden PnP property. The icon is drawn here, since Windows has no
// symbol library to borrow the macOS template from.

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/image/draw"
	"golang.org/x/sys/windows"
)

// --- COM plumbing -----------------------------------------------------------

const (
	coinitApartmentThreaded = 0x2
	coinitMultithreaded     = 0x0
	clsctxAll               = 0x17
	eRender                 = 0
	eConsole                = 0
	deviceStateActive       = 0x1
	vtLPWStr                = 31
	vtUI4                   = 19
)

var (
	clsidMMDeviceEnumerator = windows.GUID{
		Data1: 0xBCDE0395, Data2: 0xE52F, Data3: 0x467C,
		Data4: [8]byte{0x8E, 0x3D, 0xC4, 0x57, 0x92, 0x91, 0x69, 0x2E},
	}
	iidMMDeviceEnumerator = windows.GUID{
		Data1: 0xA95664D2, Data2: 0x9614, Data3: 0x4F35,
		Data4: [8]byte{0xA7, 0x46, 0xDE, 0x8D, 0xB6, 0x36, 0x17, 0xE6},
	}
	iidAudioEndpointVolume = windows.GUID{
		Data1: 0x5CDF2C82, Data2: 0x841E, Data3: 0x4546,
		Data4: [8]byte{0x97, 0x22, 0x0C, 0xF7, 0x40, 0x78, 0x22, 0x9A},
	}

	// PKEY_Device_FriendlyName / PKEY_Device_EnumeratorName.
	devicePropertySet = windows.GUID{
		Data1: 0xA45C254E, Data2: 0xDF1C, Data3: 0x4EFD,
		Data4: [8]byte{0x80, 0x20, 0x67, 0xD1, 0x46, 0xA8, 0x50, 0xE0},
	}
	pkeyFriendlyName   = propKey{devicePropertySet, 14}
	pkeyEnumeratorName = propKey{devicePropertySet, 24}
)

// propKey is a PROPERTYKEY: a GUID and a property id.
type propKey struct {
	fmtid windows.GUID
	pid   uint32
}

// propVariant is the fixed 24-byte PROPVARIANT on a 64-bit build: the
// value type, six bytes of reserved space, then the 16-byte value union.
type propVariant struct {
	vt  uint16
	pad [6]byte
	val unsafe.Pointer // the union: a string pointer, a UInt32, ...
	_   [8]byte
}

func (v *propVariant) str() string {
	if v.vt != vtLPWStr || v.val == nil {
		return ""
	}
	defer windows.CoTaskMemFree(v.val)
	return windows.UTF16PtrToString((*uint16)(v.val))
}

// winCOMVtbl is a COM object's vtable: far more entries than any interface
// this file reaches.
type winCOMVtbl [256]uintptr

// winCOMMethod resolves method index of the COM interface obj.
func winCOMMethod(obj unsafe.Pointer, index int) uintptr {
	vtbl := *(*unsafe.Pointer)(obj)
	return (*winCOMVtbl)(vtbl)[index]
}

// winCOMCall calls method index of the COM interface obj. obj is an
// unsafe.Pointer so no live object is ever laundered through a uintptr;
// the pointer arguments stay reachable for the duration of the call.
//
//go:uintptrescapes
func winCOMCall(obj unsafe.Pointer, index int, args ...uintptr) uintptr {
	fn := winCOMMethod(obj, index)
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{uintptr(obj)}, args...)...)
	runtime.KeepAlive(obj)
	return r
}

func winCOMRelease(obj unsafe.Pointer) {
	if obj != nil {
		winCOMCall(obj, 2)
	}
}

// winCOMCallFloat calls method index of obj with one float argument.
// The Windows x64 convention passes a float in a vector register, which a
// raw integer call cannot name — but Go's Windows syscall stub copies the
// first four integer arguments into XMM0–XMM3 as well, for exactly this
// reason (src/internal/runtime/syscall/windows/asm_windows_amd64.s, the
// _1args–_4args block: "Floating point arguments are passed in the XMM
// registers. Set them here..."). The float's bits therefore go in the
// second integer argument, which the stub also places in XMM1 — the slot
// of the second argument.
func winCOMCallFloat(obj unsafe.Pointer, index int, f float32) uintptr {
	fn := winCOMMethod(obj, index)
	r, _, _ := syscall.SyscallN(fn, uintptr(obj), uintptr(math.Float32bits(f)), 0)
	runtime.KeepAlive(obj)
	return r
}

// rpcEChangedMode is the HRESULT CoInitializeEx answers when the thread
// already lives in a different apartment.
const rpcEChangedMode = 0x80010106

// enterCOMApartment initializes the calling thread's COM apartment and
// returns the leave call that balances it. ok is false when nothing was
// initialized — the thread already lives in a different apartment — and
// the returned leave is a no-op: uninitializing a count this call did not
// add would tear down the apartment its real owner still uses.
func enterCOMApartment(mode uintptr) (leave func(), ok bool) {
	hr, _, _ := procCoInitializeEx.Call(0, mode)
	if uint32(hr) == rpcEChangedMode || failedHR(hr) {
		return func() {}, false
	}
	return func() { procCoUninitialize.Call() }, true
}

// newDeviceEnumerator creates the MMDevice enumerator on this thread.
func newDeviceEnumerator() unsafe.Pointer {
	var enum unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidMMDeviceEnumerator)), 0, clsctxAll,
		uintptr(unsafe.Pointer(&iidMMDeviceEnumerator)), uintptr(unsafe.Pointer(&enum)))
	if int32(uint32(hr)) < 0 {
		return nil
	}
	return enum
}

// deviceString reads one string property of a device; "" when the device
// does not carry it.
func deviceString(dev unsafe.Pointer, key *propKey) string {
	var store unsafe.Pointer
	if hr := winCOMCall(dev, 4, 0 /* STGM_READ */, uintptr(unsafe.Pointer(&store))); int32(uint32(hr)) < 0 || store == nil {
		return ""
	}
	defer winCOMRelease(store)
	var v propVariant
	if hr := winCOMCall(store, 5, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(&v))); int32(uint32(hr)) < 0 {
		return ""
	}
	return v.str()
}

// deviceID is the endpoint's own id string, which survives a reconnect.
func deviceID(dev unsafe.Pointer) string {
	var p unsafe.Pointer
	if hr := winCOMCall(dev, 5, uintptr(unsafe.Pointer(&p))); int32(uint32(hr)) < 0 || p == nil {
		return ""
	}
	defer windows.CoTaskMemFree(p)
	return windows.UTF16PtrToString((*uint16)(p))
}

// OutputDevices lists every active output endpoint with its transport.
func OutputDevices() []AudioOutput {
	// The apartment, the enumerator and the endpoints are per thread:
	// hold the goroutine on one so the counts balance.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	leave, _ := enterCOMApartment(coinitApartmentThreaded)
	defer leave()
	enum := newDeviceEnumerator()
	if enum == nil {
		return nil
	}
	defer winCOMRelease(enum)
	var coll unsafe.Pointer
	if hr := winCOMCall(enum, 3, eRender, deviceStateActive, uintptr(unsafe.Pointer(&coll))); int32(uint32(hr)) < 0 || coll == nil {
		return nil
	}
	defer winCOMRelease(coll)
	var count uint32
	if hr := winCOMCall(coll, 3, uintptr(unsafe.Pointer(&count))); int32(uint32(hr)) < 0 {
		return nil
	}
	out := make([]AudioOutput, 0, count)
	for i := uint32(0); i < count; i++ {
		var dev unsafe.Pointer
		if hr := winCOMCall(coll, 4, uintptr(i), uintptr(unsafe.Pointer(&dev))); int32(uint32(hr)) < 0 || dev == nil {
			continue
		}
		name := deviceString(dev, &pkeyFriendlyName)
		id := deviceID(dev)
		transport := deviceString(dev, &pkeyEnumeratorName)
		winCOMRelease(dev)
		if name == "" {
			continue
		}
		out = append(out, AudioOutput{
			Name:      name,
			UID:       id,
			Bluetooth: strings.HasPrefix(strings.ToUpper(transport), "BTH"),
		})
	}
	return out
}

// defaultVolumeInterface opens IAudioEndpointVolume on the default output
// (the console role, the device the user hears).
func defaultVolumeInterface() unsafe.Pointer {
	enum := newDeviceEnumerator()
	if enum == nil {
		return nil
	}
	defer winCOMRelease(enum)
	var dev unsafe.Pointer
	if hr := winCOMCall(enum, 4, eRender, eConsole, uintptr(unsafe.Pointer(&dev))); int32(uint32(hr)) < 0 || dev == nil {
		return nil
	}
	defer winCOMRelease(dev)
	var vol unsafe.Pointer
	hr := winCOMCall(dev, 3, uintptr(unsafe.Pointer(&iidAudioEndpointVolume)), clsctxAll, 0, uintptr(unsafe.Pointer(&vol)))
	if int32(uint32(hr)) < 0 {
		return nil
	}
	return vol
}

// OutputVolume returns the default output's volume in 0..1.
func OutputVolume() (float32, bool) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	leave, _ := enterCOMApartment(coinitApartmentThreaded)
	defer leave()
	vol := defaultVolumeInterface()
	if vol == nil {
		return 0, false
	}
	defer winCOMRelease(vol)
	var v float32
	if hr := winCOMCall(vol, 9, uintptr(unsafe.Pointer(&v))); int32(uint32(hr)) < 0 {
		return 0, false
	}
	return clampVolume(v), true
}

// SetOutputVolume sets the default output's volume, clamped to 0..1.
func SetOutputVolume(v float32) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	leave, _ := enterCOMApartment(coinitApartmentThreaded)
	defer leave()
	v = clampVolume(v)
	vol := defaultVolumeInterface()
	if vol == nil {
		return errors.New("windows: no default output device")
	}
	defer winCOMRelease(vol)
	if hr := winCOMCallFloat(vol, 7, v); int32(uint32(hr)) < 0 {
		return errAudioVolume
	}
	return nil
}

// VolumeSupported reports whether the output exposes a readable volume.
func VolumeSupported() bool {
	_, ok := OutputVolume()
	return ok
}

var errAudioVolume = errors.New("windows: the default output device has no writable volume")

// HeadphoneBatteries returns the charge Windows read from the named
// headset over the Hands-Free Profile. Windows reports one battery level
// per device, so the result is HeadphoneWhole. A headset that does not
// implement the standard indicator, or one that is not connected, has no
// entry: the answer is nil, never an invented percentage.
func HeadphoneBatteries(name string) []BatteryLevel {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return nil
	}
	bt, ok := bluetoothDeviceBatteries()
	if !ok {
		return nil
	}
	for devName, pct := range bt {
		if headsetNameMatches(name, strings.ToLower(devName)) {
			return []BatteryLevel{{Part: HeadphoneWhole, Pct: pct}}
		}
	}
	return nil
}

// hidden battery property Windows fills in from the Hands-Free Profile.
var pkeyBluetoothBattery = windows.DEVPROPKEY{
	FmtID: windows.DEVPROPGUID{
		Data1: 0x104EA319, Data2: 0x6EE2, Data3: 0x4701,
		Data4: [8]byte{0xBD, 0x47, 0x8D, 0xDB, 0xF4, 0x25, 0xBB, 0xE5},
	},
	PID: 2,
}

var pkeyDeviceFriendlyName = windows.DEVPROPKEY{
	FmtID: windows.DEVPROPGUID(devicePropertySet),
	PID:   14,
}

var procSetupDiGetDeviceProperty = sysProc(windows.NewLazySystemDLL("setupapi.dll"), "SetupDiGetDevicePropertyW")

// bluetoothDeviceBatteries reads every present Bluetooth device's battery
// byte and friendly name. ok is false when the property set cannot be
// built at all (SetupAPI unavailable).
func bluetoothDeviceBatteries() (map[string]int, bool) {
	set, err := windows.SetupDiGetClassDevsEx(nil, "BTHENUM", 0, windows.DIGCF_PRESENT, 0, "")
	if err != nil || uintptr(set) == ^uintptr(0) {
		return nil, false
	}
	defer set.Close()
	out := map[string]int{}
	for i := 0; ; i++ {
		dev, err := set.EnumDeviceInfo(i)
		if err != nil {
			break
		}
		pct, ok := setupDeviceByte(set, dev, &pkeyBluetoothBattery)
		if !ok || pct <= 0 || pct > 100 {
			continue
		}
		name, ok := setupDeviceString(set, dev, &pkeyDeviceFriendlyName)
		if !ok || name == "" {
			continue
		}
		out[name] = pct
	}
	return out, true
}

// setupDeviceString reads a DEVPROP_TYPE_STRING property.
func setupDeviceString(set windows.DevInfo, dev *windows.DevInfoData, key *windows.DEVPROPKEY) (string, bool) {
	var typ windows.DEVPROPTYPE
	buf := make([]uint16, 512)
	var need uint32
	r, _, _ := procSetupDiGetDeviceProperty.Call(
		uintptr(set), uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)*2), uintptr(unsafe.Pointer(&need)), 0)
	if r == 0 || typ != windows.DEVPROP_TYPE_STRING {
		return "", false
	}
	return windows.UTF16ToString(buf), true
}

// setupDeviceByte reads a DEVPROP_TYPE_BYTE property.
func setupDeviceByte(set windows.DevInfo, dev *windows.DevInfoData, key *windows.DEVPROPKEY) (int, bool) {
	var typ windows.DEVPROPTYPE
	var b [4]byte
	var need uint32
	r, _, _ := procSetupDiGetDeviceProperty.Call(
		uintptr(set), uintptr(unsafe.Pointer(dev)), uintptr(unsafe.Pointer(key)),
		uintptr(unsafe.Pointer(&typ)), uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)), uintptr(unsafe.Pointer(&need)), 0)
	if r == 0 || typ != windows.DEVPROP_TYPE_BYTE {
		return 0, false
	}
	return int(b[0]), true
}

// headsetNameMatches compares the audio endpoint's name with the
// Bluetooth node's name. The endpoint is named like
// 「头戴式耳机 (WH-1000XM4 Stereo)」 while the node is
// 「WH-1000XM4」, so containment after normalizing spaces and case is
// what tells two paired headsets apart.
func headsetNameMatches(endpoint, node string) bool {
	norm := func(s string) string {
		return strings.Join(strings.Fields(strings.ToLower(s)), "")
	}
	a, b := norm(endpoint), norm(node)
	if a == "" || b == "" {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

// HeadphoneIcon returns a PNG template icon for the named device part.
// The template is a white silhouette whose alpha carries the shape, the
// form the callers tint for their own background.
func HeadphoneIcon(model string, part HeadphonePart, size int) []byte {
	if size <= 0 {
		size = 24
	}
	const ss = 4 // supersampled drawing, then scaled down
	n := size * ss
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	cx := float64(n) / 2
	switch part {
	case HeadphoneLeft, HeadphoneRight:
		drawPod(img, cx, float64(n)*0.5, float64(n)*0.36, part == HeadphoneRight, white)
	case HeadphoneCase:
		drawCase(img, float64(n)*0.5, float64(n)*0.5, float64(n)*0.68, white)
	default:
		lower := strings.ToLower(model)
		if strings.Contains(lower, "buds") || strings.Contains(lower, "airpods") {
			drawEarbuds(img, float64(n)*0.5, float64(n)*0.52, float64(n)*0.62, white)
		} else {
			drawHeadset(img, float64(n)*0.5, float64(n)*0.5, float64(n)*0.82, white)
		}
	}
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	var out bytes.Buffer
	if err := png.Encode(&out, dst); err != nil {
		return nil
	}
	return out.Bytes()
}

// fillEllipse paints an ellipse into img.
func fillEllipse(img *image.NRGBA, cx, cy, rx, ry float64, c color.NRGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			d := math.Hypot((float64(x)+0.5-cx)/rx, (float64(y)+0.5-cy)/ry)
			if d >= 1 {
				continue
			}
			img.SetNRGBA(x, y, c)
		}
	}
}

// fillRoundRect paints a rounded rectangle.
func fillRoundRect(img *image.NRGBA, x0, y0, x1, y1, r float64, c color.NRGBA) {
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			dx := math.Max(math.Max(x0+r-px, px-(x1-r)), 0)
			dy := math.Max(math.Max(y0+r-py, py-(y1-r)), 0)
			if math.Hypot(dx, dy) > r {
				continue
			}
			img.SetNRGBA(x, y, c)
		}
	}
}

// fillRing paints the upper arc of an ellipse as a band: the headband.
func fillRing(img *image.NRGBA, cx, cy, rx, ry, t float64, c color.NRGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if math.Atan2(dy, dx) > 0 {
				continue // lower half: the band stops at the cups
			}
			// Approximate distance to the ellipse: scale the point onto the
			// unit circle, then back out by the smaller radius.
			rho := math.Hypot(dx/rx, dy/ry)
			if math.Abs(rho-1)*math.Min(rx, ry) > t/2 {
				continue
			}
			img.SetNRGBA(x, y, c)
		}
	}
}

// drawHeadset is the over-ear silhouette: a headband above two cups.
func drawHeadset(img *image.NRGBA, cx, cy, s float64, c color.NRGBA) {
	fillRing(img, cx, cy+s*0.06, s*0.46, s*0.44, s*0.12, c)
	fillRoundRect(img, cx-s*0.52, cy-s*0.10, cx-s*0.30, cy+s*0.40, s*0.09, c)
	fillRoundRect(img, cx+s*0.30, cy-s*0.10, cx+s*0.52, cy+s*0.40, s*0.09, c)
}

// drawEarbuds is two stems under two buds.
func drawEarbuds(img *image.NRGBA, cx, cy, s float64, c color.NRGBA) {
	drawPod(img, cx-s*0.30, cy, s*0.44, false, c)
	drawPod(img, cx+s*0.30, cy, s*0.44, true, c)
}

// drawPod is one earbud: a bud with a stem below it.
func drawPod(img *image.NRGBA, cx, cy, s float64, mirror bool, c color.NRGBA) {
	fillEllipse(img, cx, cy-s*0.36, s*0.30, s*0.26, c)
	stemX := cx - s*0.14
	if mirror {
		stemX = cx + s*0.14
	}
	fillRoundRect(img, stemX-s*0.07, cy-s*0.30, stemX+s*0.07, cy+s*0.52, s*0.07, c)
}

// drawCase is the charging case: a rounded lid.
func drawCase(img *image.NRGBA, cx, cy, s float64, c color.NRGBA) {
	fillRoundRect(img, cx-s*0.42, cy-s*0.5, cx+s*0.42, cy+s*0.5, s*0.16, c)
}

// procRtlMoveMemory copies memory; the string reader uses it to move an
// HSTRING's buffer into a Go slice without a uintptr-to-pointer cast.
var procRtlMoveMemory = sysProc(kernel32, "RtlMoveMemory")

// failedHR reports a failed HRESULT.
func failedHR(hr uintptr) bool { return int32(uint32(hr)) < 0 }

// winCOMQI returns obj's implementation of iid, or nil.
func winCOMQI(obj unsafe.Pointer, iid *windows.GUID) unsafe.Pointer {
	if obj == nil {
		return nil
	}
	var out unsafe.Pointer
	if hr := winCOMCall(obj, 0, uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&out))); failedHR(hr) {
		return nil
	}
	return out
}

// winCoCreateInstance creates the in-process COM object clsid as iid.
func winCoCreateInstance(clsid, iid *windows.GUID) unsafe.Pointer {
	var obj unsafe.Pointer
	hr, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(clsid)), 0, clsctxAll,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&obj)))
	if failedHR(hr) {
		return nil
	}
	return obj
}
