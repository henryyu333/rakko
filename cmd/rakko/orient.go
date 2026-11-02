package main

import (
	"encoding/binary"
	"image"
	"io"
	"os"

	"golang.org/x/image/draw"
)

// jpegOrientation 读取 JPEG APP1/EXIF 的 Orientation（1–8），无 EXIF、
// 非 JPEG 或元数据损坏时返回 1（正常方向）。只扫描段头，APP1 载荷有界，
// 不把整个文件读进内存。
func jpegOrientation(rs *os.File) int {
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return 1
	}
	var soi [2]byte
	if _, err := io.ReadFull(rs, soi[:]); err != nil || soi[0] != 0xFF || soi[1] != 0xD8 {
		return 1
	}
	var segLen [2]byte
	for {
		var m [2]byte
		if _, err := io.ReadFull(rs, m[:]); err != nil || m[0] != 0xFF {
			return 1
		}
		// 填充字节与无长度的标记（RSTn/TEM）按单字节跳过。
		for m[1] == 0xFF {
			if _, err := io.ReadFull(rs, m[1:2]); err != nil {
				return 1
			}
		}
		if m[1] == 0x01 || m[1] >= 0xD0 && m[1] <= 0xD8 {
			continue
		}
		if m[1] == 0xD9 || m[1] == 0xDA { // EOI / SOS：头信息到此为止
			return 1
		}
		if _, err := io.ReadFull(rs, segLen[:]); err != nil {
			return 1
		}
		n := int(binary.BigEndian.Uint16(segLen[:])) - 2
		if n < 0 {
			return 1
		}
		if m[1] == 0xE1 {
			payload := make([]byte, n)
			if _, err := io.ReadFull(rs, payload); err != nil {
				return 1
			}
			if o := parseExifOrientation(payload); o != 0 {
				return o
			}
			continue // 非 EXIF 的 APP1（如 XMP）继续扫描后续 APP1
		}
		if _, err := rs.Seek(int64(n), io.SeekCurrent); err != nil {
			return 1
		}
	}
}

// parseExifOrientation 解析 APP1 载荷（"Exif\x00\x00"+TIFF）中的
// Orientation，合法返回 1–8，否则返回 0。任一字段越界、字节序非法、
// 计数溢出都按损坏处理。
func parseExifOrientation(app1 []byte) int {
	const exifHeader = "Exif\x00\x00"
	if len(app1) < len(exifHeader)+8 || string(app1[:len(exifHeader)]) != exifHeader {
		return 0
	}
	tiff := app1[len(exifHeader):]
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 0
	}
	count := int(order.Uint16(tiff[ifd : ifd+2]))
	if count > (len(tiff)-ifd-2)/12 {
		return 0
	}
	for i := range count {
		e := tiff[ifd+2+i*12:]
		if order.Uint16(e[0:2]) == 0x0112 && order.Uint16(e[2:4]) == 3 && order.Uint32(e[4:8]) == 1 {
			o := int(order.Uint16(e[8:10]))
			if o >= 1 && o <= 8 {
				return o
			}
			return 0
		}
	}
	return 0
}

// orientImage 把已经降采样的小图按 EXIF Orientation 摆正，避免生成
// 全尺寸方向副本；未识别或正常方向原样返回。
func orientImage(src *image.NRGBA, o int) *image.NRGBA {
	if o < 2 || o > 8 {
		return src
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	dw, dh := sw, sh
	if o >= 5 {
		dw, dh = sh, sw
	}
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	for dy := range dh {
		for dx := range dw {
			var sx, sy int
			switch o {
			case 2: // 水平镜像
				sx, sy = sw-1-dx, dy
			case 3: // 旋转 180°
				sx, sy = sw-1-dx, sh-1-dy
			case 4: // 垂直镜像
				sx, sy = dx, sh-1-dy
			case 5: // 主对角线转置
				sx, sy = dy, dx
			case 6: // 顺时针 90°
				sx, sy = dy, sh-1-dx
			case 7: // 副对角线转置
				sx, sy = dh-1-dy, dw-1-dx
			case 8: // 逆时针 90°
				sx, sy = dh-1-dy, dx
			}
			si := src.PixOffset(sx, sy)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// decodeThumb 解码图片文件、按最长边 thumbPx 降采样，并按 JPEG EXIF
// Orientation 把缩略图摆正；失败返回 nil。
// 尺寸检查通过后才完整解码；后台降采样，不把原图常驻 GPU；
// 方向修正作用在降采样后的小图上，不生成全尺寸方向副本。
func decodeThumb(path string) *image.NRGBA {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	orientation := jpegOrientation(f)
	if _, err = f.Seek(0, 0); err != nil {
		return nil
	}
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return nil
	}
	if _, err = f.Seek(0, 0); err != nil {
		return nil
	}
	img, _, err := image.Decode(f)
	if err != nil {
		return nil
	}
	w, h := cfg.Width, cfg.Height
	if w >= h {
		h = max(1, h*thumbPx/w)
		w = thumbPx
	} else {
		w = max(1, w*thumbPx/h)
		h = thumbPx
	}
	thumb := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(thumb, thumb.Bounds(), img, img.Bounds(), draw.Src, nil)
	return orientImage(thumb, orientation)
}
