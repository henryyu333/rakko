//go:build windows

package platform

// WIC checks that do not need a real codec: the vtable slot the quality
// option is written through, and the pixel layouts the encoders ask for.

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWICSetQualityWritesThePropertyBag(t *testing.T) {
	bag := newFakeCOM()
	var (
		called    atomic.Int32
		props     int
		name      string
		bagVT     uint16
		valueVT   uint16
		quality   float32
		gotCount  uintptr
		gotWrites atomic.Int32
	)
	bag.slot[4] = windows.NewCallback(func(_ uintptr, count, propbag, value uintptr) uintptr {
		gotWrites.Add(1)
		gotCount = count
		pb := (*propBag2)(wiPtr(propbag))
		props = int(pb.dwType)
		name = windows.UTF16PtrToString(pb.pstrName)
		bagVT = pb.vt
		valueVT = *(*uint16)(wiPtr(value))
		quality = *(*float32)(unsafe.Add(wiPtr(value), 8))
		return 0
	})
	// IPropertyBag2's other methods, and the stale slot 7 the call used to
	// reach, must stay untouched.
	bag.pinSlot(5, 10, &called)

	if err := wicSetQuality(bag.ptr(), 0.7); err != nil {
		t.Fatalf("wicSetQuality: %v", err)
	}
	if slot := called.Load(); slot != 0 {
		t.Fatalf("the quality write reached vtable slot %d, want IPropertyBag2::Write (slot 4)", slot)
	}
	if gotWrites.Load() != 1 {
		t.Fatalf("the quality write ran %d times, want once", gotWrites.Load())
	}
	if gotCount != 1 {
		t.Errorf("Write counted %d properties, want 1", gotCount)
	}
	if name != "ImageQuality" {
		t.Errorf("wrote property %q, want ImageQuality", name)
	}
	if props != 0 || bagVT != 4 {
		t.Errorf("PROPBAG2 type %d vt %d, want PROPBAG2_TYPE_DATA and VT_R4", props, bagVT)
	}
	if valueVT != 4 {
		t.Errorf("PROPVARIANT vt %d, want VT_R4", valueVT)
	}
	if quality < 0.69 || quality > 0.71 {
		t.Errorf("quality %v, want 0.7", quality)
	}
}

func TestWICSetQualityReportsARefusal(t *testing.T) {
	bag := newFakeCOM()
	bag.slot[4] = windows.NewCallback(func(_, _, _ uintptr) uintptr { return 0x80004005 })
	if err := wicSetQuality(bag.ptr(), 0.5); err == nil {
		t.Fatal("a refused ImageQuality write was reported as applied")
	}
}

// TestWICDecodeReturnsPremultipliedAlpha pins the pixel contract with the
// rest of the package: a decoded semi-transparent red reaches image.RGBA
// premultiplied, as the type requires.
func TestWICDecodeReturnsPremultipliedAlpha(t *testing.T) {
	dir := t.TempDir()
	src := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	src.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 128})
	src.SetNRGBA(1, 0, color.NRGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "half-red.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	var img *image.RGBA
	// The real callers reach wicDecode inside wiApartment, which also
	// resolves the DLL procedures.
	err := wiApartment(func() error {
		var e error
		img, e = wicDecode(path)
		return e
	})
	if err != nil {
		t.Fatalf("wicDecode: %v", err)
	}
	half, opaque := img.RGBAAt(0, 0), img.RGBAAt(1, 0)
	if half.A != 128 {
		t.Fatalf("alpha %d, want 128", half.A)
	}
	// 255 premultiplied by 128/255 is 128; straight alpha would leave 255
	// and every later composite would treat the pixel as opaque red.
	if half.R < 126 || half.R > 129 || half.G != 0 || half.B != 0 {
		t.Fatalf("semi-transparent red decoded as %+v, want premultiplied (128,0,0,128)", half)
	}
	if opaque != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("opaque red decoded as %+v", opaque)
	}
}

// TestWICEncoderPixelFormatsTakePremultipliedRGBA checks the encoders'
// side of the same contract: image.RGBA is premultiplied, so a straight
// layout must unpremultiply and a premultiplied one must not.
func TestWICEncoderPixelFormatsTakePremultipliedRGBA(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	src.Pix = []byte{0x80, 0, 0, 0x80} // half-transparent red, premultiplied
	for _, tt := range []struct {
		guid wiGUID
		want []byte
	}{
		{wiGUIDPixelBGRA, []byte{0, 0, 255, 0x80}},  // straight BGRA
		{wiGUIDPixelPBGRA, []byte{0, 0, 0x80, 0x80}}, // premultiplied BGRA
		{wiGUIDPixel32BGR, []byte{0, 0, 255, 0xFF}},  // opaque, straight
		{wiGUIDPixel24BGR, []byte{0, 0, 255}},        // opaque, straight
	} {
		pf := wicPickPixelFormat(tt.guid)
		if pf == nil {
			t.Fatalf("no converter for pixel format %s", tt.guid.String())
		}
		dst := make([]byte, pf.stride(1))
		pf.convert(dst, src, 1, 1)
		if !bytes.Equal(dst, tt.want) {
			t.Errorf("format %s converted half-red to %v, want %v", tt.guid.String(), dst, tt.want)
		}
	}
}

// TestThumbnailRefusesAnOversizedSource checks the decode bound before any
// pixel buffer: a header alone claims 900 megapixels, which the old path
// would have allocated in full.
func TestThumbnailRefusesAnOversizedSource(t *testing.T) {
	dir := t.TempDir()
	gif := make([]byte, 13)
	copy(gif, "GIF89a")
	binary.LittleEndian.PutUint16(gif[6:], 30000)
	binary.LittleEndian.PutUint16(gif[8:], 30000)
	path := filepath.Join(dir, "huge.gif")
	if err := os.WriteFile(path, gif, 0o600); err != nil {
		t.Fatal(err)
	}
	if thumb, err := Thumbnail(path, 80); err == nil {
		t.Fatalf("Thumbnail of a 30000×30000 header returned %v", thumb.Rect)
	}
}

// TestThumbnailScalesTheLargeSource exercises the WIC scaler: a picture
// several times the thumbnail size comes back at the requested size.
func TestThumbnailScalesTheLargeSource(t *testing.T) {
	dir := t.TempDir()
	src := writePNG(t, dir, "large.png", testImage(2400, 1200))
	thumb, err := Thumbnail(src, 80)
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	if thumb.Rect.Dx() != 80 || thumb.Rect.Dy() != 40 {
		t.Fatalf("thumbnail %dx%d, want 80x40", thumb.Rect.Dx(), thumb.Rect.Dy())
	}
}
