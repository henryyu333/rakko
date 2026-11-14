//go:build darwin

package platform

import "strings"

// HeadphonePart is which part of a headset an icon stands for.
type HeadphonePart int

const (
	HeadphoneWhole HeadphonePart = iota
	HeadphoneLeft
	HeadphoneRight
	HeadphoneCase
)

// HeadphoneIcon returns a PNG template icon for the named device part:
// the whole headset, one pod, or the charging case. An unknown model gets
// the generic headphones symbol; a missing image for the specific symbol
// falls back to the plain one.
func HeadphoneIcon(model string, part HeadphonePart, size int) []byte {
	name := headphoneSymbol(model, part)
	if out := systemSymbol(name, size); out != nil {
		return out
	}
	if fallback := plainHeadphoneSymbol(part); fallback != name {
		return systemSymbol(fallback, size)
	}
	return nil
}

// headphoneSymbol picks the closest SF Symbol available for the device and
// part.
func headphoneSymbol(model string, part HeadphonePart) string {
	switch part {
	case HeadphoneLeft:
		return "airpod.left"
	case HeadphoneRight:
		return "airpod.right"
	case HeadphoneCase:
		if strings.Contains(strings.ToLower(model), "pro") {
			return "airpodspro.chargingcase.wireless"
		}
		return "airpods.chargingcase"
	}
	name := strings.ToLower(model)
	switch {
	case strings.Contains(name, "airpods") && strings.Contains(name, "max"):
		return "airpodsmax"
	case strings.Contains(name, "airpods") && strings.Contains(name, "pro") && strings.Contains(name, "3"):
		return "airpodspro3"
	case strings.Contains(name, "airpods") && strings.Contains(name, "pro"):
		return "airpodspro"
	case strings.Contains(name, "airpods"):
		return "airpods"
	case strings.Contains(name, "beats"):
		return "beats.headphones"
	default:
		return "headphones"
	}
}

// plainHeadphoneSymbol is the fallback the UI draws when the specific one
// is unavailable.
func plainHeadphoneSymbol(part HeadphonePart) string {
	if part == HeadphoneCase {
		return "airpods.chargingcase"
	}
	return "headphones"
}
