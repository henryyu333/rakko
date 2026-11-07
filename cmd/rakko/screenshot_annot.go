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

// shotTextSizes are the text tool's sizes in points, one per stroke width
// preset: 2, 4 and 7 points.
var shotTextSizes = []float32{14, 18, 24}

// shotTextSizeFor returns the type size a stroke width types in.
func shotTextSizeFor(width int) float32 {
	for i, w := range shotAnnotationWidths {
		if width <= w {
			return shotTextSizes[i]
		}
	}
	return shotTextSizes[len(shotTextSizes)-1]
}

// shotNumberRadius is a number badge's radius in points.
const shotNumberRadius = 11

// shotMosaicCell is one mosaic block: its place on the frozen picture and
// the average colour it covers.
type shotMosaicCell struct {
	rect  mygo.Rectangle
	color ui.Color
}

// shotAnnotation is one mark. a and b are the drag's endpoints (a is the
// top-left of text and the centre for a number), points is a pen's path,
// and cells are the mosaic blocks a brush stroke covered. boxW is a text
// box's content width in points; the height follows from the wrapped text.
type shotAnnotation struct {
	tool   shotTool
	color  ui.Color
	width  int
	a, b   mygo.Point
	points []mygo.Point
	text   string
	boxW   int
	number int
	cells  []shotMosaicCell
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
func shotAnnotationTextSize(an shotAnnotation) float32 { return shotTextSizeFor(an.width) }

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
		// Painter.Line fills an axis-aligned box, so a slanted arrow came
		// out as a horizontal or vertical bar. The shaft is a polyline; the
		// head is a solid triangle, as the system's markup tool draws it.
		x0, y0 := px(an.a)
		x1, y1 := px(an.b)
		dx, dy := x1-x0, y1-y0
		length := float32(math.Hypot(float64(dx), float64(dy)))
		if length < 0.5 {
			return
		}
		ux, uy := dx/length, dy/length
		head := max(11*scale, w*3.2)
		if head > length*0.7 {
			head = length * 0.7
		}
		bx, by := x1-ux*head*0.85, y1-uy*head*0.85
		p.StrokePolyline([][2]float32{{x0, y0}, {bx, by}}, w, an.color)
		half := head * 0.55
		shotFillTriangle(p,
			[2]float32{x1, y1},
			[2]float32{bx - uy*half, by + ux*half},
			[2]float32{bx + uy*half, by - ux*half}, an.color)
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
		// Every block carries the frozen picture's average colour for its
		// pixels, so the preview and the export paint the same grid.
		for _, cell := range an.cells {
			x0, y0 := px(mygo.Point{X: cell.rect.X, Y: cell.rect.Y})
			x1, y1 := px(mygo.Point{X: cell.rect.X + cell.rect.Width, Y: cell.rect.Y + cell.rect.Height})
			p.Fill(ui.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}, cell.color, 0)
		}
	case shotToolText:
		if an.text == "" {
			return
		}
		// Lay the text out at its point size and scale the result: shaping
		// it at the export's pixel size would break lines elsewhere, as
		// glyph advances do not scale exactly. The preview draws at scale
		// 1; a crop draws the same lines enlarged.
		size := shotAnnotationTextSize(an)
		lx := float32(an.a.X - origin.X)
		ly := float32(an.a.Y - origin.Y)
		draw := func() {
			p.RichText(lx, ly, float32(an.boxW), ui.Span{Text: an.text, Size: size, Color: an.color})
		}
		if scale == 1 {
			draw()
		} else {
			p.Transform(ui.Scaling(scale, scale), 0, 0, draw)
		}
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

// shotFillTriangle paints a solid triangle row by row: the painter has no
// path fill, and an arrow head drawn as strokes would stay hollow. Half-point
// rows keep the edges from stepping a whole point.
func shotFillTriangle(p *ui.Painter, a, b, c [2]float32, col ui.Color) {
	edges := [][2][2]float32{{a, b}, {b, c}, {c, a}}
	minY := min(a[1], b[1], c[1])
	maxY := max(a[1], b[1], c[1])
	for y := minY; y < maxY; y += 0.5 {
		yc := y + 0.25
		x0, x1 := float32(math.Inf(1)), float32(math.Inf(-1))
		for _, e := range edges {
			p0, p1 := e[0], e[1]
			if (p0[1] <= yc) == (p1[1] <= yc) {
				continue
			}
			t := (yc - p0[1]) / (p1[1] - p0[1])
			x := p0[0] + t*(p1[0]-p0[0])
			x0 = min(x0, x)
			x1 = max(x1, x)
		}
		if x1 > x0 {
			p.Fill(ui.Rect{X: x0, Y: y, W: x1 - x0, H: 0.5}, col, 0)
		}
	}
}

// absF is the float32 absolute value.
func absF(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
