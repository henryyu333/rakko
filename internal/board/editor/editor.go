// Package editor implements the headless whiteboard state machine: camera,
// coordinate transforms, paper/theme/look preferences, and (in subsequent
// segments) tools, gestures, selection, and display lists. It has zero UI
// toolkit dependencies so all logic can be unit-tested headlessly.
package editor

import (
	"math"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// Camera defines the viewport transform: screen = (page + (X, Y)) * Z.
type Camera struct {
	X, Y, Z float64
}

const (
	// MinZoom is the minimum camera zoom (5%).
	MinZoom = 0.05
	// MaxZoom is the maximum camera zoom (800%).
	MaxZoom = 8.0
	// DefaultGridStep is the base world spacing for paper patterns.
	DefaultGridStep = 40.0
)

// ZoomStops are the discrete zoom steps used by ZoomIn / ZoomOut.
var ZoomStops = []float64{0.05, 0.10, 0.25, 0.50, 1.0, 2.0, 4.0, 8.0}

// TextLine is one laid-out line of text.
type TextLine struct {
	Text    string
	X, Y, W float64
}

// TextBlock is a laid-out block of text measured by MeasureFunc.
type TextBlock struct {
	Lines []TextLine
	W, H  float64
	Line  float64
}

// MeasureFunc lays out text wrapping at width (0 wraps only at explicit newlines).
type MeasureFunc func(text, font string, size, width float64) TextBlock

// Editor owns an open whiteboard document and its interactive viewport state.
type Editor struct {
	Doc     *doc.Drawing
	Page    doc.ID
	Measure MeasureFunc

	// OnChange is invoked whenever the document state changes and should be persisted.
	OnChange func(*doc.Drawing)

	cams map[doc.ID]Camera
	VW   float64
	VH   float64
}

// New creates an Editor for d with optional text measurer measure.
func New(d *doc.Drawing, measure MeasureFunc) *Editor {
	if d == nil {
		d = doc.NewDrawing("", "Board 1")
	}
	d.Normalize(d.ID, d.Name)
	e := &Editor{
		Doc:     d,
		Measure: measure,
		cams:    make(map[doc.ID]Camera, len(d.Pages)),
		VW:      960,
		VH:      640,
	}
	e.loadPageCameras()
	e.Page = d.ActivePage
	if d.Page(e.Page) == nil {
		e.Page = d.Pages[0].ID
	}
	return e
}

// Replace swaps the open drawing cleanly (e.g. switching boards).
func (e *Editor) Replace(d *doc.Drawing) {
	if d == nil {
		d = doc.NewDrawing("", "Board 1")
	}
	d.Normalize(d.ID, d.Name)
	e.Doc = d
	e.cams = make(map[doc.ID]Camera, len(d.Pages))
	e.loadPageCameras()
	e.Page = d.ActivePage
	if d.Page(e.Page) == nil {
		e.Page = d.Pages[0].ID
	}
}

func (e *Editor) loadPageCameras() {
	for _, p := range e.Doc.Pages {
		if p.Camera != nil && p.Camera.Z > 0 {
			e.cams[p.ID] = Camera{
				X: p.Camera.X,
				Y: p.Camera.Y,
				Z: geo.Clamp(p.Camera.Z, MinZoom, MaxZoom),
			}
		}
	}
}

// SetViewport updates the stage size in DIPs.
func (e *Editor) SetViewport(w, h float64) {
	if w > 0 {
		e.VW = w
	}
	if h > 0 {
		e.VH = h
	}
}

// Cam returns the camera for the active page.
func (e *Editor) Cam() Camera {
	if c, ok := e.cams[e.Page]; ok && c.Z > 0 {
		return c
	}
	return Camera{Z: 1}
}

// SetCam updates the camera for the active page, clamping Z to [MinZoom, MaxZoom].
func (e *Editor) SetCam(c Camera) {
	c.Z = geo.Clamp(c.Z, MinZoom, MaxZoom)
	e.cams[e.Page] = c
	if p := e.Doc.Page(e.Page); p != nil {
		p.Camera = &doc.PageCamera{X: c.X, Y: c.Y, Z: c.Z}
	}
}

// ToPage converts screen coordinates (relative to the stage origin) to page coordinates.
func (e *Editor) ToPage(screen geo.Vec) geo.Vec {
	c := e.Cam()
	return geo.V(screen.X/c.Z-c.X, screen.Y/c.Z-c.Y)
}

// ToScreen converts page coordinates to screen coordinates relative to the stage origin.
func (e *Editor) ToScreen(page geo.Vec) geo.Vec {
	c := e.Cam()
	return geo.V((page.X+c.X)*c.Z, (page.Y+c.Y)*c.Z)
}

// ViewBox returns the visible rectangle in page coordinates.
func (e *Editor) ViewBox() geo.Box {
	c := e.Cam()
	return geo.Box{X: -c.X, Y: -c.Y, W: e.VW / c.Z, H: e.VH / c.Z}
}

// Pan scrolls the viewport by (dx, dy) in screen DIPs (positive dy moves viewport down).
func (e *Editor) Pan(dx, dy float64) {
	c := e.Cam()
	e.SetCam(Camera{X: c.X - dx/c.Z, Y: c.Y - dy/c.Z, Z: c.Z})
}

// ZoomAt scales the zoom by factor around screenPoint, keeping the page point under
// screenPoint stationary.
func (e *Editor) ZoomAt(screenPoint geo.Vec, factor float64) {
	if factor <= 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
		return
	}
	e.ZoomTo(screenPoint, e.Cam().Z*factor)
}

// ZoomTo sets zoom to targetZ around screenPoint, keeping the page point under
// screenPoint stationary.
func (e *Editor) ZoomTo(screenPoint geo.Vec, targetZ float64) {
	if math.IsNaN(targetZ) || math.IsInf(targetZ, 0) {
		return
	}
	z := geo.Clamp(targetZ, MinZoom, MaxZoom)
	p := e.ToPage(screenPoint)
	e.SetCam(Camera{
		X: screenPoint.X/z - p.X,
		Y: screenPoint.Y/z - p.Y,
		Z: z,
	})
}

func (e *Editor) viewportCenter() geo.Vec {
	return geo.V(e.VW/2, e.VH/2)
}

// ZoomIn advances to the next discrete zoom stop around the viewport center.
func (e *Editor) ZoomIn() {
	cur := e.Cam().Z
	for _, stop := range ZoomStops {
		if stop > cur*1.01 {
			e.ZoomTo(e.viewportCenter(), stop)
			return
		}
	}
	e.ZoomTo(e.viewportCenter(), MaxZoom)
}

// ZoomOut steps down to the previous discrete zoom stop around the viewport center.
func (e *Editor) ZoomOut() {
	cur := e.Cam().Z
	for i := len(ZoomStops) - 1; i >= 0; i-- {
		if ZoomStops[i] < cur*0.99 {
			e.ZoomTo(e.viewportCenter(), ZoomStops[i])
			return
		}
	}
	e.ZoomTo(e.viewportCenter(), MinZoom)
}

// ResetZoom resets zoom to 100% around the viewport center.
func (e *Editor) ResetZoom() {
	e.ZoomTo(e.viewportCenter(), 1.0)
}

// SwitchPage activates page id, preserving each page's own camera.
func (e *Editor) SwitchPage(id doc.ID) bool {
	if e.Doc.Page(id) == nil {
		return false
	}
	if e.Page == id {
		return true
	}
	e.Page = id
	e.Doc.ActivePage = id
	e.emitChange()
	return true
}

// Paper returns the active paper pattern ("dots", "grid", "lines", "blank").
func (e *Editor) Paper() string {
	return e.Doc.Paper
}

// SetPaper updates the board's paper style and notifies OnChange.
func (e *Editor) SetPaper(paper string) {
	switch paper {
	case doc.PaperDots, doc.PaperGrid, doc.PaperLines, doc.PaperBlank:
	default:
		return
	}
	if e.Doc.Paper == paper {
		return
	}
	e.Doc.Paper = paper
	e.emitChange()
}

// Look returns the board's visual look ("sketch", "clean", "mono").
func (e *Editor) Look() string {
	return e.Doc.Look
}

// SetLook updates the board's visual look and notifies OnChange.
func (e *Editor) SetLook(look string) {
	switch look {
	case doc.LookSketch, doc.LookClean, doc.LookMono:
	default:
		return
	}
	if e.Doc.Look == look {
		return
	}
	e.Doc.Look = look
	e.emitChange()
}

// DarkMode reports whether the board is using the dark theme.
func (e *Editor) DarkMode() bool {
	return e.Doc.Theme != doc.ThemeLight
}

// SetDarkMode switches between dark and light themes and notifies OnChange.
func (e *Editor) SetDarkMode(dark bool) {
	next := doc.ThemeDark
	if !dark {
		next = doc.ThemeLight
	}
	if e.Doc.Theme == next {
		return
	}
	e.Doc.Theme = next
	e.emitChange()
}

// Palette returns the active theme's color palette.
func (e *Editor) Palette() *Palette {
	if e.DarkMode() {
		return DarkPalette
	}
	return LightPalette
}

// SetName updates the board's title and notifies OnChange.
func (e *Editor) SetName(name string) {
	if name == "" || e.Doc.Name == name {
		return
	}
	e.Doc.Name = name
	e.emitChange()
}

// GridStep returns the world-space grid spacing for paper patterns.
func (e *Editor) GridStep() float64 {
	if e.Doc.GridSize > 0 {
		return e.Doc.GridSize
	}
	return DefaultGridStep
}

// ItemCount returns the number of shapes in the drawing.
func (e *Editor) ItemCount() int {
	return len(e.Doc.Shapes)
}

func (e *Editor) emitChange() {
	if e.OnChange != nil {
		e.OnChange(e.Doc.Snapshot())
	}
}
