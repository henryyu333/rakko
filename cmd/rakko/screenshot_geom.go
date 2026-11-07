//go:build darwin

package main

// The screenshot session's arithmetic: display-local and global point
// rectangles, pixel crops, handle hit tests, toolbar placement, window
// hit testing and file naming. Everything here is pure — no windows, no
// pasteboard, no clock — so the geometry that decides what the user
// captures and what the overlay shows can be tested on its own.

import (
	"fmt"
	"image"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
)

// shotTarget is one display the session freezes, as mygo reports it.
type shotTarget struct {
	ID     int64
	Bounds mygo.Rectangle
	Scale  float64
}

// shotExpectedPixels is the capture's expected pixel size for the target:
// the display's point size times its backing scale.
func (t shotTarget) expectedPixels() (w, h int) {
	s := t.Scale
	if s <= 0 {
		s = 1
	}
	return int(math.Round(float64(t.Bounds.Width) * s)), int(math.Round(float64(t.Bounds.Height) * s))
}

// shotLocalRect converts a global point rectangle into t's local points.
func shotLocalRect(r mygo.Rectangle, t shotTarget) mygo.Rectangle {
	return mygo.Rectangle{X: r.X - t.Bounds.X, Y: r.Y - t.Bounds.Y, Width: r.Width, Height: r.Height}
}

// shotGlobalRect is shotLocalRect's inverse.
func shotGlobalRect(r mygo.Rectangle, t shotTarget) mygo.Rectangle {
	return mygo.Rectangle{X: r.X + t.Bounds.X, Y: r.Y + t.Bounds.Y, Width: r.Width, Height: r.Height}
}

// shotPointInRect reports whether p is inside r (half-open, as rectangles
// are everywhere else).
func shotPointInRect(p mygo.Point, r mygo.Rectangle) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}

// shotNormalizeRect builds the rectangle between two corners, whatever
// order they were dragged in.
func shotNormalizeRect(a, b mygo.Point) mygo.Rectangle {
	x0, x1 := a.X, b.X
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	y0, y1 := a.Y, b.Y
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	return mygo.Rectangle{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// shotSquareRect is the selection a Shift-dragged rectangle makes: the
// square that reaches the dragged corner, keeping the press corner fixed.
func shotSquareRect(a, b mygo.Point) mygo.Rectangle {
	dx := b.X - a.X
	dy := b.Y - a.Y
	side := max(abs(dx), abs(dy))
	sx, sy := sign(dx), sign(dy)
	if sx == 0 {
		sx = 1
	}
	if sy == 0 {
		sy = 1
	}
	bx := a.X + sx*side
	by := a.Y + sy*side
	return shotNormalizeRect(a, mygo.Point{X: bx, Y: by})
}

// shotSnapPoint snaps b to the nearest 45° ray from a, keeping the
// distance: a Shift-dragged arrow or straight pen line.
func shotSnapPoint(a, b mygo.Point) mygo.Point {
	dx, dy := float64(b.X-a.X), float64(b.Y-a.Y)
	if dx == 0 && dy == 0 {
		return b
	}
	dist := math.Hypot(dx, dy)
	angle := math.Atan2(dy, dx)
	step := math.Pi / 4
	angle = math.Round(angle/step) * step
	return mygo.Point{X: a.X + int(math.Round(math.Cos(angle)*dist)), Y: a.Y + int(math.Round(math.Sin(angle)*dist))}
}

// shotClampRect keeps r inside area: the selection may never leave the
// display that owns it. A rectangle larger than the area takes its size.
func shotClampRect(r, area mygo.Rectangle) mygo.Rectangle {
	if r.Width > area.Width {
		r.Width = area.Width
	}
	if r.Height > area.Height {
		r.Height = area.Height
	}
	r.X = min(max(r.X, area.X), area.X+area.Width-r.Width)
	r.Y = min(max(r.Y, area.Y), area.Y+area.Height-r.Height)
	return r
}

// shotHandle is one of the selection's eight resize handles.
type shotHandle int

const (
	shotHandleNone shotHandle = iota
	shotHandleN
	shotHandleS
	shotHandleE
	shotHandleW
	shotHandleNE
	shotHandleNW
	shotHandleSE
	shotHandleSW
)

// shotHandleAt returns the handle under p, within tol points of the
// selection's edges and corners. Corners win over edges.
func shotHandleAt(sel mygo.Rectangle, p mygo.Point, tol int) shotHandle {
	nearLeft := abs(p.X-sel.X) <= tol
	nearRight := abs(p.X-(sel.X+sel.Width)) <= tol
	nearTop := abs(p.Y-sel.Y) <= tol
	nearBottom := abs(p.Y-(sel.Y+sel.Height)) <= tol
	insideY := p.Y >= sel.Y-tol && p.Y <= sel.Y+sel.Height+tol
	insideX := p.X >= sel.X-tol && p.X <= sel.X+sel.Width+tol
	switch {
	case nearLeft && nearTop:
		return shotHandleNW
	case nearRight && nearTop:
		return shotHandleNE
	case nearLeft && nearBottom:
		return shotHandleSW
	case nearRight && nearBottom:
		return shotHandleSE
	case nearTop && insideX:
		return shotHandleN
	case nearBottom && insideX:
		return shotHandleS
	case nearLeft && insideY:
		return shotHandleW
	case nearRight && insideY:
		return shotHandleE
	}
	return shotHandleNone
}

// shotResize moves the handle's edges to p, keeping the opposite side
// fixed and the rectangle at least 1×1 and inside area.
func shotResize(sel mygo.Rectangle, h shotHandle, p mygo.Point, area mygo.Rectangle) mygo.Rectangle {
	x0, y0 := sel.X, sel.Y
	x1, y1 := sel.X+sel.Width, sel.Y+sel.Height
	switch h {
	case shotHandleNW:
		x0, y0 = p.X, p.Y
	case shotHandleNE:
		x1, y0 = p.X, p.Y
	case shotHandleSW:
		x0, y1 = p.X, p.Y
	case shotHandleSE:
		x1, y1 = p.X, p.Y
	case shotHandleN:
		y0 = p.Y
	case shotHandleS:
		y1 = p.Y
	case shotHandleW:
		x0 = p.X
	case shotHandleE:
		x1 = p.X
	}
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	if x1-x0 < 1 {
		x1 = x0 + 1
	}
	if y0 > y1 {
		y0, y1 = y1, y0
	}
	if y1-y0 < 1 {
		y1 = y0 + 1
	}
	x0 = clampInt(x0, area.X, area.X+area.Width)
	x1 = clampInt(x1, area.X, area.X+area.Width)
	y0 = clampInt(y0, area.Y, area.Y+area.Height)
	y1 = clampInt(y1, area.Y, area.Y+area.Height)
	return mygo.Rectangle{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// shotCursorFor picks the pointer shape over the selection: resize arrows
// on a handle, a move cursor inside it, a crosshair everywhere else.
func shotCursorFor(sel mygo.Rectangle, p mygo.Point, hasSel bool, tool shotTool) ui.Cursor {
	if hasSel {
		switch shotHandleAt(sel, p, 6) {
		case shotHandleN, shotHandleS:
			return ui.CursorResizeNS
		case shotHandleE, shotHandleW:
			return ui.CursorResizeEW
		case shotHandleNW, shotHandleSE:
			return ui.CursorResizeNWSE
		case shotHandleNE, shotHandleSW:
			return ui.CursorResizeNESW
		}
		if shotPointInRect(p, sel) {
			if tool == shotToolText {
				return ui.CursorText
			}
			return ui.CursorMove
		}
	}
	return ui.CursorCrosshair
}

// shotCropRect is the selection in the frozen image's pixels. The image
// and the display may differ by a pixel after rounding, so the mapping is
// taken from the image's own size rather than the backing scale.
func shotCropRect(sel mygo.Rectangle, t shotTarget, imgW, imgH int) image.Rectangle {
	local := shotLocalRect(sel, t)
	sx := float64(imgW) / float64(max(t.Bounds.Width, 1))
	sy := float64(imgH) / float64(max(t.Bounds.Height, 1))
	x0 := int(math.Round(float64(local.X) * sx))
	y0 := int(math.Round(float64(local.Y) * sy))
	x1 := int(math.Round(float64(local.X+local.Width) * sx))
	y1 := int(math.Round(float64(local.Y+local.Height) * sy))
	x0 = clampInt(x0, 0, imgW)
	x1 = clampInt(x1, 0, imgW)
	y0 = clampInt(y0, 0, imgH)
	y1 = clampInt(y1, 0, imgH)
	if x1 <= x0 {
		x1 = min(x0+1, imgW)
	}
	if y1 <= y0 {
		y1 = min(y0+1, imgH)
	}
	return image.Rect(x0, y0, x1, y1)
}

// shotPixelSize is the selection's size in the frozen image's pixels.
func shotPixelSize(sel mygo.Rectangle, t shotTarget, imgW, imgH int) (int, int) {
	r := shotCropRect(sel, t, imgW, imgH)
	return r.Dx(), r.Dy()
}

// shotWindowAt picks the frontmost ordinary window under p on the target
// display, or none for the desktop. Windows are in front-to-back order.
func shotWindowAt(windows []mac.ScreenWindow, p mygo.Point, t shotTarget) (mygo.Rectangle, bool) {
	area := t.Bounds
	for _, w := range windows {
		r := mygo.Rectangle{
			X: int(math.Round(w.Bounds.Origin.X)), Y: int(math.Round(w.Bounds.Origin.Y)),
			Width: int(math.Round(w.Bounds.Size.Width)), Height: int(math.Round(w.Bounds.Size.Height)),
		}
		if r.Width <= 0 || r.Height <= 0 {
			continue
		}
		// A window mostly off this display still belongs to the screen it
		// is on; its rectangle is clipped to that display for selection.
		if !shotRectsIntersect(r, area) {
			continue
		}
		if shotPointInRect(p, r) {
			return shotIntersectRect(r, area), true
		}
	}
	return mygo.Rectangle{}, false
}

// shotIntersectRect is the part of a inside b. A window that hangs off the
// display is selected as the part the user can actually see.
func shotIntersectRect(a, b mygo.Rectangle) mygo.Rectangle {
	x0, x1 := max(a.X, b.X), min(a.X+a.Width, b.X+b.Width)
	y0, y1 := max(a.Y, b.Y), min(a.Y+a.Height, b.Y+b.Height)
	if x1 <= x0 || y1 <= y0 {
		return mygo.Rectangle{}
	}
	return mygo.Rectangle{X: x0, Y: y0, Width: x1 - x0, Height: y1 - y0}
}

// shotRectsIntersect reports whether a and b share any point.
func shotRectsIntersect(a, b mygo.Rectangle) bool {
	return a.X < b.X+b.Width && b.X < a.X+a.Width && a.Y < b.Y+b.Height && b.Y < a.Y+a.Height
}

// shotPixelate averages the pixels of src's region into a grid of block×
// block squares: the mosaic tool's picture, small enough to be drawn scaled
// up as pixel art. The region is clipped to the image; the grid keeps at
// least one cell.
func shotPixelate(src *image.RGBA, region image.Rectangle, block int) *image.RGBA {
	region = region.Intersect(src.Bounds())
	if region.Empty() {
		return image.NewRGBA(image.Rect(0, 0, 1, 1))
	}
	block = max(block, 1)
	w := (region.Dx() + block - 1) / block
	h := (region.Dy() + block - 1) / block
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	for by := range h {
		for bx := range w {
			x0, y0 := region.Min.X+bx*block, region.Min.Y+by*block
			x1, y1 := min(x0+block, region.Max.X), min(y0+block, region.Max.Y)
			var r, g, b, a, n uint64
			for y := y0; y < y1; y++ {
				row := src.Pix[y*src.Stride:]
				for x := x0; x < x1; x++ {
					i := x * 4
					r += uint64(row[i])
					g += uint64(row[i+1])
					b += uint64(row[i+2])
					a += uint64(row[i+3])
					n++
				}
			}
			if n == 0 {
				continue
			}
			i := out.Pix[by*out.Stride+bx*4:]
			i[0], i[1], i[2], i[3] = uint8(r/n), uint8(g/n), uint8(b/n), uint8(a/n)
		}
	}
	return out
}

// shotFilePrefix and shotFileAt pick the system screenshot's name in the
// app's language: 「截屏 2026-11-07 15.04.05.png」 or
// 「Screenshot 2026-11-07 at 15.04.05.png」.
func shotFileName(t time.Time, prefix, at string) string {
	return fmt.Sprintf("%s %s%s%s.png", prefix, t.Format("2006-01-02"), at, t.Format("15.04.05"))
}

// shotUniqueName adds 「 (2)」, 「 (3)」… before the extension while taken
// says the name is in use.
func shotUniqueName(name string, taken func(string) bool) string {
	if taken == nil || !taken(name) {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 2; i < 10000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", base, i, ext)
		if !taken(candidate) {
			return candidate
		}
	}
	return name
}

// shotSaveDir turns the screencapture preference (the output of
// `defaults read com.apple.screencapture location`) into the directory a
// screenshot is saved in: the preference when it names an existing
// absolute directory, ~/Desktop otherwise.
func shotSaveDir(configured, home string) string {
	dir := strings.TrimSpace(configured)
	if strings.HasPrefix(dir, "~/") {
		dir = filepath.Join(home, strings.TrimPrefix(dir, "~/"))
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(home, "Desktop")
}

// sign returns -1, 0 or 1.
func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

// clampInt keeps v in [lo, hi].
func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// shotToolbar is where the selection's toolbar sits and every button in
// it, in global points.
type shotToolbar struct {
	rect  mygo.Rectangle
	items []shotBarItem
}

// shotBarKind names one toolbar control.
type shotBarKind int

const (
	shotBarTool shotBarKind = iota
	shotBarColor
	shotBarWidth
	shotBarUndo
	shotBarRedo
	shotBarOCR
	shotBarPin
	shotBarSave
	shotBarCancel
	shotBarCopy
)

// shotBarItem is one control's rectangle.
type shotBarItem struct {
	kind  shotBarKind
	tool  shotTool
	index int
	rect  mygo.Rectangle
}

const (
	shotToolbarH   = 42
	shotToolbarPad = 8
	shotButtonSize = 26
	shotButtonGap  = 4
	shotSwatchSize = 16
	shotSepW       = 9
)

// shotToolbarWidth is the toolbar's fixed width: six tools, five colours,
// three widths, undo/redo, and the five actions with the wider copy button.
func shotToolbarWidth() int {
	items := shotToolbarItems(0, 0)
	maxX := 0
	for _, it := range items {
		maxX = max(maxX, it.rect.X+it.rect.Width)
	}
	return maxX + shotToolbarPad
}

// shotToolbarItems lays the controls out from x, y in local terms: x is the
// toolbar's left edge and y its top. The returned items carry no toolbar
// rect.
func shotToolbarItems(x, y int) []shotBarItem {
	var items []shotBarItem
	cx, cy := x+shotToolbarPad, y+(shotToolbarH-shotButtonSize)/2
	add := func(kind shotBarKind, tool shotTool, index, w int) {
		items = append(items, shotBarItem{kind: kind, tool: tool, index: index,
			rect: mygo.Rectangle{X: cx, Y: cy, Width: w, Height: shotButtonSize}})
		cx += w + shotButtonGap
	}
	sep := func() {
		cx += shotSepW - shotButtonGap
	}
	for _, tool := range []shotTool{shotToolRect, shotToolArrow, shotToolPen, shotToolMosaic, shotToolText, shotToolNumber} {
		add(shotBarTool, tool, 0, shotButtonSize)
	}
	sep()
	swatchY := y + (shotToolbarH-shotSwatchSize)/2
	for i := range shotAnnotationColors {
		items = append(items, shotBarItem{kind: shotBarColor, index: i,
			rect: mygo.Rectangle{X: cx, Y: swatchY, Width: shotSwatchSize, Height: shotSwatchSize}})
		cx += shotSwatchSize + 3
	}
	cx += shotSepW - 3
	for i := range shotAnnotationWidths {
		items = append(items, shotBarItem{kind: shotBarWidth, index: i,
			rect: mygo.Rectangle{X: cx, Y: cy, Width: shotSwatchSize, Height: shotButtonSize}})
		cx += shotSwatchSize + 3
	}
	cx += shotSepW - 3
	add(shotBarUndo, shotToolNone, 0, shotButtonSize)
	add(shotBarRedo, shotToolNone, 0, shotButtonSize)
	sep()
	add(shotBarOCR, shotToolNone, 0, shotButtonSize)
	add(shotBarPin, shotToolNone, 0, shotButtonSize)
	add(shotBarSave, shotToolNone, 0, shotButtonSize)
	add(shotBarCancel, shotToolNone, 0, shotButtonSize)
	// The copy button is the primary action: wider, with its word.
	add(shotBarCopy, shotToolNone, 0, 54)
	return items
}

// shotToolbarLayout places the toolbar for sel inside display: below the
// selection, right-aligned, flipped above when it does not fit, then
// inside the selection's bottom edge, and always clamped onto the display.
func shotToolbarLayout(sel, display mygo.Rectangle) shotToolbar {
	w := shotToolbarWidth()
	margin := 8
	x := sel.X + sel.Width - w
	y := sel.Y + sel.Height + margin
	if y+shotToolbarH > display.Y+display.Height-margin {
		y = sel.Y - margin - shotToolbarH
	}
	if y < display.Y+margin {
		y = sel.Y + sel.Height - shotToolbarH - margin
	}
	x = clampInt(x, display.X+margin, max(display.X+margin, display.X+display.Width-margin-w))
	y = clampInt(y, display.Y+margin, max(display.Y+margin, display.Y+display.Height-margin-shotToolbarH))
	rect := mygo.Rectangle{X: x, Y: y, Width: w, Height: shotToolbarH}
	return shotToolbar{rect: rect, items: shotToolbarItems(x, y)}
}

// shotBarAt returns the control under p, if any.
func (tb shotToolbar) shotBarAt(p mygo.Point) (shotBarItem, bool) {
	if !shotPointInRect(p, tb.rect) {
		return shotBarItem{}, false
	}
	for _, it := range tb.items {
		// The width dots and colour swatches are small; give them a
		// fingertip-sized target without moving what they draw.
		r := it.rect
		if it.kind == shotBarColor || it.kind == shotBarWidth {
			r = mygo.Rectangle{X: r.X - 3, Y: tb.rect.Y + 3, Width: r.Width + 6, Height: tb.rect.Height - 6}
		}
		if shotPointInRect(p, r) {
			return it, true
		}
	}
	return shotBarItem{}, false
}
