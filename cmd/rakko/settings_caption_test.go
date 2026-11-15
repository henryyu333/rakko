package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/settings"
)

// TestWindowsSettingsLayoutFillsWindowAndClearsCaption verifies that the
// Windows Settings sidebar and page card fill the window without the old 32 DIP
// black strip pushing content down, keep symmetric top/right/bottom margins,
// keep hero text/art clear of the top-right caption controls, and allow
// scrolling cleanly to the end of long pages.
func TestWindowsSettingsLayoutFillsWindowAndClearsCaption(t *testing.T) {
	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	const caption = 32
	for _, tc := range []struct {
		name      string
		winRadius float32
		wantGap   int
	}{
		{name: "win10-square", winRadius: 0, wantGap: 8},
		{name: "win11-rounded", winRadius: 8, wantGap: 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &shelf{
				dataDir: t.TempDir(), settings: settings.Default(), locale: "en",
				settingsWinRadius: tc.winRadius,
				thumbnails:        map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
				appNames: map[string]string{},
			}
			v := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
			v.SetTitleBar(ui.TitleBar{Height: caption, Right: 138})
			v.SetLocale("en")
			v.Frame()

			img := v.Image()
			// Outer black frame stays pure black inside the card margin.
			if c := img.RGBAAt(settingsWidth/2, 2); c.R > 4 || c.G > 4 || c.B > 4 {
				t.Fatalf("top margin (410,2) = %v, want black frame", c)
			}
			// Card panel (#0E0F11) begins right at wantGap (not pushed down 32 DIPs).
			if c := img.RGBAAt(settingsWidth/2, tc.wantGap+2); c.R != 14 || c.G != 15 || c.B != 17 {
				t.Fatalf("card top (410,%d) = %v, want panel #0E0F11", tc.wantGap+2, c)
			}
			// Bottom and right edges share the exact same margin.
			if c := img.RGBAAt(settingsWidth/2, settingsHeight-tc.wantGap-2); c.R != 14 || c.G != 15 || c.B != 17 {
				t.Fatalf("card bottom (410,%d) = %v, want panel #0E0F11", settingsHeight-tc.wantGap-2, c)
			}
			if c := img.RGBAAt(settingsWidth/2, settingsHeight-2); c.R > 4 || c.G > 4 || c.B > 4 {
				t.Fatalf("bottom margin (410,%d) = %v, want black frame", settingsHeight-2, c)
			}
			if c := img.RGBAAt(settingsWidth-tc.wantGap-2, settingsHeight/2); c.R != 14 || c.G != 15 || c.B != 17 {
				t.Fatalf("card right (%d,%d) = %v, want panel #0E0F11", settingsWidth-tc.wantGap-2, settingsHeight/2, c)
			}
			if c := img.RGBAAt(settingsWidth-2, settingsHeight/2); c.R > 4 || c.G > 4 || c.B > 4 {
				t.Fatalf("right margin (%d,%d) = %v, want black frame", settingsWidth-2, settingsHeight/2, c)
			}

			// Sidebar brand & destinations sit cleanly below the 32 DIP top drag strip.
			if r, ok := v.Find(appName); !ok {
				t.Fatal("sidebar brand is missing")
			} else if r.Y < caption {
				t.Fatalf("sidebar brand at y=%.0f overlaps top drag strip (%d)", r.Y, caption)
			}
			// Clicking brand navigates to About (not blocked by top drag strip).
			v.Click(appName)
			v.Frame()
			if a.settingsPage != settingsPageAbout {
				t.Fatalf("clicking brand went to page %v, want About", a.settingsPage)
			}
			a.settingsPage = settingsPageGeneral
			v.Frame()

			// Hero / first group sits cleanly below the caption strip.
			if r, ok := v.Find(a.appLoc().SettingsStartupGroup()); !ok {
				t.Fatal("General startup group is missing")
			} else if r.Y < caption+settingsHeroHeight {
				t.Fatalf("startup group at y=%.0f puts hero above caption clearance (%d)", r.Y, caption)
			}

			// Scroll to the end of a long page: the last line must sit inside the window.
			a.settingsPage = settingsPageNotch
			v.Frame()
			for range 20 {
				v.Scroll(settingsWidth*3/4, settingsHeight/2, 0, 400)
				v.Frame()
			}
			captionText := a.appLoc().SettingsSnapCaptionWin()
			if r, ok := v.Find(captionText); !ok {
				t.Fatalf("the last line %q is missing after scrolling", captionText)
			} else if r.Y+r.H > float32(settingsHeight-tc.wantGap) {
				t.Fatalf("the last line ends at y=%.0f, past card bottom %d", r.Y+r.H, settingsHeight-tc.wantGap)
			}
		})
	}
}

func TestWindowsSettingsThemeWeightsAndSegmented(t *testing.T) {
	if got := settingsThemeFor(false).Font; got != "" {
		t.Fatalf("macOS settingsTheme.Font = %q, want empty", got)
	}
	if got := settingsThemeFor(true).Font; got != "Segoe UI, Microsoft YaHei UI" {
		t.Fatalf("Windows settingsTheme.Font = %q", got)
	}

	orig := forWindows
	forWindows = true
	t.Cleanup(func() { forWindows = orig })

	if got := settingsWeight(600); got != 400 {
		t.Fatalf("settingsWeight(600) on Windows = %d, want 400", got)
	}
	if got := settingsWeight(500); got != 400 {
		t.Fatalf("settingsWeight(500) on Windows = %d, want 400", got)
	}
	if got := settingsWeight(700); got != 700 {
		t.Fatalf("settingsWeight(700) on Windows = %d, want 700", got)
	}
	forWindows = false
	if got := settingsWeight(600); got != 600 {
		t.Fatalf("settingsWeight(600) on macOS = %d, want 600", got)
	}

	// Verify mixed CJK/ASCII segmented control switches selections on Windows.
	forWindows = true
	selected := 0
	var segChanged bool
	v := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(settingsThemeFor(true))
		sg := settingsSegmented(c, "lang", "Language", &selected, "跟随系统", "简体中文", "English")
		if sg.Changed() {
			segChanged = true
		}
	}, 400, 80)
	v.Frame()
	if _, ok := v.Find("English"); !ok {
		t.Fatal("English segment missing")
	}
	v.Click("English")
	if !segChanged || selected != 2 {
		t.Fatalf("clicking English segment gave changed=%v selected=%d, want true / 2", segChanged, selected)
	}
}
