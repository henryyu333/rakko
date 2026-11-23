package main

import (
	"fmt"
	"math"

	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
	"rakko/locales"
)

func boardColor(c editor.Color) ui.Color {
	return ui.RGBA(c.R, c.G, c.B, float32(c.A))
}

func (a *shelf) boardView(c *ui.Context) {
	b := a.board
	if b == nil || b.ed == nil {
		return
	}
	if b.ed.DarkMode() {
		c.SetTheme(ui.DarkTheme())
	} else {
		c.SetTheme(ui.LightTheme())
	}
	l := loc(c)
	pal := b.ed.Palette()
	c.Root().Background(boardColor(pal.Background))
	ui.Column(c.Key("board-root")).Fill().Children(func() {
		a.boardBar(c, l, pal)
		a.boardStage(c, l, pal)
	})
	if b.confirmDelete {
		a.boardDeleteModal(c, l, pal)
	}
}

func (a *shelf) boardBar(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	ui.Row(c.Key("board-bar")).Height(boardBarH).Shrink(0).FillWidth().
		Background(boardColor(pal.TopBar)).
		AlignItems(ui.Center).Padding(0, 8, 0, 10).Gap(4).DragWindow().Children(func() {
		if b.renaming {
			in := ui.TextInputBase(c.Key("board-rename"), &b.renameDraft).
				Width(180).Height(22).Padding(2, 6, 2, 6).Radius(6).
				Background(boardColor(pal.BarActive)).
				FontSize(11.5).TextColor(boardColor(pal.Ink)).
				Label(l.BoardRename())
			if !b.renameBoot {
				in.Focus()
				b.renameBoot = true
			}
			in.OnTextKey(func(k ui.TextKey) bool {
				switch k.Key {
				case ui.KeyEnter:
					b.commitRename()
					return true
				case ui.KeyEscape:
					b.cancelRename()
					return true
				}
				return false
			})
		} else {
			a.boardSwitcherButton(c, l, pal)
		}
		ui.Box(c).Grow(1).FillHeight().DragWindow()
		a.boardBarButton(c, pal, "board-reveal", revealSymbol, osText(l.BoardReveal(), l.BoardRevealWin()), false, func() {
			if b.win != nil {
				b.frame = b.win.Bounds()
			}
			boardReveal(b.store.Path())
		})
		a.boardBarButton(c, pal, "board-top", pinSymbol, l.BoardKeepOnTop(), b.top, func() {
			b.setTop(!b.top)
		})
		a.boardBarButton(c, pal, "board-close", closeSymbol, l.BoardClose(), false, b.close)
	})
}

func (a *shelf) boardSwitcherButton(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	title := b.currentTitle(l)
	btn := ui.Row(c.Key("board-title-btn")).Height(22).Padding(0, 7, 0, 7).Radius(6).
		AlignItems(ui.Center).Gap(4).Label(title).Tooltip(title)
	if btn.Hovered() || b.titleMenu {
		btn.Background(boardColor(pal.BarHover))
	}
	btn.Children(func() {
		ui.Text(c, title).SingleLine().FontSize(11.5).FontWeight(600).TextColor(boardColor(pal.Ink2))
		ui.Icon(c, boardChevronSymbol).Size(10, 10).TextColor(boardColor(pal.Ink2))
	})
	btn.PopoverMenuButton(func(m *ui.Menu) {
		b.refreshBoards()
		curID := ""
		if b.store != nil {
			curID = b.store.ID()
		}
		for _, info := range b.boards {
			info := info
			it := m.Item(info.Name).Checked(info.ID == curID).Trailing(boardCountText(l, info.ItemCount))
			if it.Chosen() {
				if err := b.selectBoard(info.ID); err != nil {
					a.report(err)
				}
			}
		}
		m.Separator()
		if m.Item(l.BoardNew()).Shortcut(ui.Cmd, ui.KeyN).Chosen() {
			if err := b.newBoardDoc(); err != nil {
				a.report(err)
			}
		}
		if m.Item(l.BoardRename()).Disabled(b.locked).Chosen() {
			b.startRename()
		}
		delItem := m.Item(l.BoardDelete(title)).Disabled(len(b.boards) < 2)
		if delItem.Chosen() {
			b.confirmDelete = true
			b.invalidate()
		}
	}, b.popoverButtonOpts(pal, ui.AnchorBottomStart, ui.AnchorTopStart, &b.titleMenu))
}

func (a *shelf) boardBarButton(c *ui.Context, pal *editor.Palette, key string, icon *ui.SVG, label string, on bool, act func()) {
	btn := ui.Row(c.Key(key)).Size(22, 22).Radius(6).Center().Label(label).Tooltip(label)
	switch {
	case on:
		btn.Background(boardColor(pal.AccentFill))
	case btn.Hovered():
		btn.Background(boardColor(pal.BarHover))
	}
	btn.Children(func() {
		col := pal.Ink2
		if on {
			col = pal.Accent
		}
		ui.Icon(c, icon).Size(13, 13).TextColor(boardColor(col))
	})
	if btn.Clicked() && act != nil {
		act()
	}
}

func (a *shelf) boardStage(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	if b.locked {
		ui.Column(c.Key("board-locked")).FillWidth().Grow(1).MinHeight(0).Center().Padding(0, 24, 24, 24).
			Children(func() {
				panelLines(c, l.BoardUnreadable(), 12, 0).TextAlign(ui.Center).TextColor(boardColor(pal.Ink3))
			})
		return
	}
	stage := ui.Box(c.Key("board-stage")).FillWidth().Grow(1).MinHeight(0).Clip()
	stage.Children(func() {
		canvas := ui.Box(c.Key("board-canvas")).Fill().Focusable().Label(b.currentTitle(l))
		if !b.canvasFocus && !b.renaming && !b.confirmDelete {
			canvas.Focus()
			b.canvasFocus = true
		}
		if b.panDrag {
			canvas.Cursor(ui.CursorGrabbing)
		} else if b.spaceHeld {
			canvas.Cursor(ui.CursorGrab)
		}
		canvas.Draw(func(p *ui.Painter, r ui.Rect) {
			b.stageRect = r
			b.paintStage(p, r)
		})
		canvas.HandleInput(func(ev ui.InputEvent) bool {
			return b.handleCanvasInput(c, ev)
		})

		if b.ed.ItemCount() == 0 {
			ui.Column(c.Key("board-hint")).Absolute().Top(0).Left(0).Fill().Center().IgnoreInput(true).Children(func() {
				ui.Text(c, osText(l.BoardHint(), l.BoardHintWin())).FontSize(13).TextColor(boardColor(pal.Ink3))
			})
		}

		a.boardNavBar(c, l, pal)
	})
}

func (a *shelf) boardNavBar(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	pct := int(math.Round(b.ed.Cam().Z * 100))
	nav := ui.Row(c.Key("board-nav")).Absolute().Left(16).Bottom(16).Height(40).
		Padding(0, 6, 0, 6).Gap(2).Radius(12).Clip().AlignItems(ui.Center).
		Background(boardColor(pal.Bar)).Border(0.5, boardColor(pal.Edge)).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.28))
	nav.Children(func() {
		zout := a.boardNavIconBtn(c, pal, "board-zout", minusSymbol, l.BoardZoomOut(), false)
		if zout.Clicked() {
			b.ed.ZoomOut()
			b.invalidate()
		}

		zbtn := ui.Row(c.Key("board-zoom-pct")).Height(28).MinWidth(44).Padding(0, 6, 0, 6).Radius(7).Center().
			Label(l.BoardZoomLabel()).Tooltip(l.BoardZoomLabel())
		if zbtn.Hovered() || b.zoomMenu {
			zbtn.Background(boardColor(pal.BarHover))
		}
		zbtn.Children(func() {
			ui.Text(c, fmt.Sprintf("%d%%", pct)).SingleLine().FontSize(11.5).FontWeight(500).TextColor(boardColor(pal.Ink2))
		})
		zbtn.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(l.BoardZoomIn()).Shortcut(ui.Cmd, ui.KeyEqual).Chosen() {
				b.ed.ZoomIn()
				b.invalidate()
			}
			if m.Item(l.BoardZoomOut()).Shortcut(ui.Cmd, ui.KeyMinus).Chosen() {
				b.ed.ZoomOut()
				b.invalidate()
			}
			m.Separator()
			if m.Item(l.BoardZoomReset()).Shortcut(ui.Shift, ui.Key0).Chosen() {
				b.ed.ResetZoom()
				b.invalidate()
			}
		}, b.popoverButtonOpts(pal, ui.AnchorTopStart, ui.AnchorBottomStart, &b.zoomMenu))

		zin := a.boardNavIconBtn(c, pal, "board-zin", plusSymbol, l.BoardZoomIn(), false)
		if zin.Clicked() {
			b.ed.ZoomIn()
			b.invalidate()
		}

		ui.Box(c).Width(1).Height(20).Margin(0, 4, 0, 4).Background(boardColor(pal.Sep))

		more := a.boardNavIconBtn(c, pal, "board-more", boardMoreSymbol, l.BoardMore(), b.moreMenu)
		more.PopoverMenuButton(func(m *ui.Menu) {
			curPaper := b.ed.Paper()
			papers := []struct {
				id    string
				label string
			}{
				{doc.PaperDots, l.BoardPaperDots()},
				{doc.PaperGrid, l.BoardPaperGrid()},
				{doc.PaperLines, l.BoardPaperLines()},
				{doc.PaperBlank, l.BoardPaperBlank()},
			}
			for _, p := range papers {
				p := p
				if m.Item(l.BoardPaperSection() + " · " + p.label).Checked(curPaper == p.id).Chosen() {
					b.ed.SetPaper(p.id)
					b.invalidate()
				}
			}
			m.Separator()
			dark := b.ed.DarkMode()
			if m.Item(l.BoardThemeSection() + " · " + l.BoardThemeDark()).Checked(dark).Chosen() {
				b.setDarkMode(true)
			}
			if m.Item(l.BoardThemeSection() + " · " + l.BoardThemeLight()).Checked(!dark).Chosen() {
				b.setDarkMode(false)
			}
		}, b.popoverButtonOpts(pal, ui.AnchorTopStart, ui.AnchorBottomStart, &b.moreMenu))
	})
}

func (a *shelf) boardNavIconBtn(c *ui.Context, pal *editor.Palette, key string, icon *ui.SVG, label string, active bool) ui.Element {
	btn := ui.Row(c.Key(key)).Size(28, 28).Radius(8).Center().Label(label).Tooltip(label)
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
		ui.Icon(c, icon).Size(15, 15).TextColor(boardColor(col))
	})
	return btn
}

func (b *boardUI) popoverButtonOpts(pal *editor.Palette, at, self ui.Anchor, open *bool) ui.PopoverMenuButtonOptions {
	return ui.PopoverMenuButtonOptions{
		At:   at,
		Self: self,
		Open: open,
		Panel: func(panel ui.Element) {
			panel.Background(boardColor(pal.Bar)).Radius(10).
				Border(0.5, boardColor(pal.Edge)).
				NoShadow().Shadow(0, 14, 36, 0, ui.RGBA(0, 0, 0, 0.38))
		},
		Item: func(item ui.Element) {
			item.Radius(6)
		},
	}
}

func (a *shelf) boardDeleteModal(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	name := b.currentTitle(l)
	backdrop := ui.Box(c.Key("board-del-modal")).Modal().Absolute().Top(0).Left(0).Fill().
		Background(ui.RGBA(0, 0, 0, 0.45)).Center()
	backdrop.Children(func() {
		card := ui.Column(c.Key("board-del-card")).Width(320).Padding(16, 16, 14, 16).Gap(14).
			Radius(12).Clip().Background(boardColor(pal.Bar)).Border(0.5, boardColor(pal.Edge)).
			Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.45))
		card.Children(func() {
			ui.Text(c, osText(l.BoardDeleteConfirm(name), l.BoardDeleteConfirmWin(name))).
				FontSize(13).LineHeight(1.45).TextColor(boardColor(pal.Ink))
			ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
				cancel := ui.Row(c.Key("board-del-cancel")).Height(28).Padding(0, 12, 0, 12).Radius(7).Center().
					Background(boardColor(pal.BarActive)).Label(l.BoardCancel())
				cancel.Children(func() {
					ui.Text(c, l.BoardCancel()).SingleLine().FontSize(12).FontWeight(500).TextColor(boardColor(pal.Ink))
				})
				if cancel.Clicked() {
					b.confirmDelete = false
					b.invalidate()
				}

				delLabel := osText(l.BoardMoveToTrash(), l.BoardMoveToTrashWin())
				del := ui.Row(c.Key("board-del-ok")).Height(28).Padding(0, 12, 0, 12).Radius(7).Center().
					Background(danger).Label(delLabel)
				del.Children(func() {
					ui.Text(c, delLabel).SingleLine().FontSize(12).FontWeight(600).TextColor(ui.Hex("#FFFFFF"))
				})
				if del.Clicked() {
					if err := b.trashCurrent(); err != nil {
						a.report(err)
					}
				}
			})
		})
	})
}

func (b *boardUI) paintStage(p *ui.Painter, r ui.Rect) {
	b.ed.SetViewport(float64(r.W), float64(r.H))
	pal := b.ed.Palette()
	p.Fill(r, boardColor(pal.Background), 0)
	p.Clip(r, 0, func() {
		b.paintPaper(p, r, pal)
	})
}

func (b *boardUI) paintPaper(p *ui.Painter, r ui.Rect, pal *editor.Palette) {
	paper := b.ed.Paper()
	if paper == doc.PaperBlank {
		return
	}
	cam := b.ed.Cam()
	step := b.ed.GridStep() * cam.Z
	if step <= 0 {
		return
	}
	for step < 12 {
		step *= 2
	}
	ox := math.Mod(cam.X*cam.Z, step)
	if ox < 0 {
		ox += step
	}
	oy := math.Mod(cam.Y*cam.Z, step)
	if oy < 0 {
		oy += step
	}
	startX := float64(r.X) + ox
	startY := float64(r.Y) + oy
	endX := float64(r.X + r.W)
	endY := float64(r.Y + r.H)

	switch paper {
	case doc.PaperDots:
		dotCol := boardColor(pal.PaperDot)
		const dotSize = float32(2)
		for y := startY; y < endY; y += step {
			for x := startX; x < endX; x += step {
				p.Fill(ui.Rect{X: float32(x) - dotSize/2, Y: float32(y) - dotSize/2, W: dotSize, H: dotSize}, dotCol, dotSize/2)
			}
		}
	case doc.PaperGrid:
		lineCol := boardColor(pal.PaperLine)
		for x := startX; x < endX; x += step {
			p.Line(float32(x), r.Y, float32(x), r.Y+r.H, 1, lineCol)
		}
		for y := startY; y < endY; y += step {
			p.Line(r.X, float32(y), r.X+r.W, float32(y), 1, lineCol)
		}
	case doc.PaperLines:
		lineCol := boardColor(pal.PaperLine)
		for y := startY; y < endY; y += step {
			p.Line(r.X, float32(y), r.X+r.W, float32(y), 1, lineCol)
		}
	}
}

func (b *boardUI) handleCanvasInput(c *ui.Context, ev ui.InputEvent) bool {
	if b.ed == nil {
		return false
	}
	pt := geo.V(float64(ev.X), float64(ev.Y))
	switch ev.Kind {
	case ui.InputScroll:
		if ev.Mods&(ui.Cmd|ui.Ctrl) != 0 {
			b.ed.ZoomAt(pt, math.Exp(-float64(ev.DY)*0.01))
		} else {
			b.ed.Pan(float64(ev.DX), float64(ev.DY))
		}
		b.invalidate()
		return true
	case ui.InputZoom:
		b.ed.ZoomAt(pt, float64(ev.Scale))
		b.invalidate()
		return true
	case ui.InputPointerDown:
		if ev.Button == 2 || (ev.Button == 0 && b.spaceHeld) {
			b.panDrag = true
			b.panLastX = ev.X
			b.panLastY = ev.Y
			return true
		}
	case ui.InputPointerMove:
		if b.panDrag {
			dx := float64(ev.X - b.panLastX)
			dy := float64(ev.Y - b.panLastY)
			b.panLastX = ev.X
			b.panLastY = ev.Y
			b.ed.Pan(-dx, -dy)
			b.invalidate()
			return true
		}
	case ui.InputPointerUp:
		if b.panDrag {
			b.panDrag = false
			b.invalidate()
			return true
		}
	case ui.InputKeyDown:
		if ev.Key == ui.KeySpace {
			b.spaceHeld = true
			return true
		}
		if ev.Mods&(ui.Cmd|ui.Ctrl) != 0 {
			switch ev.Key {
			case ui.KeyEqual:
				b.ed.ZoomIn()
				b.invalidate()
				return true
			case ui.KeyMinus:
				b.ed.ZoomOut()
				b.invalidate()
				return true
			case ui.Key0:
				b.ed.ResetZoom()
				b.invalidate()
				return true
			case ui.KeySlash:
				b.setDarkMode(!b.ed.DarkMode())
				return true
			case ui.KeyN:
				if err := b.newBoardDoc(); err != nil {
					b.host.report(err)
				}
				return true
			case ui.KeyW:
				b.close()
				return true
			}
		}
		if ev.Mods&ui.Shift != 0 && ev.Key == ui.Key0 {
			b.ed.ResetZoom()
			b.invalidate()
			return true
		}
	case ui.InputKeyUp:
		if ev.Key == ui.KeySpace {
			b.spaceHeld = false
			return true
		}
	}
	return false
}
