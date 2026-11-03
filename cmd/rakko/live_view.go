//go:build darwin

package main

// Drawing for the realtime activities: the charging wings (icon + words on
// the left, the 36×6 level bar and its number on the right, Droppy's
// shape), the download wings (ring + percent or megabytes), and the
// capsule under the notch for headphones and a finished download. Every
// line is laid out at the shell's final size and uncovered by the growing
// shell, exactly like the now-playing wings and the completion capsule.

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/locales"
)

// Activity glyphs, scaled from the design preview: the lightning bolt, the
// low-battery outline (drawn by hand so one cell can be red), earbuds, a
// pair of headphones, and a document.
var (
	liveBoltSym   = ui.MustParseSVG([]byte(`<svg viewBox="0 0 11 14" xmlns="http://www.w3.org/2000/svg"><path d="M6.5 0 0 8h4.5L3.8 14 11 5.5H6.3z" fill="currentColor"/></svg>`))
	livePhonesSym = ui.MustParseSVG([]byte(`<svg viewBox="0 0 17 16" xmlns="http://www.w3.org/2000/svg"><path d="M2 10V8.5a6.5 6.5 0 0 1 13 0V10" stroke="currentColor" stroke-width="1.6" fill="none"/><rect x="1" y="9" width="4" height="6" rx="1.5" fill="currentColor"/><rect x="12" y="9" width="4" height="6" rx="1.5" fill="currentColor"/></svg>`))
	liveFileSym   = ui.MustParseSVG([]byte(`<svg viewBox="0 0 13 16" xmlns="http://www.w3.org/2000/svg"><path d="M1.5 0h6.6L13 4.9v9.6A1.5 1.5 0 0 1 11.5 16h-10A1.5 1.5 0 0 1 0 14.5v-13A1.5 1.5 0 0 1 1.5 0z" fill="#D1D1D6"/><path d="M8 0v5h5" fill="#A1A1A6"/></svg>`))
	// livePodSym / livePodRightSym are one earbud each, the fallback inside a
	// battery ring when the system symbol is unavailable (headless
	// rendering, unusual model).
	livePodSym      = ui.MustParseSVG([]byte(`<svg viewBox="0 0 10 16" xmlns="http://www.w3.org/2000/svg"><circle cx="4.2" cy="4.4" r="3.2" fill="currentColor"/><rect x="3" y="6.2" width="2.6" height="8.4" rx="1.3" fill="currentColor"/></svg>`))
	livePodRightSym = ui.MustParseSVG([]byte(`<svg viewBox="0 0 10 16" xmlns="http://www.w3.org/2000/svg"><circle cx="5.8" cy="4.4" r="3.2" fill="currentColor"/><rect x="4.4" y="6.2" width="2.6" height="8.4" rx="1.3" fill="currentColor"/></svg>`))
)

// liveView draws the event the queue is showing at the shell's final size.
func (a *shelf) liveView(c *ui.Context, shellW, shellH, peekK float32, ev liveEvent) {
	if ev.kind.wings() {
		a.powerWings(c, shellW, shellH, peekK, ev)
		return
	}
	a.liveCapsule(c, shellW, shellH, peekK, ev)
}

// powerWings draws the charging wings: the icon and words on the left, the
// level bar and its number on the right, both in the notch's band.
func (a *shelf) powerWings(c *ui.Context, shellW, shellH, k float32, ev liveEvent) {
	col := liveGreen
	if ev.kind == liveLow {
		col = liveRed
	}
	row := ui.Row(c.Key("live-power")).Width(shellW).Height(shellH).
		Padding(0, liveWingPad, npWingsBody, liveWingPad).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Opacity(min(max(k*k, 0), 1)).Label(ev.statusLineIn(loc(c)))
	row.Children(func() {
		left := ui.Row(c.Key("live-power-left")).Gap(9).AlignItems(ui.Center)
		left.Children(func() {
			switch ev.kind {
			case liveLow:
				lowBatteryIcon(c)
			default:
				ui.Icon(c, liveBoltSym).Size(11, 14).TextColor(ink)
			}
			if ev.kind != liveCharging {
				ui.Text(c, ev.label).SingleLine().FontSize(12.5).FontWeight(500).TextColor(ink)
			}
		})
		right := ui.Row(c.Key("live-power-right")).Gap(10).AlignItems(ui.Center)
		right.Children(func() {
			levelBar(c, ev.pct, col)
			ui.Text(c, strconv.Itoa(ev.pct)).SingleLine().FontSize(13).FontWeight(600).
				FontFeatures("tnum").TextAlign(ui.End).MinWidth(22).TextColor(ink)
		})
	})
}

// levelBar is the 36 × 6 charge track: the tone at 20% for the track, and
// the solid tone for the fill inside a real blurred box shadow (6pt, half
// opacity) — the design's soft halo, not a stack of outlines.
func levelBar(c *ui.Context, pct int, col ui.Color) {
	track := ui.Box(c.Key("live-level")).Width(liveBarW).Height(liveBarH).Radius(liveBarH / 2).
		Background(col.Alpha(0.2))
	track.Children(func() {
		frac := min(max(float32(pct)/100, 0), 1)
		if frac <= 0 {
			return
		}
		ui.Box(c.Key("live-level-fill")).Width(max(1.5, liveBarW*frac)).Height(liveBarH).
			Radius(liveBarH/2).Background(col).
			Shadow(0, 0, 6, 0, col.Alpha(0.5))
	})
}

// lowBatteryIcon is the small outline with one red cell inside.
func lowBatteryIcon(c *ui.Context) {
	box := ui.Box(c.Key("live-lowbat")).Size(18, 11)
	box.Draw(func(p *ui.Painter, r ui.Rect) {
		body := liveRoundPath(r.X+0.75, r.Y+0.75, 14.5, 9.5, 2.5)
		p.StrokePath(body, 1.5, ink)
		p.FillPath(liveRoundPath(r.X+16.2, r.Y+3.5, 1.5, 4, 0.7), ink)
		p.FillPath(liveRoundPath(r.X+2.5, r.Y+2.5, 2.6, 6, 1), liveRed)
	})
}

// liveRoundPath builds a rounded rectangle path from its top-left corner.
func liveRoundPath(x, y, w, h, r float32) *ui.Path {
	r = min(r, min(w/2, h/2))
	var p ui.Path
	p.MoveTo(x+r, y)
	p.LineTo(x+w-r, y)
	p.QuadTo(x+w, y, x+w, y+r)
	p.LineTo(x+w, y+h-r)
	p.QuadTo(x+w, y+h, x+w-r, y+h)
	p.LineTo(x+r, y+h)
	p.QuadTo(x, y+h, x, y+h-r)
	p.LineTo(x, y+r)
	p.QuadTo(x, y, x+r, y)
	p.Close()
	return &p
}

// downloadWings draws a running download: the blue ring with a down arrow
// (percent inside a determinate arc, a spinning arc when the browser hides
// the total) and the number or megabytes on the right, plus +N when more
// downloads run behind the newest.
func (a *shelf) downloadWings(c *ui.Context, shellW, shellH, k float32, dl downloadProgress) {
	f := dl.file
	row := ui.Row(c.Key("live-download")).Width(shellW).Height(shellH).
		Padding(0, liveWingPad, npWingsBody, liveWingPad).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Opacity(min(max(k*k, 0), 1)).Label(downloadLabel(loc(c), dl))
	row.Children(func() {
		ring := ui.Box(c.Key("live-dl-ring")).Size(20, 20)
		spin := float32(0)
		if f.total <= 0 {
			spin = ring.Loop("live-dl-spin", time.Second, ui.Linear)
		}
		ring.Draw(func(p *ui.Painter, r ui.Rect) {
			cx, cy, rad := r.X+r.W/2, r.Y+r.H/2, float32(7.5)
			var track ui.Path
			track.Circle(cx, cy, rad)
			p.StrokePath(&track, 2.2, ui.RGBA(255, 255, 255, 0.16))
			if f.total > 0 {
				frac := min(max(float32(f.done)/float32(f.total), 0), 1)
				liveArc(p, cx, cy, rad, 0.75, frac, 2.2, liveBlue)
			} else {
				// The design rotates a 28% arc from the top.
				liveArc(p, cx, cy, rad, spin, 0.28, 2.2, liveBlue)
			}
			// The down arrow stays upright inside the ring.
			var arrow ui.Path
			arrow.MoveTo(cx, cy-3.6).LineTo(cx, cy+3.4)
			arrow.MoveTo(cx-2.6, cy+1).LineTo(cx, cy+3.6).LineTo(cx+2.6, cy+1)
			p.StrokePath(&arrow, 1.5, ink)
		})
		right := ui.Row(c.Key("live-dl-right")).Gap(6).AlignItems(ui.Center)
		right.Children(func() {
			if f.total > 0 {
				ui.Text(c, downloadPercent(f)).SingleLine().FontSize(13).FontWeight(600).
					FontFeatures("tnum").TextAlign(ui.End).MinWidth(32).TextColor(ink)
			} else {
				ui.Text(c, downloadMegabytes(f.done)).SingleLine().FontSize(10.5).FontWeight(500).
					FontFeatures("tnum").TextAlign(ui.End).TextColor(ink2)
			}
			if dl.count > 0 {
				ui.Text(c, fmt.Sprintf("+%d", dl.count)).SingleLine().FontSize(10.5).
					FontWeight(500).FontFeatures("tnum").TextColor(ink2)
			}
		})
	})
}

// headsetSymbol selects the closest SF Symbol available for the device.
func headsetSymbol(label string) string {
	name := strings.ToLower(label)
	switch {
	case strings.Contains(name, "airpods") && strings.Contains(name, "max"):
		return "airpodsmax"
	case strings.Contains(name, "airpods") && strings.Contains(name, "pro") && strings.Contains(name, "3"):
		return "airpodspro3"
	case strings.Contains(name, "airpods") && strings.Contains(name, "pro"):
		return "airpodspro"
	case strings.Contains(name, "airpods"):
		return "airpods"
	case strings.Contains(name, "beats"):
		return "beats.headphones"
	default:
		return "headphones"
	}
}

var headsetSymbols = map[string]*ui.Bitmap{}

// headsetBitmap caches a white template raster of one SF Symbol by name.
func headsetBitmap(name string) *ui.Bitmap {
	if b, ok := headsetSymbols[name]; ok {
		return b
	}
	data := mac.SystemSymbol(name, 24)
	var b *ui.Bitmap
	if src, err := png.Decode(bytes.NewReader(data)); err == nil {
		dst := image.NewNRGBA(src.Bounds())
		for y := dst.Bounds().Min.Y; y < dst.Bounds().Max.Y; y++ {
			for x := dst.Bounds().Min.X; x < dst.Bounds().Max.X; x++ {
				_, _, _, alpha := src.At(x, y).RGBA()
				dst.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: uint8(alpha >> 8)})
			}
		}
		b = ui.NewBitmap(dst)
	}
	headsetSymbols[name] = b
	return b
}

// headsetGlyph draws the device's own symbol; the generic headphones is
// the last resort when the model's symbol or AppKit is unavailable.
func (a *shelf) headsetGlyph(c *ui.Context, label string) {
	var glyph *ui.Bitmap
	if a.win != nil {
		glyph = headsetBitmap(headsetSymbol(label))
		if glyph == nil {
			glyph = headsetBitmap("headphones")
		}
	}
	if glyph != nil {
		ui.Image(c.Key("headset-symbol"), glyph).Size(18, 18).Fit(ui.Contain)
	} else {
		ui.Icon(c, livePhonesSym).Size(17, 16).TextColor(ink)
	}
}

// batterySymbol names the part glyph for the device model.
func batterySymbol(model string, part batteryPart) string {
	switch part {
	case batteryLeft:
		return "airpod.left"
	case batteryRight:
		return "airpod.right"
	case batteryCase:
		if strings.Contains(strings.ToLower(model), "pro") {
			return "airpodspro.chargingcase.wireless"
		}
		return "airpods.chargingcase"
	}
	return headsetSymbol(model)
}

// batteryBadge is one part: its glyph inside a charge ring, the percentage
// beside it. The ring tracks fill3 and fills white, turning red at 20% or
// less; the capsule's height never changes.
func (a *shelf) batteryBadge(c *ui.Context, model string, lv batteryLevel) {
	col := ui.RGB(255, 255, 255)
	if lv.pct <= 20 {
		col = danger
	}
	key := "live-" + strconv.Itoa(int(lv.part))
	group := ui.Row(c.Key(key)).Gap(3).AlignItems(ui.Center)
	group.Children(func() {
		ring := ui.Box(c.Key("ring")).Size(20, 20).Center()
		ring.Draw(func(p *ui.Painter, r ui.Rect) {
			cx, cy := r.X+r.W/2, r.Y+r.H/2
			var track ui.Path
			track.Circle(cx, cy, 8.5)
			p.StrokePath(&track, 2, fill3)
			strokeRingArc(p, cx, cy, 8.5, 2, float32(max(lv.pct, 0))/100, col)
		})
		ring.Children(func() {
			var glyph *ui.Bitmap
			if a.win != nil {
				glyph = headsetBitmap(batterySymbol(model, lv.part))
				if glyph == nil && lv.part == batteryCase {
					glyph = headsetBitmap("airpods.chargingcase")
				}
			}
			if glyph != nil {
				ui.Image(c.Key("part"), glyph).Size(11, 11).Fit(ui.Contain)
			} else if lv.part == batteryCase {
				ui.Box(c.Key("case")).Width(9).Height(7).Radius(2).Border(1.1, ink)
			} else if lv.part == batteryRight {
				ui.Icon(c, livePodRightSym).Size(7, 9).TextColor(ink)
			} else {
				ui.Icon(c, livePodSym).Size(7, 9).TextColor(ink)
			}
		})
		ui.Text(c, strconv.Itoa(lv.pct)+"%").SingleLine().FontSize(10.5).
			TextColor(ink2).FontFeatures("tnum")
	})
}

// liveCapsule draws the headphone / download-done notice in the band under
// the notch. The completion capsule's body is clickable: it drops the file
// into the tray and opens the tray page.
func (a *shelf) liveCapsule(c *ui.Context, shellW, shellH, k float32, ev liveEvent) {
	l := loc(c)
	if ev.kind == liveAgentDone {
		clip := ui.Column(c.Key("live-capsule")).Width(shellW).Height(shellH).Clip().
			AlignItems(ui.Center).Margin(float32(a.sheetTop()), 0, 0, 0)
		clip.Children(func() {
			// The card has said what it came to say; a click folds it.
			if a.agentCard(c, shellW, shellH, k, ev).Clicked() {
				a.clearLive()
			}
		})
		return
	}
	w := ev.width
	if w <= 0 {
		baseW, _ := a.anchorSize()
		w = liveCapsuleWidth(l, ev, baseW)
	}
	// 刘海的提示条长在下沿 22pt 的带里；灵动岛是完整的 36 高胶囊，
	// 内容垂直居中（没有摄像头要避让）。
	rowH, justify := float32(fbBody), ui.End
	if a.island {
		rowH, justify = float32(a.capsuleBodyH()), ui.Center
	}
	clip := ui.Column(c.Key("live-capsule")).Width(shellW).Height(shellH).Clip().
		Justify(justify).AlignItems(ui.Center).Margin(float32(a.sheetTop()), 0, 0, 0)
	clip.Children(func() {
		body := ui.Row(c.Key("live-cap-body")).Width(w).Height(rowH).Gap(fbGap).
			Justify(ui.Center).AlignItems(ui.Center).Opacity(min(max(k*k, 0), 1)).
			Label(ev.statusLineIn(l))
		body.Children(func() {
			switch ev.kind {
			case liveHeadset:
				ui.Text(c, ev.label).SingleLine().FontSize(12).FontWeight(600).TextColor(ink).
					Shrink(1).MinWidth(0).Ellipsis("…")
				if len(ev.levels) == 0 {
					a.headsetGlyph(c, ev.label)
					ui.Text(c, l.LiveConnected()).SingleLine().FontSize(12).FontWeight(500).TextColor(ink2)
				}
				for _, lv := range ev.levels {
					a.batteryBadge(c, ev.label, lv)
				}
			case liveHeadsetOff:
				a.headsetGlyph(c, ev.label)
				ui.Text(c, ev.label).SingleLine().FontSize(12).FontWeight(600).TextColor(ink)
				ui.Text(c, l.LiveDisconnected()).SingleLine().FontSize(12).FontWeight(500).TextColor(ink2)
			case liveDownloadDone:
				ui.Icon(c, liveFileSym).Size(13, 16)
				ui.Text(c, l.LiveDownloaded()).SingleLine().FontSize(12).FontWeight(600).TextColor(ink)
				verbW, _ := c.MeasureText(0, ui.Span{Text: l.LiveDownloaded(), Size: 12, Weight: 600})
				ui.Text(c, middleEllipsis(c, ev.label, w-13-2*fbPadX-verbW-7-fbGap*3-7)).
					SingleLine().FontSize(12).FontWeight(500).TextColor(ink2)
				ui.Box(c.Key("live-done-dot")).Size(7, 7).Radius(3.5).Background(liveGreen)
			}
		})
		if ev.kind == liveDownloadDone && body.Clicked() {
			a.openDownloaded(ev)
		}
	})
}

// middleEllipsis shortens a file name in the middle, keeping both ends.
func middleEllipsis(c *ui.Context, name string, maxW float32) string {
	if maxW <= 0 {
		return name
	}
	measure := func(s string) float32 {
		w, _ := c.MeasureText(0, ui.Span{Text: s, Size: 12, Weight: 500})
		return w
	}
	if measure(name) <= maxW {
		return name
	}
	runes := []rune(name)
	for keep := len(runes) - 1; keep >= 2; keep-- {
		head := keep - keep/2
		tail := keep / 2
		cand := string(runes[:head]) + "…" + string(runes[len(runes)-tail:])
		if measure(cand) <= maxW {
			return cand
		}
	}
	return "…"
}

// liveArc strokes a fraction of a circle starting at start (0.75 is the
// top) and wraps past 12 o'clock instead of clamping at a full turn.
func liveArc(p *ui.Painter, cx, cy, rad, start, frac, width float32, col ui.Color) {
	frac = min(max(frac, 0), 1)
	if frac <= 0 {
		return
	}
	end := start + frac
	if end <= 1 {
		var arc ui.Path
		arc.Circle(cx, cy, rad).Trim(start, end)
		p.StrokePath(&arc, width, col)
		return
	}
	var head ui.Path
	head.Circle(cx, cy, rad).Trim(start, 1)
	p.StrokePath(&head, width, col)
	var tail ui.Path
	tail.Circle(cx, cy, rad).Trim(0, end-1)
	p.StrokePath(&tail, width, col)
}

// downloadPercent is the wings' right side for a known total.
func downloadPercent(f downloadFile) string {
	if f.total <= 0 {
		return ""
	}
	return strconv.Itoa(int(math.Floor(float64(f.done)*100/float64(f.total)+0.5))) + "%"
}

// downloadMegabytes renders the bytes received when the total is unknown.
func downloadMegabytes(done int64) string {
	const (
		kb = 1024
		mb = 1024 * kb
	)
	if done < kb {
		return fmt.Sprintf("%d B", done)
	}
	if done < mb {
		return fmt.Sprintf("%.0f KB", float64(done)/kb)
	}
	return fmt.Sprintf("%.1f MB", float64(done)/mb)
}

// downloadLabel is the accessible description of the wings.
func downloadLabel(l locales.Messages, dl downloadProgress) string {
	if dl.file.total > 0 {
		return l.DownloadWings(dl.file.name, downloadPercent(dl.file))
	}
	return l.DownloadWings(dl.file.name, downloadMegabytes(dl.file.done))
}
