//go:build darwin

package main

// 「压缩到」: shrink pictures under a byte budget instead of a pixel one.
// JPEG and HEIC keep their format and trade quality (0.4 floor, then pixel
// steps of ~0.85 down to a 640 px longer side); a PNG with transparency
// stays PNG and only shrinks — every other format becomes JPEG. The work
// rides the shared conversion pipeline (startConvert/planConvert/
// finishConvert); this file holds only the target bookkeeping, the menu
// and the result wording.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/store"
)

// Seams: tests substitute the encoder and the alpha probe.
var (
	fitImage   = mac.FitImage
	imageAlpha = mac.ImageAlpha
)

// fitLimit is a target's byte budget, 0 for targets that are not 「压缩到」.
func fitLimit(t convertTarget) int64 {
	switch t {
	case toFit500:
		return 500_000
	case toFit1M:
		return 1_000_000
	case toFit2M:
		return 2_000_000
	case toFit5M:
		return 5_000_000
	}
	return 0
}

// fitTiers are the menu's size choices, smallest first.
var fitTiers = []struct {
	target convertTarget
	size   string
}{
	{toFit500, "500 KB"},
	{toFit1M, "1 MB"},
	{toFit2M, "2 MB"},
	{toFit5M, "5 MB"},
}

// compressOutExt decides a compressed copy's format: JPEG and HEIC keep
// theirs (extension spelling included), a PNG carrying transparency stays
// PNG — lossy quality does not apply to it — and everything else (opaque
// PNGs too) becomes JPEG.
func compressOutExt(src string) string {
	switch ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(src), ".")); ext {
	case "jpg", "jpeg", "heic", "heif":
		return ext
	case "png":
		if alpha, err := imageAlpha(src); err == nil && alpha {
			return "png"
		}
	}
	return "jpg"
}

// compressFormat maps a compressed output's extension to its encoder.
func compressFormat(dst string) mac.ImageFormat {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(dst), ".")) {
	case "jpg", "jpeg":
		return mac.FormatJPEG
	case "heic", "heif":
		return mac.FormatHEIC
	default:
		return mac.FormatPNG
	}
}

// compressMenu adds 「压缩到 ▸」 after 缩小图片: a tier greys when every
// selected picture is already inside it, and the whole submenu greys when
// even the smallest tier is (or while a job runs).
func (a *shelf) compressMenu(m *ui.Menu, items []store.Item, busy bool) {
	sizes := make([]int64, len(items))
	known := true
	for i, it := range items {
		fi, err := os.Stat(it.Path)
		if err != nil {
			known = false
			break
		}
		sizes[i] = fi.Size()
	}
	allUnder := func(limit int64) bool {
		for _, n := range sizes {
			if n > limit {
				return false
			}
		}
		return true
	}
	l := a.appLoc()
	m.Submenu(l.ItemCompressTo(), func(sm *ui.Menu) {
		for _, tier := range fitTiers {
			if sm.Item(l.ItemCompressUnder(tier.size)).Disabled(busy || !known || allUnder(fitLimit(tier.target))).Chosen() {
				a.startConvert(items, tier.target)
			}
		}
	}).Disabled(busy || !known || allUnder(fitLimit(fitTiers[0].target)))
}
