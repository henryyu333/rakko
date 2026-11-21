package locales

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

// macOnlyText is wording that belongs to the macOS interface: modifier
// glyphs, Finder/Trash/Dock names, System Settings and Mac.
var macOnlyText = regexp.MustCompile(`⌘|⌃|⌥|⇧|↩|⏎|\.app\b|访达|Finder|隔空投送|AirDrop|菜单栏|menu bar|Menu Bar|快速查看|Quick Look|废纸篓|Trash|(^|[^A-Za-z])Dock([^A-Za-z]|$)|系统设置|System Settings|Open at Login|(^|[^A-Za-z])Mac([^A-Za-z]|$)|macOS|减少透明度|Reduce Transparency`)

// notchText is the shell's name on macOS. Windows calls the same shape a
// simulated notch or the top capsule, so a shared entry that says 刘海 or
// notch needs its own Windows wording, but "模拟小刘海" / "Simulated Notch"
// is already that wording.
var notchText = regexp.MustCompile(`刘海|(^|[^A-Za-z])[Nn]otch([^A-Za-z]|$)`)

// macOnlySurfaces are pages and entry points the Windows build never opens:
// the permission guide, the bundle move that only exists in a macOS .app,
// and macOS-only AirDrop controls. Their entries stay as macOS says them.
var macOnlySurfaces = map[string]bool{
	"permStep":                      true,
	"permScreenDetail":              true,
	"permAccessibilityTitle":        true,
	"permAccessibilityGloss":        true,
	"permAccessibilityDetail":       true,
	"onboardingMoveWhyTranslocated": true,
	"itemAirDrop":                   true,
	"cardAirDrop":                   true,
}

func readCatalog(t *testing.T, name string) map[string]string {
	t.Helper()
	raw, err := files.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

// TestWindowsWordingKeepsMacTextOut draws the line between the two
// platforms: a key ending in Win is what Windows shows, so it may not say a
// macOS-only name or glyph, and every shared entry that does say one needs
// such a key beside it.
func TestWindowsWordingKeepsMacTextOut(t *testing.T) {
	for _, name := range []string{"zh-Hans.json", "en.json"} {
		catalog := readCatalog(t, name)
		for key, text := range catalog {
			if strings.HasSuffix(key, "Win") {
				if macOnlyText.MatchString(text) {
					t.Errorf("%s: %s still says macOS wording: %q", name, key, text)
				}
				if _, ok := catalog[strings.TrimSuffix(key, "Win")]; !ok {
					t.Errorf("%s: %s has no macOS key beside it", name, key)
				}
				continue
			}
			if macOnlySurfaces[key] || !(macOnlyText.MatchString(text) || notchText.MatchString(text)) {
				continue
			}
			if _, ok := catalog[key+"Win"]; !ok {
				t.Errorf("%s: %s says macOS wording but has no Windows key: %q", name, key, text)
			}
		}
	}
}
