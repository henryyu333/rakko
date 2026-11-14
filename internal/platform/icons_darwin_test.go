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
	if got := plainHeadphoneSymbol(HeadphoneCase); got != "airpods.chargingcase" {
		t.Errorf("case fallback %q", got)
	}
}
