//go:build darwin

package platform

import "testing"

func TestHeadphoneSymbol(t *testing.T) {
	for name, want := range map[string]string{
		"AirPods Pro":     "airpodspro",
		"AirPods Pro 3":   "airpodspro3",
		"AirPods Max":     "airpodsmax",
		"AirPods":         "airpods",
		"Beats Studio":    "beats.headphones",
		"Sony WH-1000XM5": "headphones",
	} {
		if got := headphoneSymbol(name, HeadphoneWhole); got != want {
			t.Errorf("%s: symbol %q, want %q", name, got, want)
		}
	}
	parts := []struct {
		part HeadphonePart
		want string
	}{
		{HeadphoneLeft, "airpod.left"},
		{HeadphoneRight, "airpod.right"},
		{HeadphoneCase, "airpods.chargingcase"},
	}
	for _, c := range parts {
		if got := headphoneSymbol("AirPods", c.part); got != c.want {
			t.Errorf("part %d: symbol %q, want %q", c.part, got, c.want)
		}
	}
	if got := headphoneSymbol("AirPods Pro", HeadphoneCase); got != "airpodspro.chargingcase.wireless" {
		t.Errorf("pro case symbol %q", got)
	}
}

// TestHeadphoneIconFallback: a symbol the system lacks falls back only the
// way the drawn UI always did: the whole headset to the plain headphones,
// the case to the plain case, and a pod to no icon at all, so the caller
// draws its own pod exactly as before.
func TestHeadphoneIconFallback(t *testing.T) {
	for _, c := range []struct {
		part HeadphonePart
		want string
	}{
		{HeadphoneWhole, "headphones"},
		{HeadphoneCase, "airpods.chargingcase"},
		{HeadphoneLeft, ""},
		{HeadphoneRight, ""},
	} {
		if got := fallbackHeadphoneSymbol(c.part); got != c.want {
			t.Errorf("part %d: fallback %q, want %q", c.part, got, c.want)
		}
	}
}
