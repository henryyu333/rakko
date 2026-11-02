package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// 源图 160×80 四个象限用可区分的纯色填充，JPEG 解码加 EXIF
// 方向修正后，缩略图四角的颜色必须对应摆正后的位置。
var (
	quadTL = color.NRGBA{R: 220, G: 30, B: 30, A: 255}  // 红
	quadTR = color.NRGBA{R: 30, G: 200, B: 30, A: 255}  // 绿
	quadBL = color.NRGBA{R: 30, G: 30, B: 220, A: 255}  // 蓝
	quadBR = color.NRGBA{R: 230, G: 220, B: 30, A: 255} // 黄
)

func quadrantImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 160, 80))
	draw.Draw(img, image.Rect(0, 0, 80, 40), &image.Uniform{C: quadTL}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(80, 0, 160, 40), &image.Uniform{C: quadTR}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 40, 80, 80), &image.Uniform{C: quadBL}, image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(80, 40, 160, 80), &image.Uniform{C: quadBR}, image.Point{}, draw.Src)
	return img
}

// jpegWithAPP1 编码象限图并在 SOI 后插入一段 APP1 数据。
func jpegWithAPP1(t *testing.T, dir, name string, app1 []byte) string {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, quadrantImage(), &jpeg.Options{Quality: 96}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	data := buf.Bytes()
	out := append([]byte{}, data[:2]...)
	out = append(out, app1...)
	out = append(out, data[2:]...)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

// exifAPP1 构造只含一个 Orientation 标签（0x0112）的 APP1 段。
func exifAPP1(orientation uint16, bigEndian bool) []byte {
	var order binary.ByteOrder = binary.LittleEndian
	tiff := []byte{'I', 'I'}
	if bigEndian {
		order = binary.BigEndian
		tiff[0], tiff[1] = 'M', 'M'
	}
	var tmp [4]byte
	put16 := func(v uint16) { order.PutUint16(tmp[:2], v); tiff = append(tiff, tmp[:2]...) }
	put32 := func(v uint32) { order.PutUint32(tmp[:4], v); tiff = append(tiff, tmp[:4]...) }
	put16(42)          // TIFF magic
	put32(8)           // IFD0 偏移
	put16(1)           // 条目数
	put16(0x0112)      // Orientation
	put16(3)           // SHORT
	put32(1)           // count
	put16(orientation) // 值（内联）
	put16(0)
	put32(0) // next IFD

	payload := append([]byte("Exif\x00\x00"), tiff...)
	seg := []byte{0xFF, 0xE1}
	l := uint16(len(payload) + 2)
	seg = append(seg, byte(l>>8), byte(l))
	return append(seg, payload...)
}

func thumbColorNear(got, want color.NRGBA) bool {
	const tol = 48
	diff := func(a, b uint8) int {
		d := int(a) - int(b)
		if d < 0 {
			return -d
		}
		return d
	}
	return diff(got.R, want.R) <= tol && diff(got.G, want.G) <= tol && diff(got.B, want.B) <= tol
}

// 取缩略图某个象限中心的像素做断言，避开 JPEG 边缘振铃。
func quadrantColor(img *image.NRGBA, qx, qy int) color.NRGBA {
	b := img.Bounds()
	return img.NRGBAAt(b.Min.X+b.Dx()*(2*qx+1)/4, b.Min.Y+b.Dy()*(2*qy+1)/4)
}

func TestDecodeThumbAppliesJPEGEXIFOrientation(t *testing.T) {
	cases := []struct {
		name                           string
		orientation                    uint16
		bigEndian                      bool
		wantW, wantH                   int
		wantTL, wantTR, wantBL, wantBR color.NRGBA
	}{
		{"normal", 1, false, 80, 40, quadTL, quadTR, quadBL, quadBR},
		{"mirror horizontal", 2, false, 80, 40, quadTR, quadTL, quadBR, quadBL},
		{"rotate 180", 3, false, 80, 40, quadBR, quadBL, quadTR, quadTL},
		{"mirror vertical", 4, true, 80, 40, quadBL, quadBR, quadTL, quadTR},
		{"transpose", 5, false, 40, 80, quadTL, quadBL, quadTR, quadBR},
		{"rotate 90 CW", 6, false, 40, 80, quadBL, quadTL, quadBR, quadTR},
		{"transverse", 7, true, 40, 80, quadBR, quadTR, quadBL, quadTL},
		{"rotate 270 CW", 8, false, 40, 80, quadTR, quadBR, quadTL, quadBL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := jpegWithAPP1(t, t.TempDir(), "photo.jpg", exifAPP1(tc.orientation, tc.bigEndian))
			thumb := decodeThumb(path)
			if thumb == nil {
				t.Fatalf("decodeThumb(%d) returned nil", tc.orientation)
			}
			if got := thumb.Bounds(); got.Dx() != tc.wantW || got.Dy() != tc.wantH {
				t.Fatalf("orientation %d: bounds %v, want %dx%d", tc.orientation, got, tc.wantW, tc.wantH)
			}
			for corner, want := range map[[2]int]color.NRGBA{
				{0, 0}: tc.wantTL, {1, 0}: tc.wantTR, {0, 1}: tc.wantBL, {1, 1}: tc.wantBR,
			} {
				if got := quadrantColor(thumb, corner[0], corner[1]); !thumbColorNear(got, want) {
					t.Errorf("orientation %d corner %v: got %v, want %v", tc.orientation, corner, got, want)
				}
			}
		})
	}
}

func TestDecodeThumbMalformedEXIFFallsBackToNormal(t *testing.T) {
	cases := []struct {
		name string
		app1 []byte
	}{
		{"orientation out of range", exifAPP1(9, false)},
		{"orientation zero", exifAPP1(0, false)},
		{"truncated TIFF header", []byte{0xFF, 0xE1, 0x00, 0x0C, 'E', 'x', 'i', 'f', 0, 0, 'I', 'I', '*', 0}},
		{"non-EXIF APP1", []byte{0xFF, 0xE1, 0x00, 0x10, 'h', 't', 't', 'p', ':', '/', '/', 'x', 'm', 'p', '/', 'a', 'b', 'c'}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := jpegWithAPP1(t, t.TempDir(), "bad.jpg", tc.app1)
			thumb := decodeThumb(path)
			if thumb == nil {
				t.Fatal("decodeThumb returned nil for malformed EXIF")
			}
			if got := thumb.Bounds(); got.Dx() != 80 || got.Dy() != 40 {
				t.Fatalf("bounds %v, want 80x40", got)
			}
			for corner, want := range map[[2]int]color.NRGBA{
				{0, 0}: quadTL, {1, 0}: quadTR, {0, 1}: quadBL, {1, 1}: quadBR,
			} {
				if got := quadrantColor(thumb, corner[0], corner[1]); !thumbColorNear(got, want) {
					t.Errorf("corner %v: got %v, want %v (should be unrotated)", corner, got, want)
				}
			}
		})
	}
}

// The menu-bar item draws what the resource directory ships: the file's
// bytes go to the tray unchanged, so macOS does the template tinting.
func TestTrayIconLoadsTheResourceFile(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 45, 36))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, trayIconFile), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := trayIcon(dir); !bytes.Equal(got, buf.Bytes()) {
		t.Fatalf("trayIcon returned %d bytes, want the file's %d", len(got), buf.Len())
	}
}

// A missing icon is not an error: the tray then shows its title so the
// menu-bar item stays visible and clickable.
func TestTrayIconWithoutFile(t *testing.T) {
	if got := trayIcon(t.TempDir()); got != nil {
		t.Errorf("trayIcon of an empty directory = %d bytes, want nil", len(got))
	}
	if got := trayIcon(""); got != nil {
		t.Errorf("trayIcon of an unknown directory = %d bytes, want nil", len(got))
	}
}
