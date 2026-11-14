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
// falls back the way the drawn UI always has: the headset to the plain
// headphones, the case to the plain case, and a pod to nothing at all so
// the caller draws its own pod.
func HeadphoneIcon(model string, part HeadphonePart, size int) []byte {
	name := headphoneSymbol(model, part)
	if out := systemSymbol(name, size); out != nil {
		return out
	}
	fallback := fallbackHeadphoneSymbol(part)
	if fallback == "" || fallback == name {
		return nil
	}
	return systemSymbol(fallback, size)
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

// fallbackHeadphoneSymbol is the symbol tried when a part's own symbol is
// missing. The pods have none: their badge keeps the self-drawn pod it had
// before, so an absent SF Symbol must reach the caller as no image.
func fallbackHeadphoneSymbol(part HeadphonePart) string {
	switch part {
	case HeadphoneWhole:
		return "headphones"
	case HeadphoneCase:
		return "airpods.chargingcase"
	}
	return ""
}
