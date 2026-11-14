//go:build darwin

package platform

import (
	"math"
	"slices"
	"testing"
)

// These tests never change the system volume: TestOutputVolumeReadOnly
// only reads it, and everything else replaces the whole CoreAudio backend
// (audioVolume). SetOutputVolume is never called against the live backend
// here — muting the user's machine as a test side effect is not
// acceptable.

func TestClampVolume(t *testing.T) {
	cases := []struct {
		name string
		in   float32
		want float32
	}{
		{"zero", 0, 0},
		{"one", 1, 1},
		{"middle", 0.5, 0.5},
		{"just below zero", -0.01, 0},
		{"negative", -3, 0},
		{"above one", 2.5, 1},
		{"nan becomes zero", float32(math.NaN()), 0},
	}
	for _, tc := range cases {
		if got := clampVolume(tc.in); got != tc.want {
			t.Errorf("%s: clampVolume(%v) = %v, want %v", tc.name, tc.in, got, tc.want)
		}
	}
}

// fakeAudio is a CoreAudio stand-in: volume maps element → value, and an
// absent element is one the device does not expose (no device-wide value
// when 0 is missing). Every write is recorded instead of applied.
type fakeAudio struct {
	volume   map[uint32]float32
	channels int
	writes   []uint32
	values   []float32
}

// install swaps the backend for the test and restores it afterwards.
func (f *fakeAudio) install(t *testing.T) {
	t.Helper()
	old := audioVolume
	audioVolume = volumeBackend{
		load:   func() error { return nil },
		device: func() (uint32, bool) { return 42, true },
		get: func(_, _, _, element uint32) (float32, bool) {
			v, ok := f.volume[element]
			return v, ok
		},
		set: func(_, _, _, element uint32, v float32) error {
			f.writes = append(f.writes, element)
			f.values = append(f.values, v)
			return nil
		},
		has: func(_, _, _, element uint32) bool {
			_, ok := f.volume[element]
			return ok
		},
		channels: func(uint32) int { return f.channels },
	}
	t.Cleanup(func() { audioVolume = old })
}

// A device without a device-wide value is averaged over every channel its
// stream configuration describes, not just the first two: a 5.1 device's
// surround channels carry volume too.
func TestOutputVolumeReadsEveryChannel(t *testing.T) {
	f := &fakeAudio{
		volume:   map[uint32]float32{1: 0.1, 2: 0.2, 3: 0.3, 4: 0.4, 5: 0.5, 6: 0.6},
		channels: 6,
	}
	f.install(t)
	v, ok := OutputVolume()
	if !ok {
		t.Fatal("OutputVolume reported no volume for a six-channel device")
	}
	if want := float32(0.35); math.Abs(float64(v-want)) > 1e-6 {
		t.Fatalf("OutputVolume = %v, want the mean of all six channels (%v); "+
			"channels 1 and 2 alone would give 0.15", v, want)
	}
}

// The same device is written on every channel, so a surround device turns
// all the way down (or up) instead of leaving channels 3..6 untouched.
func TestSetOutputVolumeWritesEveryChannel(t *testing.T) {
	f := &fakeAudio{
		volume:   map[uint32]float32{1: 0.1, 2: 0.2, 3: 0.3, 4: 0.4, 5: 0.5, 6: 0.6},
		channels: 6,
	}
	f.install(t)
	if err := SetOutputVolume(0.42); err != nil {
		t.Fatalf("SetOutputVolume: %v", err)
	}
	want := []uint32{1, 2, 3, 4, 5, 6}
	if !slices.Equal(f.writes, want) {
		t.Fatalf("channels written = %v, want %v", f.writes, want)
	}
	for i, v := range f.values {
		if v != 0.42 {
			t.Fatalf("channel %d written %v, want 0.42", f.writes[i], v)
		}
	}
}

// A device that does not describe its streams is probed one element at a
// time: every channel that answers HasProperty is read or written, and
// the first one that does not ends the scan.
func TestOutputChannelElementsProbeWithoutConfiguration(t *testing.T) {
	f := &fakeAudio{volume: map[uint32]float32{1: 0.2, 2: 0.4}, channels: 0}
	f.install(t)
	got := outputChannelElements(42)
	if want := []uint32{1, 2}; !slices.Equal(got, want) {
		t.Fatalf("outputChannelElements = %v, want %v", got, want)
	}
}

// A device that reports more channels than maxVolumeChannels is capped,
// so a broken stream configuration cannot turn the volume row into a long
// loop.
func TestOutputChannelElementsCap(t *testing.T) {
	f := &fakeAudio{volume: map[uint32]float32{1: 0.5}, channels: 1000}
	f.install(t)
	if got := len(outputChannelElements(42)); got != maxVolumeChannels {
		t.Fatalf("outputChannelElements returned %d elements, want the cap %d",
			got, maxVolumeChannels)
	}
}

// TestOutputVolumeReadOnly checks the live read path against the real
// system. It skips when CoreAudio or the default device offers no
// readable volume (some HDMI and aggregate devices), and it is
// read-only by construction.
func TestOutputVolumeReadOnly(t *testing.T) {
	v, ok := OutputVolume()
	if !ok {
		t.Skip("no readable output volume on this system")
	}
	if v < 0 || v > 1 || math.IsNaN(float64(v)) {
		t.Fatalf("OutputVolume = %v, want a scalar in 0..1", v)
	}
	if !VolumeSupported() {
		t.Fatal("VolumeSupported says false, but OutputVolume read a value")
	}
}
