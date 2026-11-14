//go:build windows

package platform

// Audio. The skeleton keeps the volume and headset surface compiling; the
// Core Audio reader and the Bluetooth battery report arrive with the media
// batch. WINDOWS-NEXT(audio)

import "errors"

// AudioOutput is one device that can play sound.
type AudioOutput struct {
	// Name is the device's friendly name.
	Name string
	// UID is the device's stable endpoint id.
	UID string
	// Bluetooth is true for a Bluetooth endpoint.
	Bluetooth bool
}

// OutputDevices lists every audio device with an output, WINDOWS-NEXT(audio)
func OutputDevices() []AudioOutput { return nil }

// OutputVolume returns the default output's volume in 0..1.
// WINDOWS-NEXT(audio)
func OutputVolume() (float32, bool) { return 0, false }

// SetOutputVolume sets the default output's volume. WINDOWS-NEXT(audio)
func SetOutputVolume(v float32) error { return errNotYet }

// VolumeSupported reports whether the output exposes a readable volume.
// WINDOWS-NEXT(audio)
func VolumeSupported() bool { return false }

// HeadphonePart is which part of a headset an icon stands for.
type HeadphonePart int

const (
	HeadphoneWhole HeadphonePart = iota
	HeadphoneLeft
	HeadphoneRight
	HeadphoneCase
)

// BatteryLevel is one charge percentage a headset reports.
type BatteryLevel struct {
	Part HeadphonePart
	Pct  int
}

// HeadphoneBatteries returns the headset's reported charge levels.
// WINDOWS-NEXT(audio)
func HeadphoneBatteries(name string) []BatteryLevel { return nil }

// HeadphoneIcon returns a PNG template icon for the named device part.
// WINDOWS-NEXT(audio)
func HeadphoneIcon(model string, part HeadphonePart, size int) []byte { return nil }

// errNotYet is the failure of a call whose platform implementation is
// scheduled for a later batch. The error says which domain owns it, so a
// user-visible report names the missing feature instead of a blank result.
var errNotYet = errors.New("platform: this feature is not implemented on Windows yet")

// errDomain builds that error with the owning domain's name.
func errDomain(domain string) error {
	return errors.New("platform: the " + domain + " feature is not implemented on Windows yet")
}
