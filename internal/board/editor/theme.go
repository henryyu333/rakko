package editor

import (
	"fmt"
	"strconv"
	"strings"

	"rakko/internal/board/doc"
)

// Color is an sRGB color with alpha in [0, 1].
type Color struct {
	R, G, B uint8
	A       float64
}

// Hex parses a "#rrggbb" hex color string.
func Hex(s string) Color {
	if len(s) != 7 || s[0] != '#' {
		return Color{A: 1}
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return Color{A: 1}
	}
	return Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 1}
}

// Alpha returns c with its alpha scaled by a.
func (c Color) Alpha(a float64) Color {
	c.A *= a
	return c
}

// CSS formats c as "#rrggbb".
func (c Color) CSS() string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// Swatches lists the 16 Tern-aligned whiteboard swatch hex codes.
var Swatches = []string{
	"#E8E8EA", "#F87171", "#FBBF24", "#FACC15",
	"#4ADE80", "#34D399", "#22D3EE", "#38BDF8",
	"#60A5FA", "#A78BFA", "#F472B6", "#818CF8",
	"#94A3B8", "#CBD5E1", "#D4D4D8", "#1C1E21",
}

// Palette defines all surface, ink, paper, and overlay colors for a theme.
type Palette struct {
	Dark          bool
	Background    Color
	TopBar        Color
	Bar           Color
	BarHover      Color
	BarActive     Color
	Edge          Color
	Sep           Color
	Ink           Color
	Ink2          Color
	Ink3          Color
	Accent        Color
	AccentFill    Color
	PaperDot      Color
	PaperLine     Color
	Selection     Color
	SelectionFill Color
	BrushFill     Color
	BrushLine     Color
	FrameStroke   Color
	FrameFill     Color
	Snap          Color
	Laser         Color
	MonoInk       Color
	NoteDefault   Color
	NoteText      Color
}

// DarkPalette is Rakko's default dark whiteboard theme aligned with v1186-whiteboard-v2.html.
var DarkPalette = &Palette{
	Dark:          true,
	Background:    Hex("#1C1C1E"),
	TopBar:        Hex("#1C1C1E"),
	Bar:           Color{R: 44, G: 44, B: 46, A: 0.98},
	BarHover:      Color{R: 255, G: 255, B: 255, A: 0.08},
	BarActive:     Color{R: 255, G: 255, B: 255, A: 0.14},
	Edge:          Color{R: 255, G: 255, B: 255, A: 0.10},
	Sep:           Color{R: 255, G: 255, B: 255, A: 0.09},
	Ink:           Hex("#F5F5F7"),
	Ink2:          Color{R: 255, G: 255, B: 255, A: 0.62},
	Ink3:          Color{R: 255, G: 255, B: 255, A: 0.48},
	Accent:        Hex("#0A84FF"),
	AccentFill:    Color{R: 10, G: 132, B: 255, A: 0.18},
	PaperDot:      Color{R: 255, G: 255, B: 255, A: 0.12},
	PaperLine:     Color{R: 255, G: 255, B: 255, A: 0.065},
	Selection:     Hex("#0A84FF"),
	SelectionFill: Color{R: 10, G: 132, B: 255, A: 0.10},
	BrushFill:     Color{R: 10, G: 132, B: 255, A: 0.10},
	BrushLine:     Color{R: 10, G: 132, B: 255, A: 0.70},
	FrameStroke:   Color{R: 255, G: 255, B: 255, A: 0.24},
	FrameFill:     Color{R: 255, G: 255, B: 255, A: 0.03},
	Snap:          Hex("#FF453A"),
	Laser:         Hex("#FF453A"),
	MonoInk:       Hex("#E8E8EA"),
	NoteDefault:   Hex("#FDE68A"),
	NoteText:      Hex("#1C1E21"),
}

// LightPalette is the light whiteboard theme with readable ink and paper contrast.
var LightPalette = &Palette{
	Dark:          false,
	Background:    Hex("#F9FAFB"),
	TopBar:        Hex("#F2F3F5"),
	Bar:           Color{R: 255, G: 255, B: 255, A: 0.96},
	BarHover:      Color{R: 0, G: 0, B: 0, A: 0.05},
	BarActive:     Color{R: 0, G: 0, B: 0, A: 0.10},
	Edge:          Color{R: 0, G: 0, B: 0, A: 0.10},
	Sep:           Color{R: 0, G: 0, B: 0, A: 0.09},
	Ink:           Hex("#1C1E21"),
	Ink2:          Color{R: 28, G: 30, B: 33, A: 0.66},
	Ink3:          Color{R: 28, G: 30, B: 33, A: 0.48},
	Accent:        Hex("#0A84FF"),
	AccentFill:    Color{R: 10, G: 132, B: 255, A: 0.14},
	PaperDot:      Color{R: 28, G: 30, B: 33, A: 0.14},
	PaperLine:     Color{R: 28, G: 30, B: 33, A: 0.075},
	Selection:     Hex("#0A84FF"),
	SelectionFill: Color{R: 10, G: 132, B: 255, A: 0.10},
	BrushFill:     Color{R: 10, G: 132, B: 255, A: 0.10},
	BrushLine:     Color{R: 10, G: 132, B: 255, A: 0.70},
	FrameStroke:   Color{R: 28, G: 30, B: 33, A: 0.24},
	FrameFill:     Color{R: 255, G: 255, B: 255, A: 0.65},
	Snap:          Hex("#E03131"),
	Laser:         Hex("#E03131"),
	MonoInk:       Hex("#1C1E21"),
	NoteDefault:   Hex("#FEF08A"),
	NoteText:      Hex("#1C1E21"),
}

// ResolveColor adapts a stored stroke/fill hex to the active theme and look.
func ResolveColor(hex string, dark bool, look string) Color {
	if look == doc.LookMono {
		if dark {
			return DarkPalette.MonoInk
		}
		return LightPalette.MonoInk
	}
	u := strings.ToUpper(strings.TrimSpace(hex))
	if !dark {
		switch u {
		case "#E8E8EA":
			return Hex("#1C1E21")
		case "#1C1E21":
			return Hex("#E8E8EA")
		}
	}
	if u == "" {
		if dark {
			return DarkPalette.Ink
		}
		return LightPalette.Ink
	}
	return Hex(u)
}

// HighlightColor returns the highlighter color for hex.
func (p *Palette) HighlightColor(hex string) Color {
	u := strings.ToUpper(strings.TrimSpace(hex))
	if u == "" || u == "#E8E8EA" || u == "#1C1E21" {
		return Hex("#FACC15")
	}
	return Hex(u)
}

// NoteFillColor returns the sticky note background color for hex.
func (p *Palette) NoteFillColor(hex string) Color {
	u := strings.ToUpper(strings.TrimSpace(hex))
	if u == "" || u == "#E8E8EA" || u == "#1C1E21" {
		return p.NoteDefault
	}
	return Hex(u)
}
