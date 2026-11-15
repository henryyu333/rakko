package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
)

// TestWindowsSettingsContentSitsUnderTheCaption: Windows draws its caption
// controls in a strip above the page, and the strip and content share one
// frame. Content built as a sibling instead draws over the strip, and a
// scrolled page may reach past the window bottom.
func TestWindowsSettingsContentSitsUnderTheCaption(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	const caption = 32
	a := &shelf{
		dataDir: t.TempDir(), settings: settings.Default(), locale: "en",
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
	v := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
	v.SetTitleBar(ui.TitleBar{Height: caption})
	v.SetLocale("en")
	v.Frame()

	for _, x := range []int{settingsWidth / 2, settingsWidth - 40} {
		if c := v.Image().RGBAAt(x, 20); c.R > 4 || c.G > 4 || c.B > 4 {
			t.Fatalf("pixel (%d,20) is %v: the caption strip is not drawn above the page", x, c)
		}
	}
	if r, ok := v.Find(a.appLoc().General()); !ok {
		t.Fatal("the sidebar destination is missing")
	} else if r.Y < caption {
		t.Fatalf("sidebar starts at y=%.0f, over the caption strip (%.0f)", r.Y, r.Y+caption)
	}
	// Scroll to the end of a long page: the last line must sit inside the
	// window. A viewport that reaches past the window bottom puts it below.
	a.settingsPage = settingsPageNotch
	v.Frame()
	for range 20 {
		v.Scroll(settingsWidth*3/4, settingsHeight/2, 0, 400)
		v.Frame()
	}
	captionText := a.appLoc().SettingsSnapCaptionWin()
	if r, ok := v.Find(captionText); !ok {
		t.Fatalf("the last line %q is missing after scrolling", captionText)
	} else if r.Y+r.H > float32(settingsHeight-6) {
		t.Fatalf("the last line ends at y=%.0f, past the window bottom %d", r.Y+r.H, settingsHeight)
	}
}
