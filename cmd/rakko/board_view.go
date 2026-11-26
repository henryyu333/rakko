package main

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/transfer"
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
	c.SetMenuPresentation(ui.PopoverMenus)
	if c.Shortcut(ui.Cmd, ui.KeyPeriod) || c.Shortcut(ui.Ctrl, ui.KeyPeriod) {
		b.toggleFocusMode()
	}
	menuNow := b.anyMenuOpen()
	if b.wasMenuOpen && !menuNow {
		b.canvasFocus = false
	}
	b.wasMenuOpen = menuNow
	l := loc(c)
	pal := b.ed.Palette()
	c.Root().Background(boardColor(pal.Background))
	barH := float32(0)
	if !b.focusMode {
		barH = max(boardBarH, c.TitleBar().Height)
	}
	if winW, winH := c.Size(); winW > 0 && winH > 0 {
		b.stageRect = ui.Rect{X: 0, Y: barH, W: winW, H: max(0, winH-barH)}
	}
	ui.Column(c.Key("board-root")).Fill().Children(func() {
		if !b.focusMode {
			a.boardBar(c, l, pal)
		}
		a.boardStage(c, l, pal)
	})
	if b.confirmDelete {
		a.boardDeleteModal(c, l, pal)
	}
	if b.confirmPageDelete {
		a.boardPageDeleteModal(c, l, pal)
	}
}

func (a *shelf) boardBar(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	bar := c.TitleBar()
	ui.Row(c.Key("board-bar")).Height(max(boardBarH, bar.Height)).Shrink(0).FillWidth().
		Background(boardColor(pal.TopBar)).
		AlignItems(ui.Center).Padding(0, bar.Right+8, 0, bar.Left+10).Gap(4).DragWindow().Children(func() {
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
		a.boardPageSwitcherButton(c, l, pal)
		ui.Box(c).Grow(1).FillHeight().DragWindow()
		dragBtn := a.boardBarButton(c, pal, "board-drag-png", boardExportSymbol, l.BoardDragPNG(), false, func() {
			b.exportPNGFile(b.ed.Selection())
		})
		if b.ed != nil && b.ed.ItemCount() > 0 {
			dragBtn.DragDataFrom(func() transfer.Data {
				_, td, err := b.writeDragPNGTemp(b.ed.Selection(), "")
				if err != nil {
					return transfer.Data{}
				}
				return td
			}, transfer.DragOptions{Operations: transfer.Copy})
		}
		a.boardBarButton(c, pal, "board-reveal", revealSymbol, osText(l.BoardReveal(), l.BoardRevealWin()), false, func() {
			if b.win != nil {
				b.frame = b.win.Bounds()
			}
			boardReveal(b.store.Path())
		})
		a.boardBarButton(c, pal, "board-top", pinSymbol, l.BoardKeepOnTop(), b.top, func() {
			b.setTop(!b.top)
		})
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

func (a *shelf) boardBarButton(c *ui.Context, pal *editor.Palette, key string, icon *ui.SVG, label string, on bool, act func()) ui.Element {
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
	return btn
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
	if b.ed.InGesture() && b.ed.Tool == editor.ToolDraw {
		b.ed.CheckDwell(b.seconds(c.Now()))
		c.AnimationFrame()
	}
	if len(b.ed.Trails) > 0 {
		if b.ed.AgeTrails(b.seconds(c.Now())) {
			c.AnimationFrame()
		}
	}
	stage := ui.Box(c.Key("board-stage")).FillWidth().Grow(1).MinHeight(0).Clip()
	stage.Children(func() {
		canvas := ui.Box(c.Key("board-canvas")).Fill().Focusable().FocusRing(false).Label(b.currentTitle(l))
		if !b.canvasFocus && !b.renaming && !b.renamingPage && !b.confirmDelete && !b.confirmPageDelete && b.ed.Editing == "" && !b.anyMenuOpen() {
			canvas.Focus()
			b.canvasFocus = true
		}
		if files := canvas.DroppedFiles(); len(files) > 0 && !b.locked {
			at := b.ed.ToPage(geo.V(b.ed.VW/2, b.ed.VH/2))
			for i, path := range files {
				offset := float64(i * 24)
				b.insertImageFile(path, geo.V(at.X+offset, at.Y+offset))
			}
		}
		switch b.ed.CursorNow() {
		case editor.CursorGrabbing:
			canvas.Cursor(ui.CursorGrabbing)
		case editor.CursorGrab:
			canvas.Cursor(ui.CursorGrab)
		case editor.CursorCross:
			canvas.Cursor(ui.CursorCrosshair)
		case editor.CursorText:
			canvas.Cursor(ui.CursorText)
		case editor.CursorMove, editor.CursorRotate:
			canvas.Cursor(ui.CursorMove)
		case editor.CursorPointer:
			canvas.Cursor(ui.CursorPointer)
		case editor.CursorEW:
			canvas.Cursor(ui.CursorResizeEW)
		case editor.CursorNS:
			canvas.Cursor(ui.CursorResizeNS)
		case editor.CursorNESW:
			canvas.Cursor(ui.CursorResizeNESW)
		case editor.CursorNWSE:
			canvas.Cursor(ui.CursorResizeNWSE)
		default:
			canvas.Cursor(ui.CursorDefault)
		}
		canvas.Draw(func(p *ui.Painter, r ui.Rect) {
			b.stageRect = r
			b.paintStage(p, r)
		})
		canvas.HandleInput(func(ev ui.InputEvent) bool {
			return b.handleCanvasInput(c, ev)
		})
		canvas.ContextMenu(func(m *ui.Menu) {
			b.buildContextMenu(c, l, m)
		})

		a.boardTextEditor(c, pal)

		if b.ed.ItemCount() == 0 && !b.focusMode {
			ui.Column(c.Key("board-hint")).Absolute().Top(0).Left(0).Fill().Center().IgnoreInput(true).Children(func() {
				ui.Text(c, osText(l.BoardHint(), l.BoardHintWin())).FontSize(13).TextColor(boardColor(pal.Ink3))
			})
		}

		if b.focusMode {
			a.boardFocusExitPill(c, l, pal)
		} else {
			a.boardNavBar(c, l, pal)
			a.boardMinimap(c, l, pal)
			a.boardBackPill(c, l, pal)
			a.boardSubBar(c, l, pal)
			a.boardToolbar(c, l, pal)
			a.boardPropPanel(c, l, pal)
			a.boardRadialPalette(c, l, pal)
		}
	})
}

func (a *shelf) boardTextEditor(c *ui.Context, pal *editor.Palette) {
	b := a.board
	if b.ed.Editing == "" {
		b.lastEditing = ""
		return
	}
	justOpened := false
	if b.lastEditing != b.ed.Editing {
		b.lastEditing = b.ed.Editing
		b.editKey++
		justOpened = true
	}
	box, size, font, align, bold, italic, underline, col, ok := b.ed.EditBox()
	if !ok || size <= 0 {
		return
	}
	cam := b.ed.Cam()
	m := b.ed.PageMat(b.ed.Editing)
	sh := b.ed.Doc.Shapes[b.ed.Editing]
	if sh != nil && sh.Type == doc.TypeText && sh.AutoSize {
		switch align {
		case "center", "middle":
			box.X -= size / 2
		case "right", "end":
			box.X -= size
		}
		box.W += size
	}
	box.H += 4 / cam.Z
	center := b.ed.ToScreen(m.Apply(box.Center()))
	sw, shPx := max(box.W*cam.Z, 40), max(box.H*cam.Z, size*cam.Z*1.4)

	input := ui.TextAreaBase(c.Key(fmt.Sprintf("board-edit-%d", b.editKey)), &b.ed.EditText).
		Absolute().
		Left(float32(center.X-sw/2)).Top(float32(center.Y-shPx/2)).
		Size(float32(sw), float32(shPx)).
		Font(boardFontFamily(font)).
		FontSize(float32(size*cam.Z)).
		LineHeight(1.3).
		TextColor(boardColor(col)).
		Background(ui.Transparent).
		Padding(0).
		Border(1, boardColor(pal.Accent.Alpha(0.55))).Radius(3).
		FocusRing(false).
		Label("Text")
	if bold {
		input.FontWeight(700)
	}
	if italic {
		input.Italic()
	}
	_ = underline
	switch align {
	case "center", "middle":
		input.TextAlign(ui.Center)
	case "right", "end":
		input.TextAlign(ui.End)
	default:
		input.TextAlign(ui.Start)
	}
	if deg := m.Rotation() * 180 / math.Pi; math.Abs(deg) > 0.01 {
		input.Rotate(float32(deg))
	}
	if justOpened {
		n := utf8.RuneCountInString(b.ed.EditText)
		input.Focus().SetTextSelection(n, n)
	}
	input.OnChange(func() {
		b.ed.EditChanged()
		b.invalidate()
	})
	input.OnTextKey(func(k ui.TextKey) bool {
		isFrame := sh != nil && sh.Type == doc.TypeFrame
		if k.Key == ui.KeyEscape || (k.Key == ui.KeyEnter && (isFrame || k.Mods&(ui.Cmd|ui.Ctrl) != 0)) {
			b.ed.EditText = k.Text()
			b.ed.EditChanged()
			b.ed.StopEditing()
			b.canvasFocus = false
			b.invalidate()
			return true
		}
		return false
	})
}

type boardChromeLayout struct {
	compact  bool
	stacked  bool
	alignEnd bool
	fullTern bool

	barH    float32
	barPad  float32
	barGap  float32
	barRad  float32
	toolBtn float32
	toolIco float32

	navLeft float32
	navH    float32
	navPad  float32
	navGap  float32
	navRad  float32
	navBtn  float32
	sepH    float32
	sepMar  float32
	zoomH   float32
	zoomMin float32
	zoomFs  float32
}

// boardChromeLayout picks the Tern spec dimensions (48/34 toolbar, 40/28 nav)
// only when the centered full toolbar clears the nav without overlapping,
// preserving the centered compact 1040 layout and falling back further below.
func (b *boardUI) boardChromeLayout() boardChromeLayout {
	w := b.stageRect.W
	full := boardChromeLayout{
		fullTern: true,
		barH:     48, barPad: 8, barGap: 2, barRad: 14, toolBtn: 34, toolIco: 16,
		navLeft: 16, navH: 40, navPad: 6, navGap: 2, navRad: 12, navBtn: 28,
		sepH: 20, sepMar: 3, zoomH: 28, zoomMin: 46, zoomFs: 11.5,
	}
	slim := boardChromeLayout{
		barH: 44, barPad: 5, barGap: 1, barRad: 13, toolBtn: 30, toolIco: 17,
		navLeft: 12, navH: 38, navPad: 5, navGap: 1, navRad: 11, navBtn: 25,
		sepH: 18, sepMar: 2, zoomH: 26, zoomMin: 38, zoomFs: 11,
	}
	switch {
	case w <= 0 || w >= 1220:
		return full
	case w >= 1030:
		return slim
	case w >= 800:
		slim.alignEnd = true
		return slim
	case w >= 640:
		slim.compact = true
		slim.alignEnd = true
		slim.toolBtn = 26
		slim.toolIco = 15
		return slim
	default:
		slim.compact = true
		slim.stacked = true
		slim.toolBtn = 26
		slim.toolIco = 15
		return slim
	}
}

func (b *boardUI) boardToolbarHeight() float32 {
	return b.boardChromeLayout().barH
}

// boardChrome decides how the bottom chrome shares the stage width: the
// toolbar is compact when the full one and the nav cannot sit side by side,
// stacked above the nav when even the compact one cannot, and centred only
// when a full toolbar has room on both sides of the nav.
func (b *boardUI) boardChrome() (compact, stacked, alignEnd bool) {
	m := b.boardChromeLayout()
	return m.compact, m.stacked, m.alignEnd
}

// boardToolbarBottom is the toolbar's bottom inset, raised when it is stacked.
func (b *boardUI) boardToolbarBottom() float32 {
	m := b.boardChromeLayout()
	if m.stacked {
		// Leave >= 8px clear above the 38px nav capsule anchored at bottom=16 (top=54).
		return 16 + m.navH + 10
	}
	return 16
}

func (a *shelf) boardNavBar(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	m := b.boardChromeLayout()
	pct := int(math.Round(b.ed.Cam().Z * 100))
	nav := ui.Row(c.Key("board-nav")).Absolute().Left(m.navLeft).Bottom(16).Height(m.navH).
		Padding(0, m.navPad, 0, m.navPad).Gap(m.navGap).Radius(m.navRad).Clip().AlignItems(ui.Center).
		Background(boardColor(pal.Bar)).Border(0.5, boardColor(pal.Edge)).
		Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.28))
	nav.Children(func() {
		undoBtn := a.boardNavIconBtn(c, pal, "board-undo", shotUndoSymbol, l.BoardUndo(), false)
		if !b.ed.CanUndo() {
			undoBtn.Disabled(true).Opacity(0.35)
		}
		if undoBtn.Clicked() && b.ed.CanUndo() {
			b.ed.Undo()
			b.invalidate()
		}

		redoBtn := a.boardNavIconBtn(c, pal, "board-redo", shotRedoSymbol, l.BoardRedo(), false)
		if !b.ed.CanRedo() {
			redoBtn.Disabled(true).Opacity(0.35)
		}
		if redoBtn.Clicked() && b.ed.CanRedo() {
			b.ed.Redo()
			b.invalidate()
		}

		ui.Box(c).Width(1).Height(m.sepH).Margin(0, m.sepMar, 0, m.sepMar).Background(boardColor(pal.Sep))

		zout := a.boardNavIconBtn(c, pal, "board-zout", minusSymbol, l.BoardZoomOut(), false)
		if zout.Clicked() {
			b.ed.ZoomOut()
			b.invalidate()
		}

		zbtn := ui.Row(c.Key("board-zoom-pct")).Height(m.zoomH).MinWidth(m.zoomMin).Padding(0, 4, 0, 4).Radius(m.navRad-4).Center().
			Label(l.BoardZoomLabel()).Tooltip(l.BoardZoomLabel())
		if zbtn.Hovered() || b.zoomMenu {
			zbtn.Background(boardColor(pal.BarHover))
		}
		zbtn.Children(func() {
			ui.Text(c, fmt.Sprintf("%d%%", pct)).SingleLine().FontSize(m.zoomFs).FontWeight(500).TextColor(boardColor(pal.Ink2))
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
			if m.Item(l.BoardZoomFit()).Shortcut(ui.Shift, ui.Key1).Chosen() {
				b.ed.ZoomToFit()
				b.invalidate()
			}
			if m.Item(l.BoardZoomFitSel()).Shortcut(ui.Shift, ui.Key2).Disabled(len(b.ed.Selection()) == 0).Chosen() {
				b.ed.ZoomToSelection()
				b.invalidate()
			}
			m.Separator()
			miniLabel := l.BoardMinimapShow()
			if b.minimap {
				miniLabel = l.BoardMinimapHide()
			}
			if m.Item(miniLabel).Checked(b.minimap).Chosen() {
				b.minimap = !b.minimap
				b.invalidate()
			}
		}, b.popoverButtonOpts(pal, ui.AnchorTopStart, ui.AnchorBottomStart, &b.zoomMenu))

		zin := a.boardNavIconBtn(c, pal, "board-zin", plusSymbol, l.BoardZoomIn(), false)
		if zin.Clicked() {
			b.ed.ZoomIn()
			b.invalidate()
		}

		fitBtn := a.boardNavIconBtn(c, pal, "board-fit", boardFitSymbol, l.BoardZoomFit(), false)
		if fitBtn.Clicked() {
			b.ed.ZoomToFit()
			b.invalidate()
		}

		miniBtn := a.boardNavIconBtn(c, pal, "board-mini", boardMinimapSymbol, l.BoardMinimapLabel(), b.minimap)
		if miniBtn.Clicked() {
			b.minimap = !b.minimap
			b.invalidate()
		}

		ui.Box(c).Width(1).Height(m.sepH).Margin(0, m.sepMar, 0, m.sepMar).Background(boardColor(pal.Sep))

		more := a.boardNavIconBtn(c, pal, "board-more", boardMoreSymbol, l.BoardMore(), b.moreMenu)
		more.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(l.BoardToolImage()).Shortcut(ui.Cmd, ui.KeyU).Disabled(b.locked).Chosen() {
				b.pickImage()
			}
			m.Separator()
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
			curLook := b.ed.Look()
			looks := []struct {
				id    string
				label string
			}{
				{doc.LookSketch, l.BoardLookSketch()},
				{doc.LookClean, l.BoardLookClean()},
				{doc.LookMono, l.BoardLookMono()},
			}
			for _, lk := range looks {
				lk := lk
				if m.Item(l.BoardLookSection() + " · " + lk.label).Checked(curLook == lk.id).Chosen() {
					b.ed.SetLook(lk.id)
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
			m.Separator()
			if m.Item(l.BoardFocusMode()).Shortcut(ui.Cmd, ui.KeyPeriod).Checked(b.focusMode).Chosen() {
				b.toggleFocusMode()
			}
			if m.Item(l.BoardKeepTool()).Shortcut(0, ui.KeyQ).Checked(b.ed.ToolLocked).Chosen() {
				b.ed.ToggleToolLock()
				b.invalidate()
			}
			m.Separator()
			if m.Item(l.BoardSnapAlways()).Checked(b.ed.Snap).Chosen() {
				b.ed.ToggleSnap()
				b.invalidate()
			}
			if m.Item(l.BoardSnapGrid()).Checked(b.ed.Grid).Shortcut(ui.Cmd, ui.KeyQuote).Chosen() {
				b.ed.ToggleGrid()
				b.invalidate()
			}
			m.Separator()
			if m.Item(l.BoardRadialPalette()).Shortcut(0, ui.KeyC).Checked(b.radialOn).Chosen() {
				b.openRadialPalette(b.hoverScreenPt)
			}
			m.Separator()
			if m.Item(l.BoardExportPNG()).Chosen() {
				b.exportPNGFile(nil)
			}
			if m.Item(l.BoardExportSVG()).Chosen() {
				b.exportSVGFile(nil)
			}
			if m.Item(l.BoardExportBoard()).Chosen() {
				b.exportBoardFile()
			}
			if m.Item(l.BoardImportBoard()).Disabled(b.locked).Chosen() {
				b.importBoardFile()
			}
		}, b.popoverButtonOpts(pal, ui.AnchorTopStart, ui.AnchorBottomStart, &b.moreMenu))
	})
}

func (a *shelf) boardNavIconBtn(c *ui.Context, pal *editor.Palette, key string, icon *ui.SVG, label string, active bool) ui.Element {
	sz := float32(25)
	rad := float32(7)
	if b := a.board; b != nil {
		m := b.boardChromeLayout()
		sz = m.navBtn
		rad = m.navRad - 4
	}
	btn := ui.Row(c.Key(key)).Size(sz, sz).Radius(rad).Center().Label(label).Tooltip(label)
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
	a.boardConfirmModal(c, l, pal, "board-del",
		osText(l.BoardDeleteConfirm(name), l.BoardDeleteConfirmWin(name)),
		osText(l.BoardMoveToTrash(), l.BoardMoveToTrashWin()),
		func() {
			if err := b.trashCurrent(); err != nil {
				a.report(err)
			}
		},
		func() {
			b.confirmDelete = false
			b.invalidate()
		})
}

func (a *shelf) boardPageDeleteModal(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	name := ""
	if pg := b.ed.ActivePageInfo(); pg != nil {
		name = b.pageTitle(l, pg)
	}
	a.boardConfirmModal(c, l, pal, "board-page-del",
		l.BoardPageDeleteConfirm(name), l.BoardDeleteAction(),
		func() {
			if err := b.ed.DeletePage(b.ed.Page); err != nil {
				a.report(err)
			}
			b.confirmPageDelete = false
			b.invalidate()
		},
		func() {
			b.confirmPageDelete = false
			b.invalidate()
		})
}

// boardConfirmModal renders the shared destructive-action dialog used for
// trashing a board and deleting a page.
func (a *shelf) boardConfirmModal(c *ui.Context, l locales.Messages, pal *editor.Palette, key, body, okLabel string, ok, cancel func()) {
	backdrop := ui.Box(c.Key(key + "-modal")).Modal().Absolute().Top(0).Left(0).Fill().
		Background(ui.RGBA(0, 0, 0, 0.45)).Center()
	backdrop.Children(func() {
		if c.Shortcut(0, ui.KeyEscape) && cancel != nil {
			cancel()
		}
		card := ui.Column(c.Key(key+"-card")).Width(320).Padding(16, 16, 14, 16).Gap(14).
			Radius(12).Clip().Background(boardColor(pal.Bar)).Border(0.5, boardColor(pal.Edge)).
			Shadow(0, 16, 40, 0, ui.RGBA(0, 0, 0, 0.45))
		card.Children(func() {
			ui.Text(c, body).FontSize(13).LineHeight(1.45).TextColor(boardColor(pal.Ink))
			ui.Row(c).FillWidth().Justify(ui.End).Gap(8).Children(func() {
				btn := ui.Row(c.Key(key+"-cancel")).Height(28).Padding(0, 12, 0, 12).Radius(7).Center().
					Background(boardColor(pal.BarActive)).Label(l.BoardCancel())
				btn.Children(func() {
					ui.Text(c, l.BoardCancel()).SingleLine().FontSize(12).FontWeight(500).TextColor(boardColor(pal.Ink))
				})
				if btn.Clicked() && cancel != nil {
					cancel()
				}

				del := ui.Row(c.Key(key+"-ok")).Height(28).Padding(0, 12, 0, 12).Radius(7).Center().
					Background(danger).Label(okLabel)
				del.Children(func() {
					ui.Text(c, okLabel).SingleLine().FontSize(12).FontWeight(600).TextColor(ui.Hex("#FFFFFF"))
				})
				if del.Clicked() && ok != nil {
					ok()
				}
			})
		})
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
	step = math.Round(step)
	ox := math.Round(math.Mod(cam.X*cam.Z, step))
	if ox < 0 {
		ox += step
	}
	if ox >= step {
		ox -= step
	}
	oy := math.Round(math.Mod(cam.Y*cam.Z, step))
	if oy < 0 {
		oy += step
	}
	if oy >= step {
		oy -= step
	}
	startX := math.Round(float64(r.X)) + ox
	startY := math.Round(float64(r.Y)) + oy
	endX := float64(r.X + r.W)
	endY := float64(r.Y + r.H)

	switch paper {
	case doc.PaperDots:
		dotCol := boardColor(pal.PaperDot)
		const dotSize = float32(2)
		for iy := 0; ; iy++ {
			y := startY + float64(iy)*step
			if y >= endY {
				break
			}
			for ix := 0; ; ix++ {
				x := startX + float64(ix)*step
				if x >= endX {
					break
				}
				p.Fill(ui.Rect{X: float32(x) - dotSize/2, Y: float32(y) - dotSize/2, W: dotSize, H: dotSize}, dotCol, dotSize/2)
			}
		}
	case doc.PaperGrid:
		lineCol := boardColor(pal.PaperLine)
		for ix := 0; ; ix++ {
			x := startX + float64(ix)*step
			if x >= endX {
				break
			}
			sx := float32(x) + 0.5
			p.Line(sx, r.Y, sx, r.Y+r.H, 1, lineCol)
		}
		for iy := 0; ; iy++ {
			y := startY + float64(iy)*step
			if y >= endY {
				break
			}
			sy := float32(y) + 0.5
			p.Line(r.X, sy, r.X+r.W, sy, 1, lineCol)
		}
	case doc.PaperLines:
		lineCol := boardColor(pal.PaperLine)
		for iy := 0; ; iy++ {
			y := startY + float64(iy)*step
			if y >= endY {
				break
			}
			sy := float32(y) + 0.5
			p.Line(r.X, sy, r.X+r.W, sy, 1, lineCol)
		}
	}
}

func (a *shelf) boardSubBar(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	switch b.ed.Tool {
	case editor.ToolDraw, editor.ToolEraser:
	default:
		return
	}
	wrap := ui.Row(c.Key("board-subbar-wrap")).Absolute().Left(0).Right(0).Bottom(b.boardToolbarBottom() + b.boardToolbarHeight() + 8).Justify(ui.Center).PassThrough()
	wrap.Children(func() {
		sub := ui.Row(c.Key("board-subbar")).Height(34).Padding(0, 6, 0, 6).Gap(2).Radius(10).Clip().AlignItems(ui.Center).
			Background(boardColor(pal.Bar)).Border(0.5, boardColor(pal.Edge)).
			Shadow(0, 6, 18, 0, ui.RGBA(0, 0, 0, 0.24))
		sub.Children(func() {
			if b.ed.Tool == editor.ToolDraw {
				pens := []struct {
					id    string
					label string
				}{
					{"normal", l.BoardPenNormal()},
					{"pen", l.BoardPenFountain()},
					{"marker", l.BoardPenMarker()},
				}
				for _, pk := range pens {
					btn := a.boardPillTab(c, pal, "board-pen-"+pk.id, pk.label, b.ed.Next.PenKind == pk.id)
					if btn.Clicked() {
						b.ed.SetPenKind(pk.id)
						b.invalidate()
					}
				}
				return
			}
			modes := []struct {
				mode  editor.EraserMode
				label string
			}{
				{editor.EraserObject, l.BoardEraserObject()},
				{editor.EraserPartial, l.BoardEraserPartial()},
			}
			for _, em := range modes {
				btn := a.boardPillTab(c, pal, "board-eraser-"+string(em.mode), em.label, b.ed.Next.EraserMode == em.mode)
				if btn.Clicked() {
					b.ed.SetEraserMode(em.mode)
					b.invalidate()
				}
			}
		})
	})
}

func (a *shelf) boardPillTab(c *ui.Context, pal *editor.Palette, key, label string, active bool) ui.Element {
	btn := ui.Row(c.Key(key)).Height(24).Padding(0, 9, 0, 9).Radius(7).Center().Label(label)
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
		ui.Text(c, label).SingleLine().FontSize(11.5).FontWeight(500).TextColor(boardColor(col))
	})
	return btn
}

func (b *boardUI) chooseTool(t editor.Tool) {
	b.ed.SetTool(t)
	b.canvasFocus = false
	b.invalidate()
}

func (a *shelf) boardToolbar(c *ui.Context, l locales.Messages, pal *editor.Palette) {
	b := a.board
	m := b.boardChromeLayout()
	compact, stacked, alignEnd := m.compact, m.stacked, m.alignEnd
	wrap := ui.Row(c.Key("board-tools-wrap")).Absolute().Left(0).Right(0).Bottom(b.boardToolbarBottom()).PassThrough()
	switch {
	case stacked:
		wrap.Justify(ui.Center)
	case alignEnd:
		wrap.Justify(ui.End).Padding(0, m.navLeft, 0, 0)
	default:
		wrap.Justify(ui.Center)
	}
	wrap.Children(func() {
		bar := ui.Row(c.Key("board-tools")).Height(m.barH).Padding(0, m.barPad, 0, m.barPad).Gap(m.barGap).Radius(m.barRad).Clip().AlignItems(ui.Center).
			Background(boardColor(pal.Bar)).Border(0.5, boardColor(pal.Edge)).
			Shadow(0, 8, 24, 0, ui.RGBA(0, 0, 0, 0.28))
		bar.Children(func() {
			if a.boardToolBtn(c, pal, "tool-select", boardSelectSymbol, l.BoardToolSelect(), b.ed.Tool == editor.ToolSelect).Clicked() {
				b.chooseTool(editor.ToolSelect)
			}
			if !compact && a.boardToolBtn(c, pal, "tool-lasso", boardLassoSymbol, l.BoardToolLasso(), b.ed.Tool == editor.ToolLasso).Clicked() {
				b.chooseTool(editor.ToolLasso)
			}
			if a.boardToolBtn(c, pal, "tool-hand", boardHandSymbol, l.BoardToolHand(), b.ed.Tool == editor.ToolHand).Clicked() {
				b.chooseTool(editor.ToolHand)
			}

			ui.Box(c).Width(1).Height(22).Margin(0, 3, 0, 3).Background(boardColor(pal.Sep))

			if a.boardToolBtn(c, pal, "tool-draw", pencilSymbol, l.BoardToolDraw(), b.ed.Tool == editor.ToolDraw).Clicked() {
				b.chooseTool(editor.ToolDraw)
			}
			if a.boardToolBtn(c, pal, "tool-hilite", boardHiliteSymbol, l.BoardToolHighlight(), b.ed.Tool == editor.ToolHighlight).Clicked() {
				b.chooseTool(editor.ToolHighlight)
			}
			if a.boardToolBtn(c, pal, "tool-eraser", boardEraserSymbol, l.BoardToolEraser(), b.ed.Tool == editor.ToolEraser).Clicked() {
				b.chooseTool(editor.ToolEraser)
			}
			if a.boardToolBtn(c, pal, "tool-laser", boardLaserSymbol, l.BoardToolLaser(), b.ed.Tool == editor.ToolLaser).Clicked() {
				b.chooseTool(editor.ToolLaser)
			}

			ui.Box(c).Width(1).Height(22).Margin(0, 3, 0, 3).Background(boardColor(pal.Sep))

			shapeBtn := a.boardToolBtn(c, pal, "tool-shape", shotRectSymbol, l.BoardToolShape(), b.ed.Tool == editor.ToolGeo || b.shapeMenu)
			if shapeBtn.Clicked() {
				b.shapeMenu = !b.shapeMenu
				b.invalidate()
			}
			ui.PopoverBase(c, shapeBtn, &b.shapeMenu, func(panel ui.Element) {
				panel.AttachTo(shapeBtn, ui.AnchorTop, ui.AnchorBottom).Margin(0, 0, 8, 0).
					Padding(10).Radius(12).Clip().Background(boardColor(pal.Bar)).
					Border(0.5, boardColor(pal.Edge)).
					Shadow(0, 14, 36, 0, ui.RGBA(0, 0, 0, 0.38))
				panel.Children(func() {
					const cols = 4
					ui.Column(c.Key("shape-grid")).Gap(4).Children(func() {
						for row := 0; row < len(editor.GeoKinds); row += cols {
							end := min(row+cols, len(editor.GeoKinds))
							ui.Row(c).Gap(4).Children(func() {
								for _, kind := range editor.GeoKinds[row:end] {
									kind := kind
									label := boardShapeName(l, kind)
									active := b.ed.Tool == editor.ToolGeo && b.ed.Next.Geo == kind
									cell := ui.Row(c.Key("shape-"+kind)).Width(112).Height(28).
										Padding(0, 8, 0, 8).Radius(7).AlignItems(ui.Center).
										Label(label).Tooltip(label)
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
										ui.Text(c, label).SingleLine().FontSize(11.5).FontWeight(500).TextColor(boardColor(col))
									})
									if cell.Clicked() {
										b.ed.SetGeoTool(kind)
										b.shapeMenu = false
										b.canvasFocus = false
										b.invalidate()
									}
								}
							})
						}
					})
				})
			})

			if a.boardToolBtn(c, pal, "tool-arrow", boardArrowSymbol, l.BoardToolArrow(), b.ed.Tool == editor.ToolArrow).Clicked() {
				b.chooseTool(editor.ToolArrow)
			}
			if !compact && a.boardToolBtn(c, pal, "tool-line", boardLineSymbol, l.BoardToolLine(), b.ed.Tool == editor.ToolLine).Clicked() {
				b.chooseTool(editor.ToolLine)
			}
			if !compact && a.boardToolBtn(c, pal, "tool-frame", boardFrameSymbol, l.BoardToolFrame(), b.ed.Tool == editor.ToolFrame).Clicked() {
				b.chooseTool(editor.ToolFrame)
			}
			if a.boardToolBtn(c, pal, "tool-text", textSymbol, l.BoardToolText(), b.ed.Tool == editor.ToolText).Clicked() {
				b.chooseTool(editor.ToolText)
			}
			if !compact && a.boardToolBtn(c, pal, "tool-note", noteSymbol, l.BoardToolNote(), b.ed.Tool == editor.ToolNote).Clicked() {
				b.chooseTool(editor.ToolNote)
			}
			if a.boardToolBtn(c, pal, "tool-image", imgSymbol, l.BoardToolImage(), false).Clicked() {
				b.pickImage()
			}
			if compact {
				active := b.ed.ToolLocked || b.ed.Tool == editor.ToolLasso || b.ed.Tool == editor.ToolLine ||
					b.ed.Tool == editor.ToolFrame || b.ed.Tool == editor.ToolNote
				tools := a.boardToolBtn(c, pal, "tool-more", boardMoreSymbol, l.BoardMoreTools(), active || b.toolMenu)
				if tools.Clicked() {
					b.toolMenu = !b.toolMenu
					b.invalidate()
				}
				ui.PopoverBase(c, tools, &b.toolMenu, func(panel ui.Element) {
					panel.AttachTo(tools, ui.AnchorBottomEnd, ui.AnchorTopEnd).Margin(0, 0, 8, 0).
						Padding(6).Radius(12).Clip().Background(boardColor(pal.Bar)).
						Border(0.5, boardColor(pal.Edge)).
						Shadow(0, 14, 36, 0, ui.RGBA(0, 0, 0, 0.38))
					panel.Children(func() {
						rows := []struct {
							id       string
							label    string
							icon     *ui.SVG
							shortcut string
							on       bool
							act      func()
						}{
							{"lasso", l.BoardToolLasso(), boardLassoSymbol, "S", b.ed.Tool == editor.ToolLasso,
								func() { b.chooseTool(editor.ToolLasso) }},
							{"line", l.BoardToolLine(), boardLineSymbol, "L", b.ed.Tool == editor.ToolLine,
								func() { b.chooseTool(editor.ToolLine) }},
							{"frame", l.BoardToolFrame(), boardFrameSymbol, "F", b.ed.Tool == editor.ToolFrame,
								func() { b.chooseTool(editor.ToolFrame) }},
							{"note", l.BoardToolNote(), noteSymbol, "N", b.ed.Tool == editor.ToolNote,
								func() { b.chooseTool(editor.ToolNote) }},
							{"lock", l.BoardKeepTool(), boardToolLockSymbol, "Q", b.ed.ToolLocked,
								func() { b.ed.ToggleToolLock(); b.invalidate() }},
						}
						for _, row := range rows {
							row := row
							item := ui.Row(c.Key("tool-more-"+row.id)).Width(170).Height(28).Radius(7).
								Padding(0, 8, 0, 8).Gap(8).AlignItems(ui.Center).Label(row.label).Tooltip(row.label)
							switch {
							case row.on:
								item.Background(boardColor(pal.AccentFill))
							case item.Hovered():
								item.Background(boardColor(pal.BarHover))
							}
							item.Children(func() {
								col := pal.Ink2
								if row.on {
									col = pal.Accent
								}
								ui.Icon(c, row.icon).Size(14, 14).TextColor(boardColor(col))
								ui.Text(c, row.label).SingleLine().FontSize(11.5).FontWeight(500).TextColor(boardColor(col)).Grow(1)
								ui.Text(c, row.shortcut).SingleLine().FontSize(10.5).TextColor(boardColor(pal.Ink3))
							})
							if item.Clicked() {
								row.act()
								b.toolMenu = false
								b.canvasFocus = false
								b.invalidate()
							}
						}
					})
				})
			} else {
				b.toolMenu = false
			}
			if !compact {
				ui.Box(c).Width(1).Height(22).Margin(0, 3, 0, 3).Background(boardColor(pal.Sep))
				if a.boardToolBtn(c, pal, "tool-lock", boardToolLockSymbol, l.BoardKeepTool(), b.ed.ToolLocked).Clicked() {
					b.ed.ToggleToolLock()
					b.invalidate()
				}
			}
		})
	})
}

func (a *shelf) boardToolBtn(c *ui.Context, pal *editor.Palette, key string, icon *ui.SVG, label string, active bool) ui.Element {
	sz := float32(30)
	isz := float32(17)
	rad := float32(7)
	if b := a.board; b != nil {
		m := b.boardChromeLayout()
		sz = m.toolBtn
		isz = m.toolIco
		if m.fullTern {
			rad = 10
		}
	}
	btn := ui.Row(c.Key(key)).Size(sz, sz).Radius(rad).Center().Label(label).Tooltip(label)
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
		ui.Icon(c, icon).Size(isz, isz).TextColor(boardColor(col))
	})
	return btn
}

func boardShapeName(l locales.Messages, kind string) string {
	switch kind {
	case "rectangle":
		return l.BoardShapeRectangle()
	case "rounded-rect":
		return l.BoardShapeRoundedRect()
	case "ellipse":
		return l.BoardShapeEllipse()
	case "diamond":
		return l.BoardShapeDiamond()
	case "triangle":
		return l.BoardShapeTriangle()
	case "pentagon":
		return l.BoardShapePentagon()
	case "hexagon":
		return l.BoardShapeHexagon()
	case "octagon":
		return l.BoardShapeOctagon()
	case "parallelogram":
		return l.BoardShapeParallelogram()
	case "parallelogram-2":
		return l.BoardShapeParallelogram2()
	case "trapezoid":
		return l.BoardShapeTrapezoid()
	case "star":
		return l.BoardShapeStar()
	case "cylinder":
		return l.BoardShapeCylinder()
	case "pill":
		return l.BoardShapePill()
	case "bracket":
		return l.BoardShapeBracket()
	case "brace":
		return l.BoardShapeBrace()
	case "arrow-right":
		return l.BoardShapeArrowRight()
	case "arrow-left":
		return l.BoardShapeArrowLeft()
	case "arrow-up":
		return l.BoardShapeArrowUp()
	case "arrow-down":
		return l.BoardShapeArrowDown()
	case "x-box":
		return l.BoardShapeXBox()
	case "check-box":
		return l.BoardShapeCheckBox()
	case "cloud":
		return l.BoardShapeCloud()
	case "heart":
		return l.BoardShapeHeart()
	case "callout":
		return l.BoardShapeCallout()
	case "polygon":
		return l.BoardShapePolygon()
	case "curve":
		return l.BoardShapeCurve()
	}
	return kind
}

func boardMods(m ui.Modifiers) editor.Mods {
	return editor.Mods{
		Shift: m&ui.Shift != 0,
		Alt:   m&ui.Alt != 0,
		Cmd:   m&ui.Cmd != 0,
		Ctrl:  m&ui.Ctrl != 0,
	}
}

// boardPasteTextMax caps a pasted text shape: a whole book would make every
// layout pass measure megabytes and freeze the canvas.
const boardPasteTextMax = 20_000

func (b *boardUI) canPaste(c *ui.Context) bool {
	if b.locked || b.ed == nil {
		return false
	}
	if b.ed.HasClipboard() {
		return true
	}
	return c != nil && len(c.Services().ClipboardFormats()) > 0
}

func (b *boardUI) pasteFromClipboard(c *ui.Context, pagePt *geo.Vec) bool {
	if b.locked || b.ed == nil {
		return false
	}
	if c != nil && b.internalClipboardCurrent(c) && b.ed.Paste() {
		b.invalidate()
		return true
	}
	if c != nil {
		formats := c.Services().ClipboardFormats()
		if boardClipboardHasText(formats) {
			return b.pasteTextAt(c, pagePt)
		}
		if b.pasteImageAt(formats, pagePt) {
			return true
		}
	}
	if b.ed.Paste() {
		b.invalidate()
		return true
	}
	return false
}

// pasteText turns clipboard text into one selected TypeText shape at the
// pointer, or at the viewport center when the pointer has not been in the
// canvas. It reports whether the paste was handled.
func (b *boardUI) pasteText(c *ui.Context) bool {
	return b.pasteTextAt(c, nil)
}

func (b *boardUI) pasteTextAt(c *ui.Context, pagePt *geo.Vec) bool {
	if b.locked || b.ed == nil || c == nil {
		return true
	}
	data := c.Services().ReadClipboardData(transfer.Text)
	raw, err := data.Read(transfer.Text)
	if err != nil {
		return true
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	text = strings.TrimSpace(strings.ReplaceAll(text, "\r", "\n"))
	if text == "" {
		return true
	}
	if r := []rune(text); len(r) > boardPasteTextMax {
		text = string(r[:boardPasteTextMax])
	}
	var at geo.Vec
	if pagePt != nil {
		at = *pagePt
	} else {
		pt := b.hoverScreenPt
		inside := b.stageRect.W > 0 && b.stageRect.Contains(float32(pt.X), float32(pt.Y))
		if !inside {
			pt = geo.V(float64(b.stageRect.X+b.stageRect.W/2), float64(b.stageRect.Y+b.stageRect.H/2))
			if b.stageRect.W <= 0 {
				pt = geo.V(b.ed.VW/2, b.ed.VH/2)
			}
		}
		at = b.ed.ToPage(pt)
	}
	b.ed.CreateText(at, text)
	b.invalidate()
	return true
}

func (b *boardUI) handleCanvasInput(c *ui.Context, ev ui.InputEvent) bool {
	if b.ed == nil || b.confirmDelete || b.confirmPageDelete {
		return false
	}
	pt := geo.V(float64(ev.X), float64(ev.Y))
	mods := boardMods(ev.Mods)
	t := b.seconds(ev.Time)
	switch ev.Kind {
	case ui.InputScroll:
		if ev.Mods&(ui.Cmd|ui.Ctrl) != 0 {
			// A discrete wheel (a mouse, or macOS without precise deltas) zooms
			// in steps on both platforms; a trackpad pans with precise deltas.
			if !ev.Precise {
				if ev.DY < 0 {
					b.ed.ZoomAt(pt, 1.2)
				} else if ev.DY > 0 {
					b.ed.ZoomAt(pt, 1/1.2)
				}
			} else {
				b.ed.ZoomAt(pt, math.Exp(-float64(ev.DY)*0.01))
			}
		} else {
			dx, dy := ev.DX, ev.DY
			// Windows sends Shift+wheel as a vertical delta; Mac's system
			// already turns it into DX, so swap only the discrete vertical form.
			if ev.Mods&ui.Shift != 0 && !ev.Precise && dx == 0 {
				dx, dy = dy, 0
			}
			b.ed.Pan(float64(dx), float64(dy))
		}
		b.invalidate()
		return true
	case ui.InputZoom:
		b.ed.ZoomAt(pt, float64(ev.Scale))
		b.invalidate()
		return true
	case ui.InputPointerDown:
		if b.radialOn && ev.Button == 0 {
			b.radialOn = false
			b.invalidate()
		}
		if ev.Button == 1 {
			b.ctxPt = pt
			if !b.locked {
				b.ed.RightClickAt(pt)
				b.invalidate()
			}
			return false
		}
		if !b.locked || ev.Button == 2 || b.ed.SpaceHeld() || b.ed.Tool == editor.ToolHand {
			b.ed.PointerDownPressure(float64(ev.X), float64(ev.Y), ev.Button, ev.Clicks, float64(ev.Pressure), ev.Pointer == ui.PointerPen, mods, t)
			if b.ed.Editing == "" {
				b.canvasFocus = false
			}
			b.invalidate()
			return true
		}
	case ui.InputPointerMove:
		b.hoverScreenPt = pt
		b.ed.PointerMovePressure(float64(ev.X), float64(ev.Y), float64(ev.Pressure), ev.Pointer == ui.PointerPen, mods, t)
		b.invalidate()
		return true
	case ui.InputPointerUp:
		if ev.Button == 1 {
			return false
		}
		if ev.Canceled {
			b.ed.Cancel()
		} else {
			b.ed.PointerUp(float64(ev.X), float64(ev.Y), mods, t)
		}
		b.invalidate()
		return true
	case ui.InputKeyDown:
		b.ed.ModsChanged(mods)
		if ev.Key == ui.KeySpace {
			b.ed.SetSpace(true)
			b.invalidate()
			return true
		}
		if ev.Mods&(ui.Cmd|ui.Ctrl) != 0 {
			switch ev.Key {
			case ui.KeyZ:
				if !b.locked {
					if ev.Mods&ui.Shift != 0 {
						b.ed.Redo()
					} else {
						b.ed.Undo()
					}
					b.invalidate()
				}
				return true
			case ui.KeyY:
				if !b.locked {
					b.ed.Redo()
					b.invalidate()
				}
				return true
			case ui.KeyA:
				b.ed.SelectAll()
				b.invalidate()
				return true
			case ui.KeyC:
				if ev.Mods&ui.Shift != 0 {
					_ = b.copyAsPNG(b.ed.Selection(), false)
				} else if ev.Mods&ui.Alt != 0 {
					b.ed.CopyStyle()
				} else {
					b.copySelection(c)
				}
				b.invalidate()
				return true
			case ui.KeyX:
				if !b.locked {
					b.cutSelection(c)
					b.invalidate()
				}
				return true
			case ui.KeyU:
				b.pickImage()
				return true
			case ui.KeyV:
				if ev.Mods&ui.Alt != 0 {
					if !b.locked {
						b.ed.PasteStyle()
						b.invalidate()
					}
					return true
				}
				if b.pasteFromClipboard(c, nil) {
					return true
				}
			case ui.KeyD:
				if !b.locked && b.ed.Duplicate() {
					b.invalidate()
					return true
				}
			case ui.KeyG:
				if !b.locked {
					if ev.Mods&ui.Shift != 0 {
						b.ed.UngroupSelected()
					} else {
						b.ed.GroupSelected()
					}
					b.invalidate()
				}
				return true
			case ui.KeyL:
				if !b.locked && ev.Mods&ui.Shift != 0 {
					b.ed.ToggleLockSelected()
					b.invalidate()
					return true
				}
			case ui.KeyBracketRight:
				if !b.locked {
					if ev.Mods&ui.Shift != 0 {
						b.ed.BringToFront()
					} else {
						b.ed.BringForward()
					}
					b.invalidate()
				}
				return true
			case ui.KeyBracketLeft:
				if !b.locked {
					if ev.Mods&ui.Shift != 0 {
						b.ed.SendToBack()
					} else {
						b.ed.SendBackward()
					}
					b.invalidate()
				}
				return true
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
			case ui.KeyQuote:
				b.ed.ToggleGrid()
				b.invalidate()
				return true
			}
			// Every other modified key stops here: an unbound Ctrl+S or a
			// Ctrl+V whose clipboard had nothing to paste must not fall
			// through and switch the tool under the bare key. The keys the
			// bare handlers own keep their meaning with Cmd/Ctrl held:
			// Cmd+Delete clears a selection, Cmd+arrow nudges it, Escape
			// dismisses and Cmd+Enter edits.
			if !boardEditingKey(ev.Key) {
				return true
			}
		}
		if ev.Mods&ui.Alt != 0 && ev.Mods&(ui.Cmd|ui.Ctrl) == 0 && !b.locked {
			switch ev.Key {
			case ui.KeyA:
				if b.ed.AlignSelected("left") {
					b.invalidate()
					return true
				}
			case ui.KeyH:
				if ev.Mods&ui.Shift != 0 {
					if b.ed.DistributeSelected("x") {
						b.invalidate()
						return true
					}
				} else if b.ed.AlignSelected("center") {
					b.invalidate()
					return true
				}
			case ui.KeyD:
				if b.ed.AlignSelected("right") {
					b.invalidate()
					return true
				}
			case ui.KeyW:
				if b.ed.AlignSelected("top") {
					b.invalidate()
					return true
				}
			case ui.KeyV:
				if ev.Mods&ui.Shift != 0 {
					if b.ed.DistributeSelected("y") {
						b.invalidate()
						return true
					}
				} else if b.ed.AlignSelected("middle") {
					b.invalidate()
					return true
				}
			case ui.KeyS:
				if b.ed.AlignSelected("bottom") {
					b.invalidate()
					return true
				}
			}
		}
		if ev.Mods&ui.Shift != 0 && ev.Mods&(ui.Cmd|ui.Ctrl|ui.Alt) == 0 {
			switch ev.Key {
			case ui.Key0:
				b.ed.ResetZoom()
				b.invalidate()
				return true
			case ui.Key1:
				b.ed.ZoomToFit()
				b.invalidate()
				return true
			case ui.Key2:
				b.ed.ZoomToSelection()
				b.invalidate()
				return true
			case ui.KeyH:
				if !b.locked && b.ed.FlipSelected("x") {
					b.invalidate()
					return true
				}
			case ui.KeyV:
				if !b.locked && b.ed.FlipSelected("y") {
					b.invalidate()
					return true
				}
			case ui.KeyPeriod:
				if !b.locked && b.ed.RotateSelected90(1) {
					b.invalidate()
					return true
				}
			case ui.KeyComma:
				if !b.locked && b.ed.RotateSelected90(-1) {
					b.invalidate()
					return true
				}
			}
		}
		switch ev.Key {
		case ui.KeyEscape:
			if b.radialOn {
				b.radialOn = false
				b.invalidate()
				return true
			}
			hadState := b.ed.InGesture() || len(b.ed.Selection()) > 0 || b.ed.Editing != "" || b.ed.EditingVertices != "" || b.ed.Cropping != ""
			b.ed.Cancel()
			if !hadState && b.focusMode {
				b.focusMode = false
			}
			b.invalidate()
			return true
		case ui.KeyQ:
			b.ed.ToggleToolLock()
			b.invalidate()
			return true
		case ui.KeyC:
			if !b.locked && ev.Mods == 0 {
				if b.radialOn {
					b.radialOn = false
				} else {
					b.openRadialPalette(b.hoverScreenPt)
				}
				b.invalidate()
				return true
			}
		case ui.KeyK:
			b.ed.SetTool(editor.ToolLaser)
			b.invalidate()
			return true
		case ui.KeyEnter:
			if !b.locked && b.ed.EnterEdit() {
				b.invalidate()
				return true
			}
		case ui.KeyLeft, ui.KeyRight, ui.KeyUp, ui.KeyDown:
			if !b.locked && len(b.ed.Selection()) > 0 {
				step := 1.0
				if ev.Mods&ui.Shift != 0 {
					step = 10.0
				}
				var dx, dy float64
				switch ev.Key {
				case ui.KeyLeft:
					dx = -step
				case ui.KeyRight:
					dx = step
				case ui.KeyUp:
					dy = -step
				case ui.KeyDown:
					dy = step
				}
				b.ed.Nudge(dx, dy)
				b.invalidate()
				return true
			}
		case ui.KeyBackspace, ui.KeyDelete:
			if !b.locked {
				b.ed.DeleteSelected()
				b.invalidate()
			}
			return true
		case ui.KeyV:
			b.ed.SetTool(editor.ToolSelect)
			b.invalidate()
			return true
		case ui.KeyS:
			b.ed.SetTool(editor.ToolLasso)
			b.invalidate()
			return true
		case ui.KeyH:
			b.ed.SetTool(editor.ToolHand)
			b.invalidate()
			return true
		case ui.KeyD:
			if ev.Mods&ui.Shift != 0 {
				b.ed.SetTool(editor.ToolHighlight)
			} else {
				b.ed.SetTool(editor.ToolDraw)
			}
			b.invalidate()
			return true
		case ui.KeyE:
			b.ed.SetTool(editor.ToolEraser)
			b.invalidate()
			return true
		case ui.KeyR:
			b.ed.SetGeoTool("rectangle")
			b.invalidate()
			return true
		case ui.KeyO:
			b.ed.SetGeoTool("ellipse")
			b.invalidate()
			return true
		case ui.KeyP:
			b.ed.SetGeoTool("polygon")
			b.invalidate()
			return true
		case ui.KeyA:
			b.ed.SetTool(editor.ToolArrow)
			b.invalidate()
			return true
		case ui.KeyL:
			b.ed.SetTool(editor.ToolLine)
			b.invalidate()
			return true
		case ui.KeyF:
			b.ed.SetTool(editor.ToolFrame)
			b.invalidate()
			return true
		case ui.KeyT:
			b.ed.SetTool(editor.ToolText)
			b.invalidate()
			return true
		case ui.KeyN:
			b.ed.SetTool(editor.ToolNote)
			b.invalidate()
			return true
		case ui.Key0:
			if ev.Mods&ui.Shift != 0 {
				b.ed.ResetZoom()
				b.invalidate()
				return true
			}
		}
	case ui.InputKeyUp:
		b.ed.ModsChanged(mods)
		if ev.Key == ui.KeySpace {
			b.ed.SetSpace(false)
			b.invalidate()
			return true
		}
	}
	return false
}

// boardEditingKey reports whether a Cmd/Ctrl key is one the bare handlers
// own as well: the modifier guard lets these past it so deleting, nudging,
// editing and dismissing keep working with the modifier held, while the
// unbound letters stay swallowed.
func boardEditingKey(key ui.Key) bool {
	switch key {
	case ui.KeyBackspace, ui.KeyDelete, ui.KeyLeft, ui.KeyRight,
		ui.KeyUp, ui.KeyDown, ui.KeyEnter, ui.KeyEscape:
		return true
	}
	return false
}
