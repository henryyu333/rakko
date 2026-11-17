package main

// The Windows HEIC codec failures are the one error text the user can act
// on: it names the Microsoft Store extension to install. These checks pin
// the wording in both languages and the two places it reaches the UI.

import (
	"errors"
	"fmt"
	"testing"

	"rakko/internal/platform"
	"rakko/locales"
)

func TestImageCodecErrorWording(t *testing.T) {
	zh, en := locales.In("zh-Hans"), locales.In("en")
	for _, tt := range []struct {
		name           string
		err            error
		wantZH, wantEN string
	}{
		{
			"missing HEIF Image Extensions",
			fmt.Errorf("open a.heic: %w", platform.ErrHEIFExtensionMissing),
			"需要安装 Microsoft Store 的「HEIF 图像扩展」",
			"Install the HEIF Image Extensions from the Microsoft Store",
		},
		{
			"missing HEVC Video Extensions",
			fmt.Errorf("decode a.heic: %w", platform.ErrHEVCExtensionMissing),
			"需要安装「HEVC 视频扩展」",
			"Install the HEVC Video Extensions",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := imageCodecError(zh, tt.err); got != tt.wantZH {
				t.Fatalf("Chinese wording = %q, want %q", got, tt.wantZH)
			}
			if got := imageCodecError(en, tt.err); got != tt.wantEN {
				t.Fatalf("English wording = %q, want %q", got, tt.wantEN)
			}
		})
	}

	// A failure that is not a missing codec keeps every detail it carried.
	other := errors.New("windows: WIC decoder failed (0x80070005)")
	if got := imageCodecError(zh, other); got != other.Error() {
		t.Fatalf("unrelated error worded as %q, want %q", got, other.Error())
	}
}

func TestConvertFailTextNamesTheMissingExtension(t *testing.T) {
	zh, en := locales.In("zh-Hans"), locales.In("en")
	if got := convertFailText(zh, toHEIC, platform.ErrHEVCExtensionMissing); got != "转换失败：需要安装「HEVC 视频扩展」" {
		t.Fatalf("conversion failure = %q", got)
	}
	if got := convertFailText(en, toFit1M, platform.ErrHEIFExtensionMissing); got != "Compression failed: Install the HEIF Image Extensions from the Microsoft Store" {
		t.Fatalf("compression failure = %q", got)
	}
}
