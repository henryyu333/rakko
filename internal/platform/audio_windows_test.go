//go:build windows

package platform

// Audio checks on the Windows test machine: the endpoint list, a volume
// write that is read back and restored, the headset-name comparison, and
// the drawn template icon.

import (
	"bytes"
	"image/png"
	"math"
	"runtime"
	"testing"
)

func TestOutputDevicesList(t *testing.T) {
	devs := OutputDevices()
	if len(devs) == 0 {
		t.Fatal("OutputDevices listed nothing on a machine with audio endpoints")
	}
	for _, d := range devs {
		if d.Name == "" {
			t.Errorf("a device has no name: %+v", d)
		}
	}
}

func TestOutputVolumeRoundTrip(t *testing.T) {
	v, ok := OutputVolume()
	if !ok {
		t.Skip("the default output exposes no volume")
	}
	if v < 0 || v > 1 {
		t.Fatalf("OutputVolume = %v, want 0..1", v)
	}
	defer func() {
		if err := SetOutputVolume(v); err != nil {
			t.Errorf("restoring the volume: %v", err)
		}
	}()
	want := float32(0.42)
	if err := SetOutputVolume(want); err != nil {
		t.Fatalf("SetOutputVolume: %v", err)
	}
	got, ok := OutputVolume()
	if !ok {
		t.Fatal("VolumeSupported turned false after a successful write")
	}
	if math.Abs(float64(got-want)) > 0.02 {
		t.Fatalf("volume read back %v, want %v", got, want)
	}
	if err := SetOutputVolume(v); err != nil {
		t.Fatalf("restoring the volume: %v", err)
	}
	back, ok := OutputVolume()
	if !ok || math.Abs(float64(back-v)) > 0.02 {
		t.Fatalf("volume read back %v after restoring %v", back, v)
	}
}

func TestHeadsetNameMatching(t *testing.T) {
	for _, tt := range []struct {
		endpoint, node string
		want           bool
	}{
		{"头戴式耳机 (WH-1000XM4 Stereo)", "WH-1000XM4", true},
		{"Headphones (AirPods Pro)", "AirPods Pro", true},
		{"WH-1000XM4", "wh-1000xm4", true},
		{"扬声器 (Realtek Audio)", "WH-1000XM4", false},
		{"", "WH-1000XM4", false},
	} {
		if got := headsetNameMatches(tt.endpoint, tt.node); got != tt.want {
			t.Errorf("headsetNameMatches(%q, %q) = %v, want %v", tt.endpoint, tt.node, got, tt.want)
		}
	}
}

func TestHeadphoneIconIsATemplate(t *testing.T) {
	for _, part := range []HeadphonePart{HeadphoneWhole, HeadphoneLeft, HeadphoneRight, HeadphoneCase} {
		data := HeadphoneIcon("AirPods Pro", part, 24)
		if len(data) == 0 {
			t.Fatalf("part %d: no PNG", part)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("part %d: %v", part, err)
		}
		if b := img.Bounds(); b.Dx() != 24 || b.Dy() != 24 {
			t.Fatalf("part %d: icon is %v, want 24x24", part, b)
		}
		opaque, clear := false, false
		for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
			for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
				r, g, b, a := img.At(x, y).RGBA()
				if a >= 0xff00 {
					opaque = true
					if r != 0xffff || g != 0xffff || b != 0xffff {
						t.Fatalf("part %d: a template pixel is not white (%d,%d,%d)", part, r, g, b)
					}
				}
				if a == 0 {
					clear = true
				}
			}
		}
		if !opaque || !clear {
			t.Fatalf("part %d: template opaque=%v clear=%v, want both", part, opaque, clear)
		}
	}
}

// TestEnterCOMApartmentBalancesTheCount checks that the helper that
// replaced the bare CoInitializeEx calls gives the apartment back: a
// thread left in one makes the next user's other-mode request fail.
func TestEnterCOMApartmentBalancesTheCount(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	leave, ok := enterCOMApartment(coinitMultithreaded)
	if !ok {
		t.Fatal("a fresh test thread refused the MTA")
	}
	// A different mode is refused, and its no-op leave must not release
	// the apartment this test owns: the count still answers S_FALSE.
	other, otherOK := enterCOMApartment(coinitApartmentThreaded)
	if otherOK {
		t.Fatal("a thread already in the MTA accepted an STA")
	}
	other()
	hr, _, _ := procCoInitializeEx.Call(0, coinitMultithreaded)
	if code := int32(uint32(hr)); code != 1 {
		t.Fatalf("after the refused STA CoInitializeEx answered 0x%08X, want S_FALSE: the refused call released an apartment it did not own", uint32(hr))
	}
	procCoUninitialize.Call() // the direct initialize above
	leave()
	// Every count this test added is gone: a fresh initialize is S_OK.
	hr, _, _ = procCoInitializeEx.Call(0, coinitMultithreaded)
	if code := int32(uint32(hr)); code != 0 {
		t.Fatalf("after every leave CoInitializeEx answered 0x%08X, want S_OK: the count was not balanced", uint32(hr))
	}
	procCoUninitialize.Call()
}
