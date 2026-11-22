package main

import (
	"strconv"

	"github.com/egoist/mygo"

	"rakko/internal/platform"
	"rakko/internal/settings"
	"rakko/locales"
)

// displayFriendlyNames reads OS-level monitor names when mygo.Display.Label
// is only a device path (Windows). Tests stub it to keep headless display
// lists deterministic.
var displayFriendlyNames = platform.DisplayFriendlyNames

func (a *shelf) followsPointerDisplay() bool {
	return settings.NormalizeDisplay(a.settings.Display) == settings.DisplayFollow
}

// resolveTargetDisplay picks which display the shelf occupies given the user's
// "display" preference:
//   - "" (follow mouse): delegates to fallback(p) (displayNearest)
//   - "main": primary display (or first display if none marked Primary)
//   - "<id>": display with that stable id, falling back to primary while unplugged
func resolveTargetDisplay(displays []mygo.Display, setting string, p mygo.Point, fallback func(mygo.Point) mygo.Display) mygo.Display {
	mode := settings.NormalizeDisplay(setting)
	if mode == settings.DisplayFollow {
		return fallback(p)
	}
	if len(displays) == 0 {
		return fallback(p)
	}
	primary := primaryDisplayIn(displays)
	if mode == settings.DisplayMain {
		return primary
	}
	wantID, err := strconv.ParseInt(mode, 10, 64)
	if err != nil {
		return primary
	}
	migrated := displayIDMigrator(wantID)
	for _, d := range displays {
		if d.ID == wantID || d.ID == migrated {
			return d
		}
	}
	return primary
}

func primaryDisplayIn(displays []mygo.Display) mygo.Display {
	for _, d := range displays {
		if d.Primary {
			return d
		}
	}
	return displays[0]
}

type displayOption struct {
	key       string
	name      string
	suffix    string
	a11yLabel string
	selected  bool
}

// displayPosKind classifies where another display sits relative to the primary
// display so identically named monitors remain distinguishable.
type displayPosKind int

const (
	displayPosPrimary displayPosKind = iota
	displayPosAbove
	displayPosBelow
	displayPosLeft
	displayPosRight
)

func displayPosRelative(primary, other mygo.Display) displayPosKind {
	if other.ID == primary.ID || other.Primary {
		return displayPosPrimary
	}
	pb, ob := primary.Bounds, other.Bounds
	switch {
	case ob.Y+ob.Height <= pb.Y:
		return displayPosAbove
	case ob.Y >= pb.Y+pb.Height:
		return displayPosBelow
	case ob.X+ob.Width <= pb.X:
		return displayPosLeft
	case ob.X >= pb.X+pb.Width:
		return displayPosRight
	}
	// Fallback for overlapping or corner-touching arrangements: compare
	// normalized centre delta along each axis.
	dx := float64(ob.X+ob.Width/2) - float64(pb.X+pb.Width/2)
	dy := float64(ob.Y+ob.Height/2) - float64(pb.Y+pb.Height/2)
	nx := absFloat(dx) / float64(max(1, pb.Width+ob.Width))
	ny := absFloat(dy) / float64(max(1, pb.Height+ob.Height))
	if ny >= nx {
		if dy < 0 {
			return displayPosAbove
		}
		return displayPosBelow
	}
	if dx < 0 {
		return displayPosLeft
	}
	return displayPosRight
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func displayPosSuffix(kind displayPosKind, l locales.Messages) string {
	switch kind {
	case displayPosPrimary:
		return l.SettingsDisplayPosPrimary()
	case displayPosAbove:
		return l.SettingsDisplayPosAbove()
	case displayPosBelow:
		return l.SettingsDisplayPosBelow()
	case displayPosLeft:
		return l.SettingsDisplayPosLeft()
	case displayPosRight:
		return l.SettingsDisplayPosRight()
	}
	return ""
}

// displayOptions builds the rows of the "显示在" group. Returns nil when
// fewer than two displays are connected so the group stays hidden on single-
// screen setups.
func displayOptions(displays []mygo.Display, friendlyNames map[int64]string, setting string, l locales.Messages) []displayOption {
	if len(displays) <= 1 {
		return nil
	}
	mode := settings.NormalizeDisplay(setting)
	primary := primaryDisplayIn(displays)

	opts := make([]displayOption, 0, 2+len(displays))
	opts = append(opts,
		displayOption{
			key:       settings.DisplayFollow,
			name:      l.SettingsDisplayFollow(),
			a11yLabel: l.SettingsDisplayFollow(),
			selected:  mode == settings.DisplayFollow,
		},
		displayOption{
			key:       settings.DisplayMain,
			name:      l.SettingsDisplayMain(),
			a11yLabel: l.SettingsDisplayMain(),
			selected:  mode == settings.DisplayMain,
		},
	)
	for _, d := range displays {
		key := strconv.FormatInt(d.ID, 10)
		name := friendlyNames[d.ID]
		if name == "" {
			name = d.Label
		}
		if name == "" {
			name = l.SettingsDisplayUnknown()
		}
		suffix := displayPosSuffix(displayPosRelative(primary, d), l)
		a11y := name
		if suffix != "" {
			a11y += " " + suffix
		}
		opts = append(opts, displayOption{
			key:       key,
			name:      name,
			suffix:    suffix,
			a11yLabel: a11y,
			selected:  mode == key,
		})
	}
	return opts
}

type displayOptionsCache struct {
	valid    bool
	displays []mygo.Display
	friendly map[int64]string
	setting  string
	langKey  string
	opts     []displayOption
}

func (a *shelf) refreshDisplayOptions(l locales.Messages) {
	displays := screenDisplays()
	var friendly map[int64]string
	var opts []displayOption
	if len(displays) > 1 {
		friendly = displayFriendlyNames()
		opts = displayOptions(displays, friendly, a.settings.Display, l)
	}
	a.displayOpts = displayOptionsCache{
		valid:    true,
		displays: displays,
		friendly: friendly,
		setting:  settings.NormalizeDisplay(a.settings.Display),
		langKey:  l.SettingsDisplayFollow(),
		opts:     opts,
	}
}

func (a *shelf) displaySettingOptions(l locales.Messages) []displayOption {
	if !a.displayOpts.valid {
		a.refreshDisplayOptions(l)
		return a.displayOpts.opts
	}
	mode := settings.NormalizeDisplay(a.settings.Display)
	langKey := l.SettingsDisplayFollow()
	if a.displayOpts.setting != mode || a.displayOpts.langKey != langKey {
		a.displayOpts.setting = mode
		a.displayOpts.langKey = langKey
		if len(a.displayOpts.displays) > 1 {
			a.displayOpts.opts = displayOptions(a.displayOpts.displays, a.displayOpts.friendly, a.settings.Display, l)
		} else {
			a.displayOpts.opts = nil
		}
	}
	return a.displayOpts.opts
}

func (a *shelf) setDisplay(choice string) {
	choice = settings.NormalizeDisplay(choice)
	if settings.NormalizeDisplay(a.settings.Display) == choice {
		return
	}
	next := a.settings
	next.Display = choice
	if !a.applySettings(next) {
		return
	}
	a.refreshDisplayOptions(a.appLoc())
	if a.win != nil {
		a.place(cursorPoint())
		a.syncMousePassThrough()
		a.win.Invalidate()
	} else {
		a.placeAt(cursorPoint())
	}
	a.syncIdleIslands()
}
