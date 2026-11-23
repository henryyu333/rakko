package editor

import (
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"math"
	"strings"

	"rakko/internal/board/doc"
	"rakko/internal/board/geo"
)

// ImageDataResolver resolves embedded image asset bytes and MIME type for SVG export.
type ImageDataResolver func(assetID doc.ID) (data []byte, mime string, ok bool)

func svgHexColor(c Color) (hex string, opacity float64) {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B), c.A
}

func xmlEsc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func svgFontFamily(font string) string {
	switch font {
	case "sketch", "draw":
		return "'Shantell Sans', 'Yozai', cursive"
	case "serif":
		return "Georgia, serif"
	case "mono":
		return "ui-monospace, monospace"
	default:
		return "system-ui, sans-serif"
	}
}

// ExportSVG serializes ids (or the entire active page when ids is empty) into a
// standalone SVG document sharing Display primitives and geo.Path.SVG.
func (e *Editor) ExportSVG(ids []doc.ID, pal *Palette, resolveImage ImageDataResolver) (string, error) {
	if pal == nil {
		pal = e.Palette()
	}

	var box geo.Box
	hasBox := false
	targetSet := map[doc.ID]bool{}
	for _, id := range ids {
		if e.Doc.Shapes[id] != nil {
			targetSet[id] = true
		}
	}
	if len(targetSet) > 0 {
		for id := range targetSet {
			b := e.PageBounds(id)
			if !hasBox {
				box, hasBox = b, true
			} else {
				box = box.Union(b)
			}
		}
	} else {
		box, hasBox = e.PageContentBox(e.Page)
	}

	const pad = 24.0
	if !hasBox {
		box = geo.Box{X: 0, Y: 0, W: 320, H: 240}
	} else {
		box = box.Expand(pad)
	}
	w := math.Max(box.W, 1)
	h := math.Max(box.H, 1)

	bgHex, _ := svgHexColor(pal.Background)
	var defs strings.Builder
	var body strings.Builder
	clipSeq := 0

	nextClipID := func(prefix string) string {
		clipSeq++
		return fmt.Sprintf("%s-%d", prefix, clipSeq)
	}

	selectedTree := func(id doc.ID) bool {
		if len(targetSet) == 0 {
			return true
		}
		for cur := id; cur != "" && !doc.IsPageID(cur); {
			if targetSet[cur] {
				return true
			}
			sh := e.Doc.Shapes[cur]
			if sh == nil {
				break
			}
			cur = sh.Parent
		}
		return false
	}

	shiftMat := geo.Translate(-box.X, -box.Y)

	writePrims := func(s *doc.Shape, m geo.Mat, opacity float64) {
		prims := e.Display(s, pal)
		fullM := shiftMat.Mul(m)
		for _, pr := range prims {
			switch pr.Kind {
			case PrimFill:
				if pr.Path == nil || len(pr.Path.Segs) == 0 {
					continue
				}
				d := pr.Path.SVG(fullM)
				if d == "" {
					continue
				}
				hex, ca := svgHexColor(pr.Color.Alpha(opacity))
				fmt.Fprintf(&body, `  <path d="%s" fill="%s"`, xmlEsc(d), hex)
				if ca < 0.999 {
					fmt.Fprintf(&body, ` fill-opacity="%.3f"`, ca)
				}
				body.WriteString("/>\n")
			case PrimStroke:
				if pr.Path == nil || len(pr.Path.Segs) == 0 {
					continue
				}
				d := pr.Path.SVG(fullM)
				if d == "" {
					continue
				}
				hex, ca := svgHexColor(pr.Color.Alpha(opacity))
				capStyle, joinStyle := "round", "round"
				if pr.Butt {
					capStyle, joinStyle = "butt", "miter"
				}
				fmt.Fprintf(&body, `  <path d="%s" fill="none" stroke="%s" stroke-width="%.2f" stroke-linecap="%s" stroke-linejoin="%s"`,
					xmlEsc(d), hex, math.Max(pr.Width, 0.5), capStyle, joinStyle)
				if len(pr.Dash) >= 2 {
					fmt.Fprintf(&body, ` stroke-dasharray="%.2f %.2f"`, pr.Dash[0], pr.Dash[1])
				}
				if ca < 0.999 {
					fmt.Fprintf(&body, ` stroke-opacity="%.3f"`, ca)
				}
				body.WriteString("/>\n")
			case PrimText:
				if pr.Block == nil || len(pr.Block.Lines) == 0 {
					continue
				}
				hex, ca := svgHexColor(pr.Color.Alpha(opacity))
				origin := fullM.Apply(geo.V(pr.X, pr.Y))
				deg := fullM.Rotation() * 180 / math.Pi
				fmt.Fprintf(&body, `  <text x="%.2f" y="%.2f" font-family="%s" font-size="%.2f" fill="%s"`,
					origin.X, origin.Y, xmlEsc(svgFontFamily(pr.Font)), pr.Size, hex)
				if pr.Bold {
					body.WriteString(` font-weight="700"`)
				}
				if pr.Italic {
					body.WriteString(` font-style="italic"`)
				}
				if pr.Underline {
					body.WriteString(` text-decoration="underline"`)
				}
				if ca < 0.999 {
					fmt.Fprintf(&body, ` fill-opacity="%.3f"`, ca)
				}
				if math.Abs(deg) > 0.01 {
					fmt.Fprintf(&body, ` transform="rotate(%.2f %.2f %.2f)"`, deg, origin.X, origin.Y)
				}
				body.WriteString(">\n")
				baseY := origin.Y + pr.Block.Line*0.78
				for i, line := range pr.Block.Lines {
					if line.Text == "" {
						continue
					}
					lx := origin.X + line.X
					ly := baseY + float64(i)*pr.Block.Line
					fmt.Fprintf(&body, `    <tspan x="%.2f" y="%.2f">%s</tspan>`+"\n", lx, ly, xmlEsc(line.Text))
				}
				body.WriteString("  </text>\n")
			case PrimImage:
				if resolveImage == nil {
					continue
				}
				raw, mime, ok := resolveImage(pr.Asset)
				if !ok || len(raw) == 0 {
					continue
				}
				if mime == "" {
					mime = "image/png"
				}
				uri := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
				origin := fullM.Apply(geo.V(pr.Box.X, pr.Box.Y))
				deg := fullM.Rotation() * 180 / math.Pi
				attrW, attrH := pr.Box.W, pr.Box.H
				var tfParts []string
				if math.Abs(deg) > 0.01 {
					tfParts = append(tfParts, fmt.Sprintf("rotate(%.2f %.2f %.2f)", deg, origin.X, origin.Y))
				}
				imgX, imgY, imgW, imgH := origin.X, origin.Y, attrW, attrH
				clipAttr := ""
				if pr.Crop != nil {
					cid := nextClipID("crop")
					fmt.Fprintf(&defs, `  <clipPath id="%s"><rect x="%.2f" y="%.2f" width="%.2f" height="%.2f"/></clipPath>`+"\n",
						cid, origin.X, origin.Y, attrW, attrH)
					clipAttr = fmt.Sprintf(` clip-path="url(#%s)"`, cid)
					cw := math.Max(pr.Crop.BottomRight.X-pr.Crop.TopLeft.X, minCropFraction)
					ch := math.Max(pr.Crop.BottomRight.Y-pr.Crop.TopLeft.Y, minCropFraction)
					imgW = attrW / cw
					imgH = attrH / ch
					imgX = origin.X - pr.Crop.TopLeft.X*imgW
					imgY = origin.Y - pr.Crop.TopLeft.Y*imgH
				}
				if pr.FlipX || pr.FlipY {
					sx, sy := 1.0, 1.0
					if pr.FlipX {
						sx = -1
					}
					if pr.FlipY {
						sy = -1
					}
					cx, cy := origin.X+attrW/2, origin.Y+attrH/2
					tfParts = append(tfParts, fmt.Sprintf("translate(%.2f %.2f) scale(%.0f %.0f) translate(%.2f %.2f)", cx, cy, sx, sy, -cx, -cy))
				}
				tfAttr := ""
				if len(tfParts) > 0 {
					tfAttr = fmt.Sprintf(` transform="%s"`, strings.Join(tfParts, " "))
				}
				opAttr := ""
				if opacity < 0.999 {
					opAttr = fmt.Sprintf(` opacity="%.3f"`, opacity)
				}
				fmt.Fprintf(&body, `  <image href="%s" x="%.2f" y="%.2f" width="%.2f" height="%.2f" preserveAspectRatio="none"%s%s%s/>`+"\n",
					xmlEsc(uri), imgX, imgY, imgW, imgH, clipAttr, tfAttr, opAttr)
			}
		}
	}

	var renderLayer func(parent doc.ID, opacityMul float64, highlightsOnly bool)
	renderLayer = func(parent doc.ID, opacityMul float64, highlightsOnly bool) {
		for _, id := range e.Children(parent) {
			s := e.Doc.Shapes[id]
			if s == nil {
				continue
			}
			op := opacityMul * geo.Clamp(s.Opacity, 0, 1)
			if s.Opacity == 0 {
				op = opacityMul
			}
			if s.Type == doc.TypeGroup {
				renderLayer(id, op, highlightsOnly)
				continue
			}
			isHL := s.Type == doc.TypeHighlight
			if isHL == highlightsOnly && selectedTree(id) {
				writePrims(s, e.PageMat(id), op)
			}
			if s.Type == doc.TypeFrame {
				fcid := nextClipID("frame")
				m := shiftMat.Mul(e.PageMat(id))
				cs := geo.Box{W: math.Max(s.W, 1), H: math.Max(s.H, 1)}.Corners()
				d := (&geo.Path{}).Poly(cs[:], true).SVG(m)
				fmt.Fprintf(&defs, `  <clipPath id="%s"><path d="%s"/></clipPath>`+"\n", fcid, xmlEsc(d))
				fmt.Fprintf(&body, `  <g clip-path="url(#%s)">`+"\n", fcid)
				renderLayer(id, op, highlightsOnly)
				body.WriteString("  </g>\n")
			}
		}
	}

	renderLayer(e.Page, 1, true)
	renderLayer(e.Page, 1, false)

	var out strings.Builder
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.2f %.2f">`+"\n",
		math.Ceil(w), math.Ceil(h), w, h)
	if defs.Len() > 0 {
		out.WriteString("<defs>\n")
		out.WriteString(defs.String())
		out.WriteString("</defs>\n")
	}
	fmt.Fprintf(&out, `  <rect width="100%%" height="100%%" fill="%s"/>`+"\n", bgHex)
	out.WriteString(body.String())
	out.WriteString("</svg>\n")
	return out.String(), nil
}
