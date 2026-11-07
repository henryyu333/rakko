//go:build darwin

package main

// The screenshot annotations: what the user can draw on the frozen
// picture, how each mark paints at any scale, and the undo stack that
// keeps every change reversible. Geometry is in global points; a caller
// supplies the origin and pixel scale that maps it to a display's
// overlay or to the crop being exported.

import (
	"fmt"
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// shotTool is the mark a drag makes.
type shotTool int

const (
	shotToolNone shotTool = iota
	shotToolRect
	shotToolArrow
	shotToolPen
	shotToolMosaic
	shotToolText
	shotToolNumber
)

// shotAnnotationColors are the five presets, in toolbar order.
var shotAnnotationColors = []ui.Color{
	ui.Hex("#FF453A"), // red
	ui.Hex("#FFD60A"), // yellow
	ui.Hex("#30D158"), // green
	ui.Hex("#0A84FF"), // blue
	ui.Hex("#FFFFFF"), // white
}

// shotAnnotationWidths are the three stroke widths, in points.
var shotAnnotationWidths = []int{2, 4, 7}

// shotMosaicBlock is the mosaic tool's cell size in points.
const shotMosaicBlock = 10

// shotTextSize is the text tool's type size in points.
const shotTextSize = 16

// shotNumberRadius is a number badge's radius in points.
const shotNumberRadius = 11

// shotAnnotation is one mark. a and b are the drag's endpoints (a is the
// anchor for text and the centre for a number), points is a pen's path,
// and mosaic is the pixelated picture made when the drag ended.
type shotAnnotation struct {
	tool   shotTool
	color  ui.Color
	width  int
	a, b   mygo.Point
	points []mygo.Point
	text   string
	number int
	mosaic *ui.Bitmap
}

// pre is the annotation a drag is drawing; it is not in the history until
// the pointer goes up.
type pre struct {
	ann shotAnnotation
	on  bool
}

// shotHistory is the annotation list and its two stacks. A snapshot is
// taken before each change, so undo and redo restore whole lists and need
// no per-tool inverse.
type shotHistory struct {
	annots []shotAnnotation
	undo   [][]shotAnnotation
	redo   [][]shotAnnotation
}

// shotHistoryLimit caps each stack; a session rarely needs more.
const shotHistoryLimit = 100

// shotHistorySnap records the state before a change.
func (h *shotHistory) snap() {
	h.undo = append(h.undo, append([]shotAnnotation(nil), h.annots...))
	if len(h.undo) > shotHistoryLimit {
		h.undo = h.undo[len(h.undo)-shotHistoryLimit:]
	}
	h.redo = nil
}

// push adds ann after a snapshot taken by snap.
func (h *shotHistory) push(ann shotAnnotation) {
	h.annots = append(h.annots, ann)
}

// undo restores the previous list; it reports whether anything changed.
func (h *shotHistory) undoLast() bool {
	if len(h.undo) == 0 {
		return false
	}
	h.redo = append(h.redo, append([]shotAnnotation(nil), h.annots...))
	last := h.undo[len(h.undo)-1]
	h.undo = h.undo[:len(h.undo)-1]
	h.annots = last
	return true
}

// redo reapplies the list undo took back.
func (h *shotHistory) redoLast() bool {
	if len(h.redo) == 0 {
		return false
	}
	h.undo = append(h.undo, append([]shotAnnotation(nil), h.annots...))
	last := h.redo[len(h.redo)-1]
	h.redo = h.redo[:len(h.redo)-1]
	h.annots = last
	return true
}

// canUndo and canRedo report whether the toolbar's buttons act.
func (h *shotHistory) canUndo() bool { return len(h.undo) > 0 }
func (h *shotHistory) canRedo() bool { return len(h.redo) > 0 }

// shotAnnotationSize returns the text annotation's rendered size in points.
func shotAnnotationTextSize(an shotAnnotation) float32 { return shotTextSize }

// shotDrawAnnotation paints one mark: origin is the global point that maps
// to (0, 0) and scale is the pixels a point takes. On an overlay scale is
// 1 and origin is the display's top-left; in an exported crop scale is the
// frozen image's pixel density and origin is the selection's top-left.
func shotDrawAnnotation(p *ui.Painter, an shotAnnotation, origin mygo.Point, scale float32) {
	px := func(pt mygo.Point) (float32, float32) {
		return float32(pt.X-origin.X) * scale, float32(pt.Y-origin.Y) * scale
	}
	w := float32(an.width) * scale
	switch an.tool {
	case shotToolRect:
		x0, y0 := px(an.a)
		x1, y1 := px(an.b)
		r := ui.Rect{X: min(x0, x1), Y: min(y0, y1), W: absF(x1 - x0), H: absF(y1 - y0)}
		p.Stroke(r, an.color, 0, w)
	case shotToolArrow:
		x0, y0 := px(an.a)
		x1, y1 := px(an.b)
		p.Line(x0, y0, x1, y1, w, an.color)
		ang := math.Atan2(float64(y1-y0), float64(x1-x0))
		head := math.Max(float64(10*scale), float64(w)*3)
		for _, delta := range []float64{math.Pi * 5 / 6, -math.Pi * 5 / 6} {
			hx := float64(x1) + math.Cos(ang+delta)*head
			hy := float64(y1) + math.Sin(ang+delta)*head
			p.Line(x1, y1, float32(hx), float32(hy), w, an.color)
		}
	case shotToolPen:
		if len(an.points) == 0 {
			return
		}
		pts := make([][2]float32, len(an.points))
		for i, pt := range an.points {
			pts[i][0], pts[i][1] = px(pt)
		}
		if len(pts) == 1 {
			p.Fill(ui.Rect{X: pts[0][0] - w/2, Y: pts[0][1] - w/2, W: w, H: w}, an.color, w/2)
			return
		}
		p.StrokePolyline(pts, w, an.color)
	case shotToolMosaic:
		x0, y0 := px(an.a)
		x1, y1 := px(an.b)
		dst := ui.Rect{X: min(x0, x1), Y: min(y0, y1), W: absF(x1 - x0), H: absF(y1 - y0)}
		if an.mosaic == nil {
			// While the drag is live there is no grid yet: show the area
			// the pixelation will take.
			p.Fill(dst, an.color.Alpha(0.18), 0)
			p.Stroke(dst, an.color, 0, w)
			return
		}
		p.ImageRegion(an.mosaic, ui.Rect{}, dst, ui.ImageOptions{})
	case shotToolText:
		if an.text == "" {
			return
		}
		x, y := px(an.a)
		size := shotAnnotationTextSize(an) * scale
		p.Text(x, y+size*0.15, an.text, size, an.color)
	case shotToolNumber:
		cx, cy := px(an.a)
		r := float32(shotNumberRadius) * scale
		p.Fill(ui.Rect{X: cx - r, Y: cy - r, W: 2 * r, H: 2 * r}, an.color, r)
		label := fmt.Sprintf("%d", an.number)
		span := ui.Span{Text: label, Size: 13 * scale, Weight: 600, Color: ui.Hex("#FFFFFF")}
		tw, th := p.MeasureText(0, span)
		p.RichText(cx-tw/2, cy-th/2, 0, span)
	}
}

// shotDragAnnotation builds the live annotation from the tool, the press
// point, the pointer and whether Shift is held: a square for rectangles, a
// 45° arrow, and a straight pen stroke.
func shotDragAnnotation(tool shotTool, color ui.Color, width int, start, cur mygo.Point, shift bool) shotAnnotation {
	an := shotAnnotation{tool: tool, color: color, width: width, a: start, b: cur}
	switch tool {
	case shotToolRect:
		if shift {
			r := shotSquareRect(start, cur)
			an.a = mygo.Point{X: r.X, Y: r.Y}
			an.b = mygo.Point{X: r.X + r.Width, Y: r.Y + r.Height}
		}
	case shotToolArrow:
		if shift {
			an.b = shotSnapPoint(start, cur)
		}
	case shotToolPen:
		a, b := start, cur
		if shift {
			b = shotSnapPoint(start, cur)
			an.points = []mygo.Point{a, b}
		} else {
			an.points = []mygo.Point{a, b}
		}
	}
	return an
}

// absF is the float32 absolute value.
func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
