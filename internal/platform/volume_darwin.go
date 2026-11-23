//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// System output volume through CoreAudio's AudioObject property API.
// OutputVolume reads the default output device's volume scalar and
// SetOutputVolume writes it. Devices that carry no software volume
// (HDMI, many aggregate devices) report false or return an error, which
// the UI shows as a greyed-out volume row. Every call runs on the main
// thread, the same shape as the IOKit calls in power_darwin.go, with the
// argument addresses kept alive around SyscallN.

// AudioObjectPropertyAddress names one property of one audio object:
// which property (Selector), which scope of the object (Scope) and
// which channel (Element).
type AudioObjectPropertyAddress struct {
	Selector uint32
	Scope    uint32
	Element  uint32
}

const (
	// kAudioObjectSystemObject is the root object every device query
	// starts from.
	kAudioObjectSystemObject uint32 = 1

	// FourCC selectors and scopes, spelled the way CoreAudio names them.
	// Go has no multi-character rune literals, so each is built from its
	// bytes.
	selectorDefaultOutput = 'd'<<24 | 'O'<<16 | 'u'<<8 | 't' // 'dOut'
	selectorVolume        = 'v'<<24 | 'o'<<16 | 'l'<<8 | 'm' // 'volm'
	// selectorStreamConfiguration ('slay') describes a device's output
	// streams: how many, and how many channels each carries.
	selectorStreamConfiguration = 's'<<24 | 'l'<<16 | 'a'<<8 | 'y'
	scopeGlobal                 = 'g'<<24 | 'l'<<16 | 'o'<<8 | 'b' // 'glob'
	scopeOutput                 = 'o'<<24 | 'u'<<16 | 't'<<8 | 'p' // 'outp'

	// elementMain is the device-wide element; channels start at 1.
	elementMain uint32 = 0

	// An AudioBuffer is two UInt32 followed by a pointer (16 bytes on a
	// 64-bit build), and an AudioBufferList's first buffer starts at
	// offset 8: the buffer count, then the pointer's alignment.
	audioBufferSize     = 16
	audioBufferListHead = 8

	// maxVolumeChannels caps the per-channel scan. No real output device
	// carries more, and a device that reports nonsense must not turn the
	// volume row into a long loop.
	maxVolumeChannels = 32
)

var (
	coreAudioOnce    sync.Once
	coreAudioLoadErr error
	// Resolved addresses; the calls go through SyscallN like send() —
	// RegisterFunc mis-marshals these signatures on arm64 (the same
	// reason as the IOKit calls in power_darwin.go).
	audioGetAddr, audioSetAddr, audioHasAddr uintptr
)

// volumeBackend is every CoreAudio call the volume path makes, so a test
// can replace them as one unit: no test reads or changes the system
// volume. channels answers the device's output channel count, 0 when it
// does not describe its streams.
type volumeBackend struct {
	load     func() error
	device   func() (uint32, bool)
	get      func(obj, selector, scope, element uint32) (float32, bool)
	set      func(obj, selector, scope, element uint32, v float32) error
	has      func(obj, selector, scope, element uint32) bool
	channels func(obj uint32) int
}

// audioVolume is the live backend. OutputVolume, SetOutputVolume and
// VolumeSupported go through it.
var audioVolume = volumeBackend{
	load:     loadCoreAudio,
	device:   defaultOutputDevice,
	get:      audioObjectGetFloat32,
	set:      audioObjectSetFloat32,
	has:      audioObjectHasProperty,
	channels: outputChannelCount,
}

// loadCoreAudio opens CoreAudio once and resolves the property calls.
func loadCoreAudio() error {
	coreAudioOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				coreAudioLoadErr = fmt.Errorf("mac: CoreAudio setup failed: %v", r)
			}
		}()
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			coreAudioLoadErr = fmt.Errorf("mac: cannot load CoreAudio: %w", err)
			return
		}
		// The library handle stays open for the process lifetime; the
		// resolved addresses stay valid with it.
		for name, dst := range map[string]*uintptr{
			"AudioObjectGetPropertyData": &audioGetAddr,
			"AudioObjectSetPropertyData": &audioSetAddr,
			"AudioObjectHasProperty":     &audioHasAddr,
		} {
			if *dst, err = purego.Dlsym(lib, name); err != nil {
				coreAudioLoadErr = fmt.Errorf("mac: %w", err)
				return
			}
		}
	})
	return coreAudioLoadErr
}

// defaultOutputDevice returns the AudioObjectID of the system's current
// default output device; false when the system names none (no speakers
// and no headphones).
func defaultOutputDevice() (uint32, bool) {
	addr := AudioObjectPropertyAddress{
		Selector: selectorDefaultOutput,
		Scope:    scopeGlobal,
		Element:  elementMain,
	}
	size := uint32(unsafe.Sizeof(uint32(0)))
	var dev uint32
	ret, _, _ := purego.SyscallN(audioGetAddr,
		uintptr(kAudioObjectSystemObject),
		uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&dev)))
	runtime.KeepAlive(&addr)
	if int32(ret) != 0 || size != 4 || dev == 0 {
		return 0, false
	}
	return dev, true
}

// audioObjectGetFloat32 reads a property that is a single Float32. ok is
// false on any CoreAudio error, including a property the device does not
// have.
func audioObjectGetFloat32(obj, selector, scope, element uint32) (float32, bool) {
	addr := AudioObjectPropertyAddress{Selector: selector, Scope: scope, Element: element}
	size := uint32(unsafe.Sizeof(float32(0)))
	var v float32
	ret, _, _ := purego.SyscallN(audioGetAddr,
		uintptr(obj), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&v)))
	runtime.KeepAlive(&addr)
	runtime.KeepAlive(&v)
	if int32(ret) != 0 || size != 4 {
		return 0, false
	}
	return v, true
}

// audioObjectSetFloat32 writes a property that is a single Float32.
func audioObjectSetFloat32(obj, selector, scope, element uint32, v float32) error {
	addr := AudioObjectPropertyAddress{Selector: selector, Scope: scope, Element: element}
	size := uint32(unsafe.Sizeof(v))
	ret, _, _ := purego.SyscallN(audioSetAddr,
		uintptr(obj), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(size), uintptr(unsafe.Pointer(&v)))
	runtime.KeepAlive(&addr)
	runtime.KeepAlive(&v)
	if int32(ret) != 0 {
		return fmt.Errorf("AudioObjectSetPropertyData: OSStatus %d", int32(ret))
	}
	return nil
}

// outputChannelCount reports how many output channels dev has, 0 when the
// device does not describe its streams. The count is the sum of
// mNumberChannels over the stream configuration's buffers, which is what
// decides how many volume elements the device carries: a 5.1 device has
// six, not the two a stereo assumption would touch.
func outputChannelCount(dev uint32) int {
	if audioGetAddr == 0 {
		return 0 // CoreAudio not loaded; nothing to ask
	}
	addr := AudioObjectPropertyAddress{
		Selector: selectorStreamConfiguration,
		Scope:    scopeOutput,
		Element:  elementMain,
	}
	// The list is variable length and CoreAudio rejects a size-only query
	// for it (it needs a buffer), so the buffer is sized for the channel
	// cap and the reported length is trusted only up to what it holds.
	buf := make([]byte, audioBufferListHead+audioBufferSize*maxVolumeChannels)
	size := uint32(len(buf))
	ret, _, _ := purego.SyscallN(audioGetAddr,
		uintptr(dev), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&buf[0])))
	runtime.KeepAlive(&addr)
	runtime.KeepAlive(buf)
	if int32(ret) != 0 || size < audioBufferListHead+audioBufferSize || int(size) > len(buf) {
		return 0
	}
	buf = buf[:size]
	buffers := *(*uint32)(unsafe.Pointer(&buf[0]))
	if int(buffers) > maxVolumeChannels ||
		uint64(len(buf)) < uint64(audioBufferListHead)+uint64(buffers)*audioBufferSize {
		return 0
	}
	total := 0
	for i := uint32(0); i < buffers; i++ {
		off := audioBufferListHead + int(i)*audioBufferSize
		total += int(*(*uint32)(unsafe.Pointer(&buf[off])))
	}
	return total
}

// outputChannelElements returns the per-channel volume elements of dev, in
// element order. The device's own stream configuration decides the count;
// a device that does not describe its streams is probed one element at a
// time until HasProperty says no. An empty result means the device has no
// per-channel volume at all.
func outputChannelElements(dev uint32) []uint32 {
	n := audioVolume.channels(dev)
	if n <= 0 {
		for n < maxVolumeChannels && audioVolume.has(dev, selectorVolume, scopeOutput, uint32(n+1)) {
			n++
		}
	}
	if n > maxVolumeChannels {
		n = maxVolumeChannels
	}
	elems := make([]uint32, n)
	for i := range elems {
		elems[i] = uint32(i + 1)
	}
	return elems
}

// audioObjectHasProperty reports whether obj exposes the property. It
// answers without touching the property value.
func audioObjectHasProperty(obj, selector, scope, element uint32) bool {
	addr := AudioObjectPropertyAddress{Selector: selector, Scope: scope, Element: element}
	ret, _, _ := purego.SyscallN(audioHasAddr,
		uintptr(obj), uintptr(unsafe.Pointer(&addr)))
	runtime.KeepAlive(&addr)
	return uint32(ret) != 0
}

// OutputVolume returns the default output device's volume scalar in 0..1
// (the UI shows it as a percentage). ok is false when the system has no
// output device or the device has no readable software volume.
func OutputVolume() (float32, bool) {
	if err := audioVolume.load(); err != nil {
		return 0, false
	}
	dev, ok := audioVolume.device()
	if !ok {
		return 0, false
	}
	// Element 0 is the device-wide volume; most devices answer it.
	if v, ok := audioVolume.get(dev, selectorVolume, scopeOutput, elementMain); ok {
		return clampVolume(v), true
	}
	// Otherwise read every channel and report the mean, so a device
	// without a device-wide value still shows one number for all of them.
	var sum float32
	var n int
	for _, element := range outputChannelElements(dev) {
		if v, ok := audioVolume.get(dev, selectorVolume, scopeOutput, element); ok {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return clampVolume(sum / float32(n)), true
}

// SetOutputVolume sets the default output device's volume. v is clamped
// to 0..1 first. The device-wide element is preferred; a device without
// it gets every channel of its stream configuration, so a surround device
// changes all of its channels rather than the first two. The error is
// descriptive so the UI can grey the row: some devices (HDMI, many
// aggregate and virtual devices) have no writable software volume.
func SetOutputVolume(v float32) error {
	v = clampVolume(v)
	if err := audioVolume.load(); err != nil {
		return err
	}
	dev, ok := audioVolume.device()
	if !ok {
		return errors.New("mac: no default output device")
	}
	var mainErr error
	if audioVolume.has(dev, selectorVolume, scopeOutput, elementMain) {
		mainErr = audioVolume.set(dev, selectorVolume, scopeOutput, elementMain, v)
		if mainErr == nil {
			return nil
		}
	}
	var lastErr error
	set := false
	for _, element := range outputChannelElements(dev) {
		if !audioVolume.has(dev, selectorVolume, scopeOutput, element) {
			continue
		}
		if err := audioVolume.set(dev, selectorVolume, scopeOutput, element, v); err != nil {
			lastErr = err
			continue
		}
		set = true
	}
	if set {
		return nil
	}
	switch {
	case lastErr != nil:
		return fmt.Errorf("mac: default output device has no writable volume (device %d): %w", dev, lastErr)
	case mainErr != nil:
		return fmt.Errorf("mac: default output device has no writable volume (device %d): %w", dev, mainErr)
	default:
		return fmt.Errorf("mac: default output device has no writable volume (device %d)", dev)
	}
}

// VolumeSupported reports whether the default output device exposes a
// readable volume, i.e. whether the volume row can show a position.
func VolumeSupported() bool {
	_, ok := OutputVolume()
	return ok
}
