package editor

import (
	"math"

	"rakko/internal/board/doc"
)

var (
	FillStyles  = []string{"none", "semi", "solid", "pattern"}
	StrokeSizes = []string{"s", "m", "l", "xl"}
	DashStyles  = []string{"solid", "dash", "dot"}
	Opacities   = []float64{0.25, 0.5, 0.75, 1.0}
	FontSizes   = []int{14, 18, 24, 32, 48}
	Fonts       = []string{"sketch", "sans", "serif", "mono"}
	PenWidths   = []float64{1, 2, 3, 5, 8, 12}
)

// PanelState describes the adaptive property panel sections and current values
// for either the active selection or the active drawing tool.
type PanelState struct {
	Visible        bool
	HasSelection   bool
	SelectionCount int

	ShowColor       bool
	ShowFillTab     bool
	ShowGeoShape    bool
	ShowFillStyle   bool
	ShowStrokeSize  bool
	ShowDash        bool
	ShowOpacity     bool
	ShowArrowProps  bool
	ShowLineSpline  bool
	ShowDrawProps   bool
	ShowEraserProps bool
	ShowFont        bool
	ShowAlign       bool
	ShowVAlign      bool

	Color      string
	FillColor  string
	Fill       string
	Size       string
	Dash       string
	Opacity    float64
	Geo        string
	Font       string
	FontSize   int
	Align      string
	VAlign     string
	Bold       bool
	Italic     bool
	Underline  bool
	ArrowRoute string
	HeadStart  string
	HeadEnd    string
	Spline     string
	PenKind    string
	PenWidth   float64
	EraserMode EraserMode
}

func (e *Editor) styledTargets() []*doc.Shape {
	if len(e.sel) == 0 {
		return nil
	}
	seen := make(map[doc.ID]bool, len(e.sel))
	out := make([]*doc.Shape, 0, len(e.sel))
	var add func(id doc.ID)
	add = func(id doc.ID) {
		if seen[id] {
			return
		}
		seen[id] = true
		s := e.Doc.Shapes[id]
		if s == nil {
			return
		}
		if s.Type == doc.TypeGroup {
			for _, cid := range e.Children(id) {
				add(cid)
			}
			return
		}
		out = append(out, s)
	}
	for _, id := range e.sel {
		add(id)
	}
	return out
}

func shapeArrowRoute(s *doc.Shape) string {
	if s.Kind == "elbow" {
		return "elbow"
	}
	if math.Abs(s.Bend) >= 1 {
		return "curve"
	}
	return "straight"
}

func nextArrowRoute(n Style) string {
	if n.ArrowKind == "elbow" {
		return "elbow"
	}
	if math.Abs(n.ArrowBend) >= 1 {
		return "curve"
	}
	return "straight"
}

func shapeAlignValue(s *doc.Shape) string {
	if s.Type == doc.TypeText {
		if s.TextAlign != "" {
			return s.TextAlign
		}
	}
	if s.Align != "" {
		return s.Align
	}
	return "left"
}

// PanelState computes which property controls apply to the current selection or active tool.
func (e *Editor) PanelState() PanelState {
	n := e.Next
	st := PanelState{
		Color:      n.Stroke,
		FillColor:  n.FillColor,
		Fill:       n.Fill,
		Size:       n.Size,
		Dash:       n.Dash,
		Opacity:    n.Opacity,
		Geo:        n.Geo,
		Font:       n.Font,
		FontSize:   n.FontSize,
		Align:      n.Align,
		VAlign:     n.VAlign,
		Bold:       n.Bold,
		Italic:     n.Italic,
		Underline:  n.Underline,
		ArrowRoute: nextArrowRoute(n),
		HeadStart:  n.HeadStart,
		HeadEnd:    n.HeadEnd,
		Spline:     n.Spline,
		PenKind:    n.PenKind,
		PenWidth:   n.PenWidth,
		EraserMode: n.EraserMode,
	}

	targets := e.styledTargets()
	if len(targets) > 0 {
		st.HasSelection = true
		st.SelectionCount = len(targets)
		first := true
		for _, s := range targets {
			switch s.Type {
			case doc.TypeGeo:
				st.ShowColor = true
				st.ShowFillTab = true
				st.ShowGeoShape = true
				st.ShowFillStyle = true
				st.ShowStrokeSize = true
				st.ShowDash = true
				st.ShowOpacity = true
				st.ShowFont = true
				st.ShowAlign = true
				st.ShowVAlign = true
			case doc.TypeArrow:
				st.ShowColor = true
				st.ShowStrokeSize = true
				st.ShowDash = true
				st.ShowOpacity = true
				st.ShowArrowProps = true
				st.ShowFont = true
				st.ShowAlign = true
			case doc.TypeLine:
				st.ShowColor = true
				st.ShowStrokeSize = true
				st.ShowDash = true
				st.ShowOpacity = true
				st.ShowLineSpline = true
			case doc.TypeDraw:
				st.ShowColor = true
				st.ShowDash = true
				st.ShowOpacity = true
				st.ShowDrawProps = true
				if s.IsClosed {
					st.ShowFillTab = true
					st.ShowFillStyle = true
				}
			case doc.TypeHighlight:
				st.ShowColor = true
				st.ShowStrokeSize = true
				st.ShowOpacity = true
			case doc.TypeText:
				st.ShowColor = true
				st.ShowOpacity = true
				st.ShowFont = true
				st.ShowAlign = true
			case doc.TypeNote:
				st.ShowColor = true
				st.ShowOpacity = true
				st.ShowFont = true
				st.ShowAlign = true
				st.ShowVAlign = true
			case doc.TypeImage:
				st.ShowOpacity = true
			case doc.TypeFrame:
				st.ShowOpacity = true
			}
			if first {
				first = false
				if s.Color != "" {
					st.Color = s.Color
				}
				if s.FillColor != "" {
					st.FillColor = s.FillColor
				} else if s.Color != "" {
					st.FillColor = s.Color
				}
				if s.Fill != "" {
					st.Fill = s.Fill
				}
				if s.Size != "" {
					st.Size = s.Size
				}
				if s.Dash != "" {
					st.Dash = s.Dash
				}
				if s.Opacity > 0 {
					st.Opacity = s.Opacity
				}
				if s.Geo != "" {
					st.Geo = normalizeGeoKind(s.Geo)
				}
				if s.Font != "" {
					st.Font = s.Font
				}
				if s.FontSize > 0 {
					st.FontSize = s.FontSize
				}
				st.Align = shapeAlignValue(s)
				if s.VAlign != "" {
					st.VAlign = s.VAlign
				}
				st.Bold, st.Italic, st.Underline = s.Bold, s.Italic, s.Underline
				st.ArrowRoute = shapeArrowRoute(s)
				if s.HeadStart != "" {
					st.HeadStart = s.HeadStart
				}
				if s.HeadEnd != "" {
					st.HeadEnd = s.HeadEnd
				}
				if s.Spline != "" {
					st.Spline = s.Spline
				}
				if s.PenKind != "" {
					st.PenKind = s.PenKind
				}
				if s.PenWidth > 0 {
					st.PenWidth = s.PenWidth
				}
			}
		}
		if st.ShowAlign && !st.ShowVAlign && targets[0].Type == doc.TypeText && targets[0].TextAlign == "" && st.Align == "" {
			st.Align = "left"
		}
		st.Visible = st.ShowColor || st.ShowOpacity || st.ShowEraserProps
		return st
	}

	switch e.Tool {
	case ToolGeo:
		st.ShowColor = true
		st.ShowFillTab = true
		st.ShowGeoShape = true
		st.ShowFillStyle = true
		st.ShowStrokeSize = true
		st.ShowDash = true
		st.ShowOpacity = true
		st.ShowFont = true
		st.ShowAlign = true
		st.ShowVAlign = true
	case ToolArrow:
		st.ShowColor = true
		st.ShowStrokeSize = true
		st.ShowDash = true
		st.ShowOpacity = true
		st.ShowArrowProps = true
		st.ShowFont = true
		st.ShowAlign = true
	case ToolLine:
		st.ShowColor = true
		st.ShowStrokeSize = true
		st.ShowDash = true
		st.ShowOpacity = true
		st.ShowLineSpline = true
	case ToolDraw:
		st.ShowColor = true
		st.ShowDash = true
		st.ShowOpacity = true
		st.ShowDrawProps = true
	case ToolHighlight:
		st.ShowColor = true
		st.ShowStrokeSize = true
		st.ShowOpacity = true
	case ToolEraser:
		st.ShowEraserProps = true
	case ToolText:
		st.ShowColor = true
		st.ShowOpacity = true
		st.ShowFont = true
		st.ShowAlign = true
		st.Align = n.TextAlign
		if st.Align == "" {
			st.Align = "left"
		}
	case ToolNote:
		st.ShowColor = true
		st.ShowOpacity = true
		st.ShowFont = true
		st.ShowAlign = true
		st.ShowVAlign = true
	}
	st.Visible = st.ShowColor || st.ShowOpacity || st.ShowEraserProps
	return st
}

func (e *Editor) mutateStyled(fn func(s *doc.Shape)) {
	targets := e.styledTargets()
	changed := false
	for _, t := range targets {
		if t.Locked {
			continue
		}
		e.update(t.ID, fn)
		changed = true
	}
	if changed {
		e.commit()
	}
}

func (e *Editor) SetPropStroke(hex string) {
	if hex == "" {
		return
	}
	e.Next.Stroke = hex
	e.mutateStyled(func(c *doc.Shape) {
		c.Color = hex
	})
}

func (e *Editor) SetPropFillColor(hex string) {
	if hex == "" {
		return
	}
	e.Next.FillColor = hex
	if e.Next.Fill == "" || e.Next.Fill == "none" {
		e.Next.Fill = "semi"
	}
	e.mutateStyled(func(c *doc.Shape) {
		c.FillColor = hex
		if c.Fill == "" || c.Fill == "none" {
			c.Fill = "semi"
		}
	})
}

func (e *Editor) SetPropFill(fill string) {
	switch fill {
	case "none", "semi", "solid", "pattern":
	default:
		return
	}
	e.Next.Fill = fill
	e.mutateStyled(func(c *doc.Shape) {
		c.Fill = fill
	})
}

func (e *Editor) SetPropSize(size string) {
	switch size {
	case "s", "m", "l", "xl":
	default:
		return
	}
	e.Next.Size = size
	e.mutateStyled(func(c *doc.Shape) {
		c.Size = size
	})
}

func (e *Editor) SetPropDash(dash string) {
	switch dash {
	case "solid", "dash", "dot", "none":
	default:
		return
	}
	e.Next.Dash = dash
	e.mutateStyled(func(c *doc.Shape) {
		c.Dash = dash
	})
}

func (e *Editor) SetPropOpacity(op float64) {
	if op <= 0 || op > 1 {
		return
	}
	e.Next.Opacity = op
	e.mutateStyled(func(c *doc.Shape) {
		c.Opacity = op
	})
}

func (e *Editor) SetPropFont(font string) {
	switch font {
	case "sketch", "sans", "serif", "mono":
	default:
		return
	}
	e.Next.Font = font
	e.mutateStyled(func(c *doc.Shape) {
		c.Font = font
		e.fitLabel(c)
	})
}

func (e *Editor) SetPropFontSize(size int) {
	if size <= 0 {
		return
	}
	e.Next.FontSize = size
	e.mutateStyled(func(c *doc.Shape) {
		c.FontSize = size
		e.fitLabel(c)
	})
}

func (e *Editor) SetPropAlign(align string) {
	switch align {
	case "left", "center", "right":
	default:
		return
	}
	if len(e.sel) == 0 && e.Tool == ToolText {
		e.Next.TextAlign = align
		return
	}
	e.Next.Align = align
	e.mutateStyled(func(c *doc.Shape) {
		if c.Type == doc.TypeText {
			c.TextAlign = align
			e.Next.TextAlign = align
			return
		}
		c.Align = align
	})
}

func (e *Editor) SetPropVAlign(valign string) {
	switch valign {
	case "top", "middle", "bottom":
	default:
		return
	}
	e.Next.VAlign = valign
	e.mutateStyled(func(c *doc.Shape) {
		c.VAlign = valign
	})
}

func (e *Editor) TogglePropBold() {
	next := !e.PanelState().Bold
	e.Next.Bold = next
	e.mutateStyled(func(c *doc.Shape) {
		c.Bold = next
		e.fitLabel(c)
	})
}

func (e *Editor) TogglePropItalic() {
	next := !e.PanelState().Italic
	e.Next.Italic = next
	e.mutateStyled(func(c *doc.Shape) {
		c.Italic = next
		e.fitLabel(c)
	})
}

func (e *Editor) TogglePropUnderline() {
	next := !e.PanelState().Underline
	e.Next.Underline = next
	e.mutateStyled(func(c *doc.Shape) {
		c.Underline = next
	})
}

func (e *Editor) SetPropGeo(kind string) {
	norm := normalizeGeoKind(kind)
	if !IsValidGeoKind(norm) {
		return
	}
	e.Next.Geo = norm
	e.mutateStyled(func(c *doc.Shape) {
		if c.Type == doc.TypeGeo {
			c.Geo = norm
			e.fitLabel(c)
		}
	})
}

func (e *Editor) SetPropArrowHeads(start, end string) {
	if start != "" && IsValidArrowHead(start) {
		e.Next.HeadStart = start
	}
	if end != "" && IsValidArrowHead(end) {
		e.Next.HeadEnd = end
	}
	e.mutateStyled(func(c *doc.Shape) {
		if c.Type != doc.TypeArrow {
			return
		}
		if start != "" && IsValidArrowHead(start) {
			c.HeadStart = start
		}
		if end != "" && IsValidArrowHead(end) {
			c.HeadEnd = end
		}
	})
}

func (e *Editor) SetPropArrowRoute(route string) {
	switch route {
	case "straight":
		e.Next.ArrowKind = "arc"
		e.Next.ArrowBend = 0
	case "curve":
		e.Next.ArrowKind = "arc"
		e.Next.ArrowBend = 40
	case "elbow":
		e.Next.ArrowKind = "elbow"
		e.Next.ArrowBend = 0
	default:
		return
	}
	e.mutateStyled(func(c *doc.Shape) {
		if c.Type != doc.TypeArrow {
			return
		}
		switch route {
		case "straight":
			c.Kind = "arc"
			c.Bend = 0
		case "curve":
			c.Kind = "arc"
			if math.Abs(c.Bend) < 1 {
				c.Bend = math.Max(24, c.Start.Dist(c.End)*0.18)
			}
		case "elbow":
			c.Kind = "elbow"
			c.Bend = 0
		}
	})
}

func (e *Editor) SetPropLineSpline(spline string) {
	switch spline {
	case "line", "cubic":
	default:
		return
	}
	e.Next.Spline = spline
	e.mutateStyled(func(c *doc.Shape) {
		if c.Type == doc.TypeLine {
			c.Spline = spline
		}
	})
}

func (e *Editor) SetPropPenKind(kind string) {
	e.SetPenKind(kind)
	switch kind {
	case "normal", "pen", "marker":
	default:
		return
	}
	e.mutateStyled(func(c *doc.Shape) {
		if c.Type == doc.TypeDraw {
			c.PenKind = kind
			c.IsPen = kind == "pen"
		}
	})
}

func (e *Editor) SetPropPenWidth(w float64) {
	if w <= 0 {
		return
	}
	e.Next.PenWidth = w
	e.mutateStyled(func(c *doc.Shape) {
		if c.Type == doc.TypeDraw {
			c.PenWidth = w
		}
	})
}
