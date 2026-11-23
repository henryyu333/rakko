package platform

import "time"

// Cross-platform types, enums, and pure helpers shared between darwin
// and windows implementations.

// ClipboardKind is what one clipboard change should become. Not-ready is
// distinct from skip: the contents are still being published and the caller
// must wait, while a skipped change will never be recorded.
type ClipboardKind int

const (
	ClipboardNotReady ClipboardKind = iota // no type declared yet
	ClipboardSkip                          // private, files, or nothing usable
	ClipboardText
	ClipboardImage
)

// classifyClipboard decides what a clipboard change should become from its
// declared formats in source-preference order, delegating format matching to
// the platform's equality or case-folding matcher.
func classifyClipboard(types []string, match func(name string, set []string) bool, priv, files, texts, images []string) ClipboardKind {
	if len(types) == 0 {
		return ClipboardNotReady
	}
	for _, t := range types {
		if match(t, priv) {
			return ClipboardSkip
		}
	}
	for _, t := range types {
		if match(t, files) {
			return ClipboardSkip
		}
	}
	for _, t := range types {
		switch {
		case match(t, texts):
			return ClipboardText
		case match(t, images):
			return ClipboardImage
		}
	}
	return ClipboardSkip
}

// ImageFormat selects the encoding a conversion or render writes.
type ImageFormat int

const (
	// FormatJPEG encodes JPEG.
	FormatJPEG ImageFormat = iota
	// FormatPNG encodes lossless PNG.
	FormatPNG
	// FormatHEIC encodes HEIC.
	FormatHEIC
)

// SearchHit is one search result across Spotlight (macOS) or Windows Search.
type SearchHit struct {
	Path       string
	Name       string
	App        bool
	Folder     bool
	LastUsed   time.Time
	TargetPath string // resolved disk path for Windows apps whose Path is shell:AppsFolder\...
	StoreApp   bool   // true for Microsoft Store / UWP packaged apps on Windows
}

// SearchScope is what a file/app search looks at.
type SearchScope int

const (
	ScopeAll   SearchScope = iota // names and text content, everything
	ScopeApps                     // application entries/bundles, by name only
	ScopeFiles                    // names and text content, applications left out
)

// wants reports whether a hit belongs to the scope's results.
func (s SearchScope) wants(h SearchHit) bool {
	switch s {
	case ScopeApps:
		return h.App
	case ScopeFiles:
		return !h.App
	}
	return true
}

// ScreenWindow is one ordinary on-screen window of another process.
type ScreenWindow struct {
	Number int
	PID    int
	Bounds Rect
}

// BatteryStatus is what the system battery reports now.
type BatteryStatus struct {
	Percent  int
	Charging bool
	External bool
}

// PowerAssertionKind names the behaviour a power assertion holds.
type PowerAssertionKind int

const (
	// PowerAssertSystemSleep blocks system idle sleep; the display still sleeps.
	PowerAssertSystemSleep PowerAssertionKind = iota
	// PowerAssertDisplaySleep also keeps the display on.
	PowerAssertDisplaySleep
)

// PowerAssertionID is the handle an assertion creation returns; zero means
// no assertion.
type PowerAssertionID uintptr

// RunningApp is one entry of the app switcher's snapshot.
type RunningApp struct {
	AppID string
	Name  string
	PID   int
}

// AudioOutput is one device that can play sound.
type AudioOutput struct {
	Name      string
	UID       string
	Bluetooth bool
}

// HeadphonePart is which part of a headset an icon stands for.
type HeadphonePart int

const (
	HeadphoneWhole HeadphonePart = iota
	HeadphoneLeft
	HeadphoneRight
	HeadphoneCase
)

// BatteryLevel is one charge percentage a headset reports.
type BatteryLevel struct {
	Part HeadphonePart
	Pct  int
}

// clampVolume keeps a volume inside the 0..1 scalar range. NaN becomes 0 so a
// calculation slip cannot mute the system by accident.
func clampVolume(v float32) float32 {
	if v != v || v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
