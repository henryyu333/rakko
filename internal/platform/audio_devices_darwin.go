//go:build darwin

package platform

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// The system's audio output devices through CoreAudio's AudioObject
// properties: the device list, each device's name and transport type, and
// whether it carries output streams. Bluetooth devices are the ones the
// realtime-activity notifier watches; the transport type is what tells
// them apart from the built-in speakers, USB interfaces and displays.
//
// Main thread only, like the volume calls next door.

// AudioOutput is one device that can play sound.
type AudioOutput struct {
	// Name is kAudioObjectPropertyName, e.g. 「AirPods Pro」.
	Name string
	// UID is kAudioDevicePropertyDeviceUID: stable across reconnects.
	UID string
	// Bluetooth is true for the Bluetooth and Bluetooth LE transports.
	Bluetooth bool
}

const (
	selectorDevices    = 'd'<<24 | 'e'<<16 | 'v'<<8 | '#' // 'dev#'
	selectorTransport  = 't'<<24 | 'r'<<16 | 'a'<<8 | 'n' // 'tran'
	selectorObjectName = 'l'<<24 | 'n'<<16 | 'a'<<8 | 'm' // 'lnam'
	selectorDeviceUID  = 'u'<<24 | 'i'<<16 | 'd'<<8 | ' ' // 'uid '
	// kAudioDevicePropertyDeviceCanBeDefaultDevice ('dflt') with the
	// output scope: the fallback for devices that do not describe their
	// streams (a Bluetooth headset always can be the default output; a
	// Bluetooth keyboard cannot).
	selectorCanBeDefault = 'd'<<24 | 'f'<<16 | 'l'<<8 | 't'

	transportBluetooth   uint32 = 'b'<<24 | 'l'<<16 | 'u'<<8 | 'e' // 'blue'
	transportBluetoothLE uint32 = 'b'<<24 | 'l'<<16 | 'e'<<8 | 'a' // 'blea'
)

var (
	audioDevicesOnce sync.Once
	audioDevicesErr  error
	audioSizeAddr    uintptr
)

// loadAudioDevices resolves the size query once; the getter comes from
// loadCoreAudio, which every path here needs anyway.
func loadAudioDevices() error {
	audioDevicesOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				audioDevicesErr = fmt.Errorf("mac: CoreAudio device list setup failed: %v", r)
			}
		}()
		if err := loadCoreAudio(); err != nil {
			audioDevicesErr = err
			return
		}
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreAudio.framework/CoreAudio",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
		if err != nil {
			audioDevicesErr = fmt.Errorf("mac: cannot load CoreAudio: %w", err)
			return
		}
		if audioSizeAddr, err = purego.Dlsym(lib, "AudioObjectGetPropertyDataSize"); err != nil {
			audioDevicesErr = fmt.Errorf("mac: %w", err)
			return
		}
	})
	return audioDevicesErr
}

// OutputDevices lists every audio device that has at least one output
// channel, with its transport. Devices that do not describe their streams
// are included too: CoreAudio's own aggregate and virtual devices often do
// not, and dropping them would misreport a connected pair of headphones.
// The list is empty when CoreAudio cannot be reached.
func OutputDevices() []AudioOutput {
	if err := loadAudioDevices(); err != nil {
		return nil
	}
	addr := AudioObjectPropertyAddress{Selector: selectorDevices, Scope: scopeGlobal, Element: elementMain}
	var size uint32
	ret, _, _ := purego.SyscallN(audioSizeAddr,
		uintptr(kAudioObjectSystemObject), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)))
	runtime.KeepAlive(&addr)
	if int32(ret) != 0 || size < 4 {
		return nil
	}
	devs := make([]uint32, size/4)
	ret, _, _ = purego.SyscallN(audioGetAddr,
		uintptr(kAudioObjectSystemObject), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&devs[0])))
	runtime.KeepAlive(&addr)
	runtime.KeepAlive(devs)
	if int32(ret) != 0 || int(size/4) > len(devs) || size < 4 {
		return nil
	}
	devs = devs[:size/4]
	out := make([]AudioOutput, 0, len(devs))
	for _, dev := range devs {
		outputs := outputChannelCount(dev)
		if outputs == 0 && !audioCanBeDefaultOutput(dev) {
			continue
		}
		name, uid := audioDeviceString(dev, selectorObjectName), audioDeviceString(dev, selectorDeviceUID)
		if name == "" {
			continue
		}
		transport := audioDeviceUint32(dev, selectorTransport)
		out = append(out, AudioOutput{
			Name:      name,
			UID:       uid,
			Bluetooth: transport == transportBluetooth || transport == transportBluetoothLE,
		})
	}
	return out
}

// audioCanBeDefaultOutput reports whether the device can carry the system
// output, which separates real outputs from input-only devices that do not
// publish a stream configuration.
func audioCanBeDefaultOutput(dev uint32) bool {
	addr := AudioObjectPropertyAddress{Selector: selectorCanBeDefault, Scope: scopeOutput, Element: elementMain}
	size := uint32(unsafe.Sizeof(uint32(0)))
	var v uint32
	ret, _, _ := purego.SyscallN(audioGetAddr,
		uintptr(dev), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&v)))
	runtime.KeepAlive(&addr)
	return int32(ret) == 0 && size == 4 && v != 0
}

// audioDeviceString reads a CFString property and releases the reference
// CoreAudio hands back.
func audioDeviceString(dev uint32, selector uint32) string {
	addr := AudioObjectPropertyAddress{Selector: selector, Scope: scopeGlobal, Element: elementMain}
	size := uint32(unsafe.Sizeof(uintptr(0)))
	var str uintptr
	ret, _, _ := purego.SyscallN(audioGetAddr,
		uintptr(dev), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&str)))
	runtime.KeepAlive(&addr)
	if int32(ret) != 0 || str == 0 || size != uint32(unsafe.Sizeof(uintptr(0))) {
		return ""
	}
	s := goString(id(str))
	releaseCF(str)
	return s
}

// audioDeviceUint32 reads a UInt32 property.
func audioDeviceUint32(dev uint32, selector uint32) uint32 {
	addr := AudioObjectPropertyAddress{Selector: selector, Scope: scopeGlobal, Element: elementMain}
	size := uint32(unsafe.Sizeof(uint32(0)))
	var v uint32
	ret, _, _ := purego.SyscallN(audioGetAddr,
		uintptr(dev), uintptr(unsafe.Pointer(&addr)), 0, 0,
		uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&v)))
	runtime.KeepAlive(&addr)
	if int32(ret) != 0 || size != 4 {
		return 0
	}
	return v
}
