package main

import (
	"math"

	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
	"rakko/locales"
)

func (a *shelf) boardPageSwitcherButton(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	if b.renamingPage {
		in := ui.TextInputBase(c.Key("board-page-rename"), &b.renamePageDraft).
			Width(130).Height(22).Padding(2, 6, 2, 6).Radius(6).
			Background(boardColor(pal.BarActive)).
			FontSize(11).TextColor(boardColor(pal.Ink)).
			Label(l.BoardPageRename())
		if !b.renamePageBoot {
			in.Focus()
			b.renamePageBoot = true
		}
		in.OnTextKey(func(k ui.TextKey) bool {
			switch k.Key {
			case ui.KeyEnter:
				b.commitPageRename()
				return true
			case ui.KeyEscape:
				b.cancelPageRename()
				return true
			}
			return false
		})
		return
	}

	pages := b.ed.Pages()
	curName := ""
	if pg := b.ed.ActivePageInfo(); pg != nil {
		curName = b.pageTitle(l, pg)
	}
	if curName == "" {
		return
	}
	btn := ui.Row(c.Key("board-page-btn")).Height(22).Padding(0, 7, 0, 7).Radius(6).
		AlignItems(ui.Center).Gap(4).Label(curName).Tooltip(l.BoardPageCount(len(pages)))
	if btn.Hovered() || b.pageMenu {
		btn.Background(boardColor(pal.BarHover))
	}
	btn.Children(func() {
		ui.Text(c, curName).SingleLine().FontSize(11).FontWeight(500).TextColor(boardColor(pal.Ink3))
		ui.Icon(c, boardChevronSymbol).Size(9, 9).TextColor(boardColor(pal.Ink3))
	})
	btn.PopoverMenuButton(func(m *ui.Menu) {
		for _, pg := range pages {
			pg := pg
			cnt := b.ed.PageItemCount(pg.ID)
			it := m.Item(b.pageTitle(l, pg)).Checked(pg.ID == b.ed.Page).Trailing(boardCountText(l, cnt))
			if it.Chosen() {
				b.ed.SwitchPage(pg.ID)
				b.invalidate()
			}
		}
		m.Separator()
		if m.Item(l.BoardPageAdd()).Disabled(b.locked).Chosen() {
			b.ed.AddPage(b.nextPageName(l))
			b.invalidate()
		}
		if m.Item(l.BoardPageRename()).Disabled(b.locked).Chosen() {
			b.startPageRename()
		}
		if m.Item(l.BoardPageDelete()).Disabled(b.locked || len(pages) <= 1).Danger().Chosen() {
			b.confirmPageDelete = true
			b.invalidate()
		}
	}, b.popoverButtonOpts(pal, ui.AnchorBottomStart, ui.AnchorTopStart, &b.pageMenu))
}

func (a *shelf) boardBackPill(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	if b.ed.ContentVisible() {
		return
	}
	bottom := b.boardToolbarBottom() + 44 + 12
	if b.ed.Tool == editor.ToolDraw || b.ed.Tool == editor.ToolEraser {
		bottom += 42
	}
	wrap := ui.Row(c.Key("board-back-wrap")).Absolute().Left(0).Right(0).Bottom(bottom).Justify(ui.Center).PassThrough()
	wrap.Children(func() {
		pill := ui.Row(c.Key("board-back-pill")).Height(32).Padding(0, 12, 0, 12).Gap(6).
			Radius(16).AlignItems(ui.Center).
			Background(boardColor(pal.AccentFill)).
			Border(0.5, boardColor(pal.Accent.Alpha(0.5))).
			Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.28)).
			Label(l.BoardBackToContent())
		pill.Children(func() {
			ui.Icon(c, boardFitSymbol).Size(13, 13).TextColor(boardColor(pal.Accent))
			ui.Text(c, l.BoardBackToContent()).SingleLine().FontSize(12).FontWeight(500).TextColor(boardColor(pal.Accent))
		})
		if pill.Clicked() {
			b.ed.ZoomToFit()
			b.invalidate()
		}
	})
}

func (a *shelf) boardFocusExitPill(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	pill := ui.Row(c.Key("board-focus-exit")).Absolute().Top(14).Right(16).
		Height(30).Padding(0, 12, 0, 12).Gap(6).Radius(15).AlignItems(ui.Center).
		Background(boardColor(pal.Bar)).Border(0.5, boardColor(pal.Edge)).
		Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.28)).
		Label(l.BoardFocusExit())
	if pill.Hovered() {
		pill.Background(boardColor(pal.BarHover))
	}
	pill.Children(func() {
		ui.Icon(c, boardFocusSymbol).Size(13, 13).TextColor(boardColor(pal.Ink2))
		ui.Text(c, l.BoardFocusExit()).SingleLine().FontSize(11.5).FontWeight(500).TextColor(boardColor(pal.Ink2))
	})
	if pill.Clicked() {
		b.toggleFocusMode()
	}
}

type miniTransform struct {
	ox, oy, scale float64
}

func (b *boardUI) miniMapTransform() (miniTransform, geo.Box, bool) {
	const (
		w   = 180.0
		h   = 120.0
		pad = 8.0
	)
	vb := b.ed.ViewBox()
	u, ok := b.ed.PageContentBox(b.ed.Page)
	if ok {
		u = u.Union(vb)
	} else {
		u = vb
	}
	spanW := math.Max(u.W, 1)
	spanH := math.Max(u.H, 1)
	sc := math.Min((w-pad*2)/spanW, (h-pad*2)/spanH)
	return miniTransform{
		ox:    u.X - (w/sc-u.W)/2,
		oy:    u.Y - (h/sc-u.H)/2,
		scale: sc,
	}, vb, ok
}

func (m miniTransform) toWorld(x, y float64) geo.Vec {
	return geo.V(m.ox+x/m.scale, m.oy+y/m.scale)
}

func (a *shelf) boardMinimap(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	if !b.minimap {
		return
	}
	const (
		mw = float32(180)
		mh = float32(120)
	)
	miniBottom := float32(62)
	if _, stacked, _ := b.boardChrome(); stacked {
		miniBottom = b.boardToolbarBottom() + 52
		if b.ed.Tool == editor.ToolDraw || b.ed.Tool == editor.ToolEraser {
			miniBottom += 42
		}
	}
	mini := ui.Box(c.Key("board-minimap")).Absolute().Left(12).Bottom(miniBottom).Size(mw, mh).
		Radius(10).Clip().
		Background(boardColor(pal.Bar)).
		Border(0.5, boardColor(pal.Edge)).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.30)).
		Label(l.BoardMinimapLabel())

	mini.Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(r, boardColor(pal.Bar), 10)
		tf, vb, _ := b.miniMapTransform()
		var walk func(parent doc.ID)
		walk = func(parent doc.ID) {
			for _, id := range b.ed.Children(parent) {
				s := b.ed.Doc.Shapes[id]
				if s == nil {
					continue
				}
				if s.Type == doc.TypeGroup || s.Type == doc.TypeFrame {
					walk(id)
					if s.Type == doc.TypeGroup {
						continue
					}
				}
				box := b.ed.PageBounds(id)
				rx := float32((box.X-tf.ox)*tf.scale) + r.X
				ry := float32((box.Y-tf.oy)*tf.scale) + r.Y
				rw := max(float32(box.W*tf.scale), 2)
				rh := max(float32(box.H*tf.scale), 2)
				col := pal.Ink3.Alpha(0.38)
				if b.ed.IsSelected(id) {
					col = pal.Accent.Alpha(0.55)
				}
				p.Fill(ui.Rect{X: rx, Y: ry, W: rw, H: rh}, boardColor(col), 1)
			}
		}
		walk(b.ed.Page)

		vx := float32((vb.X-tf.ox)*tf.scale) + r.X
		vy := float32((vb.Y-tf.oy)*tf.scale) + r.Y
		vw := max(float32(vb.W*tf.scale), 6)
		vh := max(float32(vb.H*tf.scale), 6)
		p.Stroke(ui.Rect{X: vx, Y: vy, W: vw, H: vh}, boardColor(pal.Accent), 2, 1.5)
	})

	mini.HandleInput(func(ev ui.InputEvent) bool {
		tf, _, _ := b.miniMapTransform()
		switch ev.Kind {
		case ui.InputPointerDown:
			if ev.Button == 0 {
				b.miniDrag = true
				b.ed.CenterOn(tf.toWorld(float64(ev.X), float64(ev.Y)))
				b.invalidate()
				return true
			}
		case ui.InputPointerMove:
			if b.miniDrag {
				b.ed.CenterOn(tf.toWorld(float64(ev.X), float64(ev.Y)))
				b.invalidate()
				return true
			}
		case ui.InputPointerUp:
			if b.miniDrag {
				b.miniDrag = false
				b.ed.CenterOn(tf.toWorld(float64(ev.X), float64(ev.Y)))
				b.invalidate()
				return true
			}
		}
		return false
	})
}

func (b *boardUI) openRadialPalette(at geo.Vec) {
	if b.ed == nil {
		return
	}
	w := float64(b.stageRect.W)
	if w <= 0 {
		w = b.ed.VW
	}
	h := float64(b.stageRect.H)
	if h <= 0 {
		h = b.ed.VH
	}
	if at.X <= 0 && at.Y <= 0 {
		at = geo.V(w/2, h/2)
	}
	at.X = geo.Clamp(at.X, 112, math.Max(112, w-112))
	at.Y = geo.Clamp(at.Y, 112, math.Max(112, h-112))
	b.radialPt = at
	b.radialOn = true
	b.invalidate()
}

func (a *shelf) boardRadialPalette(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	if !b.radialOn || b.ed == nil {
		return
	}
	const size = float32(204)
	cx, cy := float32(102), float32(102)
	ringR := 72.0

	wheel := ui.Box(c.Key("board-radial-palette")).Absolute().
		Left(float32(b.radialPt.X)-cx).Top(float32(b.radialPt.Y)-cy).
		Size(size, size).Radius(cx).Clip().
		Background(boardColor(pal.Bar)).
		Border(0.5, boardColor(pal.Edge)).
		Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.42)).
		Label(l.BoardRadialPalette())

	activeHex := b.ed.PanelState().Color
	wheel.Children(func() {
		for i, hex := range editor.Swatches {
			hex := hex
			ang := -math.Pi/2 + 2*math.Pi*float64(i)/float64(len(editor.Swatches))
			sx := float32(float64(cx)+math.Cos(ang)*ringR) - 12
			sy := float32(float64(cy)+math.Sin(ang)*ringR) - 12
			col := editor.ResolveColor(hex, pal.Dark, doc.LookClean)
			isActive := hex == activeHex
			cell := ui.Row(c.Key("radial-sw-"+hex)).Absolute().Left(sx).Top(sy).Size(24, 24).
				Radius(12).Center().Label(l.BoardRadialPalette() + " " + hex).Tooltip(hex)
			if isActive {
				cell.Border(2, boardColor(pal.Accent))
			} else if cell.Hovered() {
				cell.Border(1.5, boardColor(pal.Ink2))
			}
			cell.Children(func() {
				dot := ui.Box(c).Size(17, 17).Radius(9).Background(boardColor(col))
				if !pal.Dark && hex == "#E8E8EA" {
					dot.Border(0.5, boardColor(pal.Edge))
				}
			})
			if cell.Clicked() {
				b.ed.SetPropStroke(hex)
				b.radialOn = false
				b.canvasFocus = false
				b.invalidate()
			}
		}

		// Center live preview dot showing active color; clicking closes wheel.
		curCol := editor.ResolveColor(activeHex, pal.Dark, b.ed.Look())
		centerBtn := ui.Row(c.Key("radial-center")).Absolute().
			Left(cx-24).Top(cy-24).Size(48, 48).Radius(24).Center().
			Background(boardColor(pal.Background.Alpha(0.45))).
			Border(0.5, boardColor(pal.Edge)).
			Label(l.BoardRadialPalette())
		centerBtn.Children(func() {
			ui.Box(c).Size(22, 22).Radius(11).Background(boardColor(curCol))
		})
		if centerBtn.Clicked() {
			b.radialOn = false
			b.canvasFocus = false
			b.invalidate()
		}
	})
}

func (b *boardUI) buildContextMenu(c *ui.Context, l locales.Messages, m *ui.Menu) {
	if b.locked || b.ed == nil {
		return
	}
	sel := b.ed.Selection()
	if len(sel) == 0 {
		if m.Item(l.BoardCtxPaste()).Shortcut(ui.Cmd, ui.KeyV).Disabled(!b.ed.HasClipboard()).Chosen() {
			b.ed.Paste()
			b.invalidate()
		}
		m.Separator()
		m.Submenu(l.BoardCtxCopyAs(), func(sm *ui.Menu) {
			if sm.Item("PNG").Shortcut(ui.Cmd|ui.Shift, ui.KeyC).Chosen() {
				_ = b.copyAsPNG(nil, false)
			}
			if sm.Item("SVG").Chosen() {
				_ = b.copyAsSVG(nil, false)
			}
		})
		m.Submenu(l.BoardCtxExportAs(), func(sm *ui.Menu) {
			if sm.Item(l.BoardExportPNG()).Chosen() {
				b.exportPNGFile(nil)
			}
			if sm.Item(l.BoardExportSVG()).Chosen() {
				b.exportSVGFile(nil)
			}
		})
		m.Separator()
		if m.Item(l.BoardCtxSelectAll()).Shortcut(ui.Cmd, ui.KeyA).Chosen() {
			b.ed.SelectAll()
			b.invalidate()
		}
		if m.Item(l.BoardZoomFit()).Shortcut(ui.Shift, ui.Key1).Chosen() {
			b.ed.ZoomToFit()
			b.invalidate()
		}
		if m.Item(l.BoardCtxUnlockAll()).Disabled(!b.ed.HasLockedOnPage()).Chosen() {
			b.ed.UnlockAll()
			b.invalidate()
		}
		m.Separator()
		if m.Item(l.BoardFocusMode()).Shortcut(ui.Cmd, ui.KeyPeriod).Checked(b.focusMode).Chosen() {
			b.toggleFocusMode()
		}
		return
	}

	n := len(sel)
	if m.Item(l.BoardCtxCut()).Shortcut(ui.Cmd, ui.KeyX).Chosen() {
		b.cutSelection(c)
		b.invalidate()
	}
	if m.Item(l.BoardCtxCopy()).Shortcut(ui.Cmd, ui.KeyC).Chosen() {
		b.copySelection(c)
		b.invalidate()
	}
	if m.Item(l.BoardCtxPaste()).Shortcut(ui.Cmd, ui.KeyV).Disabled(!b.ed.HasClipboard()).Chosen() {
		b.ed.Paste()
		b.invalidate()
	}
	if m.Item(l.BoardCtxDuplicate()).Shortcut(ui.Cmd, ui.KeyD).Chosen() {
		b.ed.Duplicate()
		b.invalidate()
	}
	if m.Item(l.BoardCtxDelete()).Danger().Chosen() {
		b.ed.DeleteSelected()
		b.invalidate()
	}
	m.Separator()

	if m.Item(l.BoardCtxCopyStyle()).Shortcut(ui.Cmd|ui.Alt, ui.KeyC).Chosen() {
		b.ed.CopyStyle()
		b.invalidate()
	}
	if m.Item(l.BoardCtxPasteStyle()).Shortcut(ui.Cmd|ui.Alt, ui.KeyV).Disabled(!b.ed.HasStyleClip()).Chosen() {
		b.ed.PasteStyle()
		b.invalidate()
	}
	m.Separator()

	m.Submenu(l.BoardCtxArrange(), func(sm *ui.Menu) {
		if sm.Item(l.BoardCtxAlignLeft()).Shortcut(ui.Alt, ui.KeyA).Chosen() {
			b.ed.AlignSelected("left")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxAlignCenter()).Shortcut(ui.Alt, ui.KeyH).Chosen() {
			b.ed.AlignSelected("center")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxAlignRight()).Shortcut(ui.Alt, ui.KeyD).Chosen() {
			b.ed.AlignSelected("right")
			b.invalidate()
		}
		sm.Separator()
		if sm.Item(l.BoardCtxAlignTop()).Shortcut(ui.Alt, ui.KeyW).Chosen() {
			b.ed.AlignSelected("top")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxAlignMiddle()).Shortcut(ui.Alt, ui.KeyV).Chosen() {
			b.ed.AlignSelected("middle")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxAlignBottom()).Shortcut(ui.Alt, ui.KeyS).Chosen() {
			b.ed.AlignSelected("bottom")
			b.invalidate()
		}
		sm.Separator()
		if sm.Item(l.BoardCtxDistH()).Shortcut(ui.Alt|ui.Shift, ui.KeyH).Disabled(n < 3).Chosen() {
			b.ed.DistributeSelected("x")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxDistV()).Shortcut(ui.Alt|ui.Shift, ui.KeyV).Disabled(n < 3).Chosen() {
			b.ed.DistributeSelected("y")
			b.invalidate()
		}
		sm.Separator()
		if sm.Item(l.BoardCtxStackH()).Chosen() {
			b.ed.StackSelected("x")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxStackV()).Chosen() {
			b.ed.StackSelected("y")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxPack()).Chosen() {
			b.ed.PackSelected()
			b.invalidate()
		}
		sm.Separator()
		if sm.Item(l.BoardCtxStretchW()).Chosen() {
			b.ed.StretchSelected("w")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxStretchH()).Chosen() {
			b.ed.StretchSelected("h")
			b.invalidate()
		}
	}).Disabled(n < 2)

	m.Submenu(l.BoardCtxOrder(), func(sm *ui.Menu) {
		if sm.Item(l.BoardCtxBringFront()).Shortcut(ui.Cmd|ui.Shift, ui.KeyBracketRight).Chosen() {
			b.ed.BringToFront()
			b.invalidate()
		}
		if sm.Item(l.BoardCtxBringForward()).Shortcut(ui.Cmd, ui.KeyBracketRight).Chosen() {
			b.ed.BringForward()
			b.invalidate()
		}
		if sm.Item(l.BoardCtxSendBackward()).Shortcut(ui.Cmd, ui.KeyBracketLeft).Chosen() {
			b.ed.SendBackward()
			b.invalidate()
		}
		if sm.Item(l.BoardCtxSendBack()).Shortcut(ui.Cmd|ui.Shift, ui.KeyBracketLeft).Chosen() {
			b.ed.SendToBack()
			b.invalidate()
		}
	})

	m.Submenu(l.BoardCtxTransform(), func(sm *ui.Menu) {
		if sm.Item(l.BoardCtxRotateCW()).Shortcut(ui.Shift, ui.KeyPeriod).Chosen() {
			b.ed.RotateSelected90(1)
			b.invalidate()
		}
		if sm.Item(l.BoardCtxRotateCCW()).Shortcut(ui.Shift, ui.KeyComma).Chosen() {
			b.ed.RotateSelected90(-1)
			b.invalidate()
		}
		sm.Separator()
		if sm.Item(l.BoardCtxFlipH()).Shortcut(ui.Shift, ui.KeyH).Chosen() {
			b.ed.FlipSelected("x")
			b.invalidate()
		}
		if sm.Item(l.BoardCtxFlipV()).Shortcut(ui.Shift, ui.KeyV).Chosen() {
			b.ed.FlipSelected("y")
			b.invalidate()
		}
	}).Disabled(!b.ed.HasTransformableSelected())

	if pages := b.ed.Pages(); len(pages) > 1 {
		m.Submenu(l.BoardPageMoveTo(), func(sm *ui.Menu) {
			for _, pg := range pages {
				pg := pg
				if pg.ID == b.ed.Page {
					continue
				}
				if sm.Item(pg.Name).Chosen() {
					b.ed.MoveSelectedToPage(pg.ID)
					b.invalidate()
				}
			}
		})
	}

	if b.ed.HasGroupedSelected() {
		if m.Item(l.BoardCtxUngroup()).Shortcut(ui.Cmd|ui.Shift, ui.KeyG).Chosen() {
			b.ed.UngroupSelected()
			b.invalidate()
		}
	} else {
		if m.Item(l.BoardCtxGroup()).Shortcut(ui.Cmd, ui.KeyG).Disabled(n < 2).Chosen() {
			b.ed.GroupSelected()
			b.invalidate()
		}
	}

	single := b.ed.Doc.Shapes[sel[0]]
	canEditText := n == 1 && single != nil && single.Type != doc.TypeImage && single.Type != doc.TypeDraw && single.Type != doc.TypeHighlight
	if m.Item(l.BoardCtxEditText()).Shortcut(0, ui.KeyEnter).Disabled(!canEditText).Chosen() {
		b.ed.StartEditing(sel[0])
		b.invalidate()
	}
	isImage := n == 1 && single != nil && single.Type == doc.TypeImage
	if isImage {
		if m.Item(l.BoardCtxCropImage()).Chosen() {
			b.ed.EnterCrop(sel[0])
			b.invalidate()
		}
		if b.ed.CanResetCrop() {
			if m.Item(l.BoardCtxResetCrop()).Chosen() {
				b.ed.ResetCrop(sel[0])
				b.invalidate()
			}
		}
	}
	lockLabel := l.BoardCtxLock()
	if b.ed.AllSelectionLocked() {
		lockLabel = l.BoardCtxUnlock()
	}
	if m.Item(lockLabel).Shortcut(ui.Cmd|ui.Shift, ui.KeyL).Chosen() {
		b.ed.ToggleLockSelected()
		b.invalidate()
	}
	m.Separator()
	m.Submenu(l.BoardCtxCopyAs(), func(sm *ui.Menu) {
		if sm.Item("PNG").Shortcut(ui.Cmd|ui.Shift, ui.KeyC).Chosen() {
			_ = b.copyAsPNG(sel, false)
		}
		if sm.Item("SVG").Chosen() {
			_ = b.copyAsSVG(sel, false)
		}
	})
	m.Submenu(l.BoardCtxExportAs(), func(sm *ui.Menu) {
		if sm.Item(l.BoardExportPNG()).Chosen() {
			b.exportPNGFile(sel)
		}
		if sm.Item(l.BoardExportSVG()).Chosen() {
			b.exportSVGFile(sel)
		}
	})
	m.Separator()

	if m.Item(l.BoardZoomFitSel()).Shortcut(ui.Shift, ui.Key2).Chosen() {
		b.ed.ZoomToSelection()
		b.invalidate()
	}
	if m.Item(l.BoardCtxSelectAll()).Shortcut(ui.Cmd, ui.KeyA).Chosen() {
		b.ed.SelectAll()
		b.invalidate()
	}
}
