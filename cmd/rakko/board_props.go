package main

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/locales"
)

type propOption struct {
	id    string
	label string
}

func (a *shelf) boardPropPanel(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	ps := b.ed.PanelState()
	if !ps.Visible {
		return
	}
	if !ps.ShowFillTab && b.propFillTab {
		b.propFillTab = false
	}

	_, stacked, _ := b.boardChrome()
	reserve := float32(96)
	if stacked {
		reserve = 136
	}
	maxH := float32(596)
	if b.stageRect.H > 160 {
		maxH = max(float32(b.stageRect.H)-reserve, 220)
	}
	narrow := b.stageRect.W > 0 && b.stageRect.W < 640
	pw := float32(216)
	inset := float32(16)
	gap := float32(8)
	if narrow {
		inset = 10
		gap = 6
	}

	card := ui.Column(c.Key("board-prop-panel")).
		Absolute().Top(inset).Right(inset).Width(pw).MinWidth(pw).
		Radius(12).Clip().
		Background(boardColor(pal.Bar)).
		Border(0.5, boardColor(pal.Edge)).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.28))

	card.Children(func() {
		ui.Scroll(c.Key("board-prop-scroll")).FillWidth().MaxHeight(maxH).TrackScroll(&b.propScroll).Children(func() {
			ui.Column(c.Key("board-prop-body")).FillWidth().Padding(10, 10, 10, 10).Gap(gap).Children(func() {
				if ps.ShowColor {
					a.boardPropColorSection(c, l, pal, ps)
				}
				if ps.ShowGeoShape {
					a.boardPropGeoSection(c, l, pal, ps)
				}
				if ps.ShowFillStyle {
					a.boardPropSegmented(c, pal, l.BoardPropFill(), "fill", []propOption{
						{"none", l.BoardPropFillNone()},
						{"semi", l.BoardPropFillSemi()},
						{"solid", l.BoardPropFillSolid()},
						{"pattern", l.BoardPropFillPattern()},
					}, ps.Fill, func(v string) {
						b.ed.SetPropFill(v)
						b.invalidate()
					})
				}
				if ps.ShowDrawProps {
					a.boardPropSegmented(c, pal, l.BoardPropPen(), "pen", []propOption{
						{"normal", l.BoardPenNormal()},
						{"pen", l.BoardPenFountain()},
						{"marker", l.BoardPenMarker()},
					}, ps.PenKind, func(v string) {
						b.ed.SetPropPenKind(v)
						b.invalidate()
					})
					penOpts := make([]propOption, len(editor.PenWidths))
					curPen := fmt.Sprintf("%.4g", ps.PenWidth)
					for i, w := range editor.PenWidths {
						s := fmt.Sprintf("%.4g", w)
						penOpts[i] = propOption{s, s}
					}
					a.boardPropSegmented(c, pal, l.BoardPropPenSize(), "penw", penOpts, curPen, func(v string) {
						for _, w := range editor.PenWidths {
							if fmt.Sprintf("%.4g", w) == v {
								b.ed.SetPropPenWidth(w)
								b.invalidate()
								break
							}
						}
					})
				}
				if ps.ShowEraserProps {
					a.boardPropSegmented(c, pal, l.BoardPropEraserMode(), "eraser", []propOption{
						{string(editor.EraserObject), l.BoardEraserObject()},
						{string(editor.EraserPartial), l.BoardEraserPartial()},
					}, string(ps.EraserMode), func(v string) {
						b.ed.SetEraserMode(editor.EraserMode(v))
						b.invalidate()
					})
				}
				if ps.ShowStrokeSize {
					a.boardPropSegmented(c, pal, l.BoardPropWidth(), "size", []propOption{
						{"s", "S"},
						{"m", "M"},
						{"l", "L"},
						{"xl", "XL"},
					}, ps.Size, func(v string) {
						b.ed.SetPropSize(v)
						b.invalidate()
					})
				}
				if ps.ShowDash {
					a.boardPropSegmented(c, pal, l.BoardPropDash(), "dash", []propOption{
						{"solid", l.BoardPropDashSolid()},
						{"dash", l.BoardPropDashDashed()},
						{"dot", l.BoardPropDashDotted()},
					}, ps.Dash, func(v string) {
						b.ed.SetPropDash(v)
						b.invalidate()
					})
				}
				if ps.ShowArrowProps {
					a.boardPropSegmented(c, pal, l.BoardPropRoute(), "route", []propOption{
						{"straight", l.BoardPropRouteStraight()},
						{"curve", l.BoardPropRouteCurve()},
						{"elbow", l.BoardPropRouteElbow()},
					}, ps.ArrowRoute, func(v string) {
						b.ed.SetPropArrowRoute(v)
						b.invalidate()
					})
					a.boardPropArrowHeadsSection(c, l, pal, ps)
				}
				if ps.ShowLineSpline {
					a.boardPropSegmented(c, pal, l.BoardPropSpline(), "spline", []propOption{
						{"line", l.BoardPropSplineStraight()},
						{"cubic", l.BoardPropSplineCubic()},
					}, ps.Spline, func(v string) {
						b.ed.SetPropLineSpline(v)
						b.invalidate()
					})
				}
				if ps.ShowFont {
					a.boardPropFontSection(c, l, pal, ps)
				}
				if b.ed.CanResetCrop() {
					btn := ui.Row(c.Key("prop-reset-crop")).FillWidth().Height(26).Radius(7).Center().
						Background(boardColor(pal.Background.Alpha(0.32))).
						Border(0.5, boardColor(pal.Edge)).Label(l.BoardCtxResetCrop())
					if btn.Hovered() {
						btn.Background(boardColor(pal.BarHover))
					}
					btn.Children(func() {
						ui.Text(c, l.BoardCtxResetCrop()).SingleLine().FontSize(11).FontWeight(500).TextColor(boardColor(pal.Ink2))
					})
					if btn.Clicked() {
						b.ed.ResetCrop()
						b.canvasFocus = false
						b.invalidate()
					}
				}
				if ps.ShowOpacity {
					opID := boardOpacityID(ps.Opacity)
					a.boardPropSegmented(c, pal, l.BoardPropOpacity(), "op", []propOption{
						{"0.25", "25%"},
						{"0.5", "50%"},
						{"0.75", "75%"},
						{"1", "100%"},
					}, opID, func(v string) {
						for _, op := range editor.Opacities {
							if boardOpacityID(op) == v {
								b.ed.SetPropOpacity(op)
								b.invalidate()
								break
							}
						}
					})
				}
			})
		})
	})
}

func boardOpacityID(op float64) string {
	switch {
	case math.Abs(op-0.25) < 0.05:
		return "0.25"
	case math.Abs(op-0.5) < 0.05:
		return "0.5"
	case math.Abs(op-0.75) < 0.05:
		return "0.75"
	default:
		return "1"
	}
}

func boardPropLabel(c *ui.Context, pal *editor.Palette, text string) {
	ui.Text(c, text).SingleLine().FontSize(10.5).FontWeight(600).TextColor(boardColor(pal.Ink3))
}

func (a *shelf) boardPropColorSection(c *ui.Context, l locales.Messages, pal *editor.Palette, ps editor.PanelState) {
	b := a.board
	ui.Column(c.Key("prop-color-sec")).FillWidth().Gap(5).Children(func() {
		if ps.ShowFillTab {
			ui.Row(c.Key("prop-color-tabs")).FillWidth().Gap(3).Padding(2, 2, 2, 2).Radius(8).
				Background(boardColor(pal.Background.Alpha(0.35))).Children(func() {
				tabs := []struct {
					fill  bool
					label string
				}{
					{false, l.BoardPropStrokeTab()},
					{true, l.BoardPropFillTab()},
				}
				for _, tb := range tabs {
					active := b.propFillTab == tb.fill
					btn := ui.Row(c.Key(fmt.Sprintf("prop-tab-%v", tb.fill))).Grow(1).Height(22).Radius(6).Center().Label(tb.label)
					switch {
					case active:
						btn.Background(boardColor(pal.AccentFill))
					case btn.Hovered():
						btn.Background(boardColor(pal.BarHover))
					}
					btn.Children(func() {
						col := pal.Ink2
						if active {
							col = pal.Accent
						}
						ui.Text(c, tb.label).SingleLine().FontSize(11).FontWeight(500).TextColor(boardColor(col))
					})
					if btn.Clicked() {
						b.propFillTab = tb.fill
						b.invalidate()
					}
				}
			})
		} else {
			boardPropLabel(c, pal, l.BoardPropColor())
		}

		activeHex := strings.ToUpper(ps.Color)
		if b.propFillTab {
			activeHex = strings.ToUpper(ps.FillColor)
		}
		const cols = 8
		ui.Column(c.Key("prop-swatches")).FillWidth().Gap(5).Children(func() {
			for row := 0; row < len(editor.Swatches); row += cols {
				end := min(row+cols, len(editor.Swatches))
				ui.Row(c).FillWidth().Justify(ui.SpaceBetween).Children(func() {
					for _, hex := range editor.Swatches[row:end] {
						hex := hex
						active := strings.ToUpper(hex) == activeHex
						dotCol := editor.ResolveColor(hex, pal.Dark, doc.LookClean)
						cell := ui.Row(c.Key("prop-sw-"+hex)).Size(21, 21).Radius(11).Center().Label(hex)
						if active {
							cell.Border(2, boardColor(pal.Accent))
						} else if cell.Hovered() {
							cell.Border(1.5, boardColor(pal.Ink3))
						}
						cell.Children(func() {
							inner := ui.Box(c).Size(15, 15).Radius(8).Background(boardColor(dotCol))
							if !pal.Dark && strings.ToUpper(hex) == "#E8E8EA" {
								inner.Border(0.5, boardColor(pal.Edge))
							}
						})
						if cell.Clicked() {
							if b.propFillTab {
								b.ed.SetPropFillColor(hex)
							} else {
								b.ed.SetPropStroke(hex)
							}
							b.canvasFocus = false
							b.invalidate()
						}
					}
				})
			}
		})
	})
}

func (a *shelf) boardPropSegmented(c *ui.Context, pal *editor.Palette, title, keyPrefix string, opts []propOption, active string, onPick func(string)) {
	a.boardPropSegmentedFit(c, pal, title, keyPrefix, opts, active, onPick, 3, 10.5)
}

// boardPropSegmentedFit lets a row whose labels are longer than the equal
// widths permit (the four font names at 480px) keep its text whole.
func (a *shelf) boardPropSegmentedFit(c *ui.Context, pal *editor.Palette, title, keyPrefix string, opts []propOption, active string, onPick func(string), pad, size float32) {
	ui.Column(c.Key("prop-sec-" + keyPrefix)).FillWidth().Gap(4).Children(func() {
		boardPropLabel(c, pal, title)
		ui.Row(c.Key("prop-row-" + keyPrefix)).FillWidth().Gap(3).Children(func() {
			for _, opt := range opts {
				opt := opt
				isOn := opt.id == active
				btn := ui.Row(c.Key("prop-"+keyPrefix+"-"+opt.id)).Grow(1).Height(24).Radius(6).Padding(0, pad, 0, pad).Center().Label(opt.label)
				switch {
				case isOn:
					btn.Background(boardColor(pal.AccentFill)).Border(0.5, boardColor(pal.Accent.Alpha(0.55)))
				case btn.Hovered():
					btn.Background(boardColor(pal.BarHover))
				default:
					btn.Background(boardColor(pal.Background.Alpha(0.28)))
				}
				btn.Children(func() {
					col := pal.Ink2
					if isOn {
						col = pal.Accent
					}
					ui.Text(c, opt.label).SingleLine().FontSize(size).FontWeight(500).TextColor(boardColor(col))
				})
				if btn.Clicked() && onPick != nil {
					a.board.canvasFocus = false
					onPick(opt.id)
				}
			}
		})
	})
}

func (a *shelf) boardPropGeoSection(c *ui.Context, l locales.Messages, pal *editor.Palette, ps editor.PanelState) {
	b := a.board
	ui.Column(c.Key("prop-sec-geo")).FillWidth().Gap(4).Children(func() {
		boardPropLabel(c, pal, l.BoardPropShape())
		curLabel := boardShapeName(l, ps.Geo)
		trigger := ui.Row(c.Key("prop-geo-trigger")).FillWidth().Height(26).Padding(0, 8, 0, 8).Radius(7).
			AlignItems(ui.Center).Justify(ui.SpaceBetween).
			Background(boardColor(pal.Background.Alpha(0.32))).
			Border(0.5, boardColor(pal.Edge)).Label(curLabel)
		if trigger.Hovered() || b.propGeoMenu {
			trigger.Background(boardColor(pal.BarHover))
		}
		trigger.Children(func() {
			ui.Text(c, curLabel).SingleLine().FontSize(11).FontWeight(500).TextColor(boardColor(pal.Ink))
			ui.Icon(c, boardChevronSymbol).Size(10, 10).TextColor(boardColor(pal.Ink2))
		})
		if trigger.Clicked() {
			b.propGeoMenu = !b.propGeoMenu
			b.invalidate()
		}
		ui.PopoverBase(c, trigger, &b.propGeoMenu, func(panel ui.Element) {
			panel.AttachTo(trigger, ui.AnchorStart, ui.AnchorEnd).Margin(0, 8, 0, 0).
				Padding(8).Radius(12).Clip().Background(boardColor(pal.Bar)).
				Border(0.5, boardColor(pal.Edge)).
				Shadow(0, 14, 36, 0, ui.RGBA(0, 0, 0, 0.38))
			panel.Children(func() {
				const cols = 3
				ui.Column(c.Key("prop-geo-grid")).Gap(4).Children(func() {
					for row := 0; row < len(editor.GeoKinds); row += cols {
						end := min(row+cols, len(editor.GeoKinds))
						ui.Row(c).Gap(4).Children(func() {
							for _, kind := range editor.GeoKinds[row:end] {
								kind := kind
								label := boardShapeName(l, kind)
								active := ps.Geo == kind
								cell := ui.Row(c.Key("prop-geo-"+kind)).Width(104).Height(26).
									Padding(0, 7, 0, 7).Radius(6).AlignItems(ui.Center).
									Label(label)
								switch {
								case active:
									cell.Background(boardColor(pal.AccentFill))
								case cell.Hovered():
									cell.Background(boardColor(pal.BarHover))
								}
								cell.Children(func() {
									col := pal.Ink2
									if active {
										col = pal.Accent
									}
									ui.Text(c, label).SingleLine().FontSize(11).FontWeight(500).TextColor(boardColor(col))
								})
								if cell.Clicked() {
									b.ed.SetPropGeo(kind)
									b.propGeoMenu = false
									b.invalidate()
								}
							}
						})
					}
				})
			})
		})
	})
}

func boardHeadLabel(l locales.Messages, head string) string {
	switch head {
	case "arrow":
		return l.BoardHeadArrow()
	case "triangle":
		return l.BoardHeadTriangle()
	case "square":
		return l.BoardHeadSquare()
	case "dot":
		return l.BoardHeadDot()
	case "diamond":
		return l.BoardHeadDiamond()
	case "inverted":
		return l.BoardHeadInverted()
	case "bar":
		return l.BoardHeadBar()
	default:
		return l.BoardHeadNone()
	}
}

func (a *shelf) boardPropArrowHeadsSection(c *ui.Context, l locales.Messages, pal *editor.Palette, ps editor.PanelState) {
	b := a.board
	ui.Column(c.Key("prop-sec-heads")).FillWidth().Gap(4).Children(func() {
		boardPropLabel(c, pal, l.BoardPropHeads())
		ui.Row(c.Key("prop-heads-row")).FillWidth().Gap(4).Children(func() {
			a.boardPropHeadBtn(c, l, pal, "start", l.BoardPropHeadStart(), ps.HeadStart, &b.propHeadStartMenu, func(h string) {
				b.ed.SetPropArrowHeads(h, "")
				b.invalidate()
			})
			a.boardPropHeadBtn(c, l, pal, "end", l.BoardPropHeadEnd(), ps.HeadEnd, &b.propHeadEndMenu, func(h string) {
				b.ed.SetPropArrowHeads("", h)
				b.invalidate()
			})
		})
	})
}

func (a *shelf) boardPropHeadBtn(c *ui.Context, l locales.Messages, pal *editor.Palette, which, prefix, curHead string, open *bool, onPick func(string)) {
	text := prefix + ": " + boardHeadLabel(l, curHead)
	btn := ui.Row(c.Key("prop-head-"+which)).Grow(1).Height(25).Padding(0, 6, 0, 6).Radius(6).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).
		Background(boardColor(pal.Background.Alpha(0.32))).
		Border(0.5, boardColor(pal.Edge)).Label(text)
	if btn.Hovered() || *open {
		btn.Background(boardColor(pal.BarHover))
	}
	btn.Children(func() {
		ui.Text(c, text).SingleLine().FontSize(10.5).FontWeight(500).TextColor(boardColor(pal.Ink2))
		ui.Icon(c, boardChevronSymbol).Size(9, 9).TextColor(boardColor(pal.Ink3))
	})
	btn.PopoverMenuButton(func(m *ui.Menu) {
		for _, head := range editor.ArrowHeads {
			head := head
			if m.Item(boardHeadLabel(l, head)).Checked(curHead == head).Chosen() {
				onPick(head)
			}
		}
	}, a.board.popoverButtonOpts(pal, ui.AnchorStart, ui.AnchorEnd, open))
}

func (a *shelf) boardPropFontSection(c *ui.Context, l locales.Messages, pal *editor.Palette, ps editor.PanelState) {
	b := a.board
	a.boardPropSegmentedFit(c, pal, l.BoardPropFont(), "font", []propOption{
		{"sketch", l.BoardPropFontSketch()},
		{"sans", l.BoardPropFontSans()},
		{"serif", l.BoardPropFontSerif()},
		{"mono", l.BoardPropFontMono()},
	}, ps.Font, func(v string) {
		b.ed.SetPropFont(v)
		b.invalidate()
	}, 1, 10)

	sizeOpts := make([]propOption, len(editor.FontSizes))
	curSize := fmt.Sprintf("%d", ps.FontSize)
	for i, sz := range editor.FontSizes {
		s := fmt.Sprintf("%d", sz)
		sizeOpts[i] = propOption{s, s}
	}
	a.boardPropSegmented(c, pal, l.BoardPropFontSize(), "fsize", sizeOpts, curSize, func(v string) {
		for _, sz := range editor.FontSizes {
			if fmt.Sprintf("%d", sz) == v {
				b.ed.SetPropFontSize(sz)
				b.invalidate()
				break
			}
		}
	})

	ui.Column(c.Key("prop-sec-textfmt")).FillWidth().Gap(4).Children(func() {
		boardPropLabel(c, pal, l.BoardPropText())
		ui.Row(c.Key("prop-textfmt-row")).FillWidth().Gap(3).Children(func() {
			toggles := []struct {
				id     string
				label  string
				icon   *ui.SVG
				active bool
				act    func()
			}{
				{"b", l.BoardPropBold(), boardBoldSymbol, ps.Bold, func() { b.ed.TogglePropBold(); b.invalidate() }},
				{"i", l.BoardPropItalic(), boardItalicSymbol, ps.Italic, func() { b.ed.TogglePropItalic(); b.invalidate() }},
				{"u", l.BoardPropUnderline(), boardUnderlineSymbol, ps.Underline, func() { b.ed.TogglePropUnderline(); b.invalidate() }},
			}
			for _, tg := range toggles {
				tg := tg
				btn := ui.Row(c.Key("prop-fmt-" + tg.id)).Grow(1).Height(24).Radius(6).Center().Label(tg.label)
				switch {
				case tg.active:
					btn.Background(boardColor(pal.AccentFill)).Border(0.5, boardColor(pal.Accent.Alpha(0.55)))
				case btn.Hovered():
					btn.Background(boardColor(pal.BarHover))
				default:
					btn.Background(boardColor(pal.Background.Alpha(0.28)))
				}
				btn.Children(func() {
					col := pal.Ink2
					if tg.active {
						col = pal.Accent
					}
					ui.Icon(c, tg.icon).Size(13, 13).TextColor(boardColor(col))
				})
				if btn.Clicked() {
					b.canvasFocus = false
					tg.act()
				}
			}
			if ps.ShowAlign {
				aligns := []struct {
					id    string
					label string
					icon  *ui.SVG
				}{
					{"left", l.BoardPropAlignLeft(), boardAlignLeftSymbol},
					{"center", l.BoardPropAlignCenter(), boardAlignCenterSymbol},
					{"right", l.BoardPropAlignRight(), boardAlignRightSymbol},
				}
				for _, al := range aligns {
					al := al
					isOn := ps.Align == al.id
					btn := ui.Row(c.Key("prop-align-" + al.id)).Grow(1).Height(24).Radius(6).Center().Label(al.label)
					switch {
					case isOn:
						btn.Background(boardColor(pal.AccentFill)).Border(0.5, boardColor(pal.Accent.Alpha(0.55)))
					case btn.Hovered():
						btn.Background(boardColor(pal.BarHover))
					default:
						btn.Background(boardColor(pal.Background.Alpha(0.28)))
					}
					btn.Children(func() {
						col := pal.Ink2
						if isOn {
							col = pal.Accent
						}
						ui.Icon(c, al.icon).Size(13, 13).TextColor(boardColor(col))
					})
					if btn.Clicked() {
						b.ed.SetPropAlign(al.id)
						b.canvasFocus = false
						b.invalidate()
					}
				}
			}
		})
	})

	if ps.ShowVAlign {
		a.boardPropSegmented(c, pal, l.BoardPropLabelPos(), "valign", []propOption{
			{"top", "T"},
			{"middle", "M"},
			{"bottom", "B"},
		}, ps.VAlign, func(v string) {
			b.ed.SetPropVAlign(v)
			b.invalidate()
		})
	}
}
