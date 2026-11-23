package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"rakko/internal/board/doc"
	"rakko/internal/board/editor"
	"rakko/internal/board/geo"
)

const (
	boardExportTileSize = 1024
	boardExportMaxDim   = 8192
)

var (
	boardSaveFileDialog = func(w *mygo.Window, name string, filterName string, exts []string) (string, error) {
		return mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      w,
			DefaultPath: name,
			Filters:     []mygo.FileFilter{{Name: filterName, Extensions: exts}},
		})
	}
	boardOpenBoardDialog = func(w *mygo.Window) ([]string, error) {
		return mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:   w,
			Multiple: false,
			Filters:  []mygo.FileFilter{{Name: "Rakko Board", Extensions: []string{"json", "rakkoboard.json"}}},
		})
	}
	boardWriteClipboardData = func(d transfer.Data, concealed bool) error {
		return mygo.Clipboard.Write(d, mygo.ClipboardOptions{Concealed: concealed})
	}
)

func (b *boardUI) exportBounds(ids []doc.ID) (geo.Box, bool) {
	if len(ids) > 0 {
		var u geo.Box
		first := true
		for _, id := range ids {
			if b.ed.Doc.Shapes[id] == nil {
				continue
			}
			box := b.ed.PageBounds(id)
			if first {
				u, first = box, false
			} else {
				u = u.Union(box)
			}
		}
		return u, !first
	}
	return b.ed.PageContentBox(b.ed.Page)
}

func (b *boardUI) renderTiledPNG(ids []doc.ID) (pngBytes []byte, width, height, tiles int, err error) {
	if b.ed == nil {
		return nil, 0, 0, 0, fmt.Errorf("board: nil editor")
	}
	box, ok := b.exportBounds(ids)
	const pad = 24.0
	if !ok {
		box = geo.Box{X: 0, Y: 0, W: 640, H: 400}
	} else {
		box = box.Expand(pad)
	}
	scale := 2.0
	maxSpan := math.Max(box.W, box.H)
	if maxSpan*scale > boardExportMaxDim {
		scale = float64(boardExportMaxDim) / math.Max(maxSpan, 1)
	}
	width = max(1, min(boardExportMaxDim, int(math.Ceil(box.W*scale))))
	height = max(1, min(boardExportMaxDim, int(math.Ceil(box.H*scale))))

	var filterSet map[doc.ID]bool
	if len(ids) > 0 {
		filterSet = make(map[doc.ID]bool, len(ids)*2)
		for _, id := range ids {
			filterSet[id] = true
			for _, kid := range b.ed.Descendants(id) {
				filterSet[kid] = true
			}
		}
	}

	pal := b.ed.Palette()
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	for ty := 0; ty < height; ty += boardExportTileSize {
		th := min(boardExportTileSize, height-ty)
		for tx := 0; tx < width; tx += boardExportTileSize {
			tw := min(boardExportTileSize, width-tx)
			tiles++
			tileX, tileY := tx, ty
			tileRGBA := ui.Render(func(c *ui.Context) {
				ui.Box(c).Size(float32(tw), float32(th)).Draw(func(p *ui.Painter, r ui.Rect) {
					p.Fill(r, boardColor(pal.Background), 0)
					cam := editor.Camera{
						X: -box.X - float64(tileX)/scale,
						Y: -box.Y - float64(tileY)/scale,
						Z: scale,
					}
					sc := &boardScene{
						b:      b,
						p:      p,
						pal:    pal,
						cam:    cam,
						origin: geo.V(float64(r.X), float64(r.Y)),
						view: geo.Box{
							X: box.X + float64(tileX)/scale,
							Y: box.Y + float64(tileY)/scale,
							W: float64(tw) / scale,
							H: float64(th) / scale,
						},
					}
					sc.lightInk = sc.estimateMaskArea(b.ed.Page) > boardAtlasBudget
					if len(filterSet) == 0 {
						sc.renderChildren(b.ed.Page)
						return
					}
					sc.renderFilteredPage(filterSet)
				})
			}, tw, th, 1.0)
			if tileRGBA != nil {
				draw.Draw(dst, image.Rect(tx, ty, tx+tw, ty+th), tileRGBA, image.Point{}, draw.Src)
			}
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, 0, 0, tiles, err
	}
	return buf.Bytes(), width, height, tiles, nil
}

func (s *boardScene) renderFilteredPage(keep map[doc.ID]bool) {
	s.renderFilteredLayer(s.b.ed.Page, keep, true)
	s.renderFilteredLayer(s.b.ed.Page, keep, false)
}

func (s *boardScene) renderFilteredLayer(parent doc.ID, keep map[doc.ID]bool, highlights bool) {
	for _, id := range s.b.ed.Children(parent) {
		sh := s.b.ed.Doc.Shapes[id]
		if sh == nil {
			continue
		}
		if sh.Type == doc.TypeGroup {
			s.renderFilteredLayer(id, keep, highlights)
			continue
		}
		isHL := sh.Type == doc.TypeHighlight
		if isHL == highlights && keep[id] {
			s.renderShape(sh)
		}
		if sh.Type == doc.TypeFrame {
			if keep[id] {
				s.renderFrameClippedFiltered(sh, keep, highlights)
			} else {
				s.renderFilteredLayer(id, keep, highlights)
			}
		}
	}
}

func (s *boardScene) renderFrameClippedFiltered(frame *doc.Shape, keep map[doc.ID]bool, highlights bool) {
	m := s.camMat().Mul(s.b.ed.PageMat(frame.ID))
	cs := geo.Box{W: math.Max(frame.W, 1), H: math.Max(frame.H, 1)}.Corners()
	tl := m.Apply(cs[0])
	br := m.Apply(cs[2])
	r := ui.Rect{
		X: float32(math.Min(tl.X, br.X)),
		Y: float32(math.Min(tl.Y, br.Y)),
		W: float32(math.Abs(br.X - tl.X)),
		H: float32(math.Abs(br.Y - tl.Y)),
	}
	s.p.Clip(r, 0, func() {
		s.renderFilteredLayer(frame.ID, keep, highlights)
	})
}

func (b *boardUI) svgImageResolver() editor.ImageDataResolver {
	return func(assetID doc.ID) ([]byte, string, bool) {
		if b.ed == nil || b.store == nil {
			return nil, "", false
		}
		a := b.ed.Doc.Assets[assetID]
		if a == nil || a.Path == "" {
			return nil, "", false
		}
		full, err := b.store.AssetPath(a.Path)
		if err != nil {
			return nil, "", false
		}
		raw, err := os.ReadFile(full)
		if err != nil {
			return nil, "", false
		}
		mime := a.Mime
		if mime == "" {
			ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(a.Path)), ".")
			if ext == "" {
				ext = "png"
			}
			mime = "image/" + ext
		}
		return raw, mime, true
	}
}

func (b *boardUI) exportSVGString(ids []doc.ID) (string, error) {
	if b.ed == nil {
		return "", fmt.Errorf("board: nil editor")
	}
	return b.ed.ExportSVG(ids, b.ed.Palette(), b.svgImageResolver())
}

func (b *boardUI) safeFileBaseName() string {
	name := "board"
	if b.ed != nil && strings.TrimSpace(b.ed.Doc.Name) != "" {
		name = strings.TrimSpace(b.ed.Doc.Name)
	}
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			return '-'
		}
		return r
	}, name)
	return name
}

func (b *boardUI) exportPNGFile(ids []doc.ID) {
	data, _, _, _, err := b.renderTiledPNG(ids)
	if err != nil {
		b.host.report(err)
		return
	}
	path, err := boardSaveFileDialog(b.win, b.safeFileBaseName()+".png", "PNG Image", []string{"png"})
	if err != nil || path == "" {
		return
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		b.host.report(err)
	}
}

func (b *boardUI) exportSVGFile(ids []doc.ID) {
	svg, err := b.exportSVGString(ids)
	if err != nil {
		b.host.report(err)
		return
	}
	path, err := boardSaveFileDialog(b.win, b.safeFileBaseName()+".svg", "SVG Vector", []string{"svg"})
	if err != nil || path == "" {
		return
	}
	if err := os.WriteFile(path, []byte(svg), 0o600); err != nil {
		b.host.report(err)
	}
}

func (b *boardUI) exportBoardFile() {
	if b.ed == nil || b.store == nil {
		return
	}
	raw, err := doc.ExportPackage(b.ed.Doc, b.store)
	if err != nil {
		b.host.report(err)
		return
	}
	path, err := boardSaveFileDialog(b.win, b.safeFileBaseName()+".rakkoboard.json", "Rakko Board", []string{"json"})
	if err != nil || path == "" {
		return
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		b.host.report(err)
	}
}

func (b *boardUI) importBoardFile() {
	if b.locked || b.store == nil {
		return
	}
	paths, err := boardOpenBoardDialog(b.win)
	if err != nil || len(paths) == 0 {
		return
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		b.host.report(err)
		return
	}
	d, err := doc.ImportPackage(raw, b.store, b.host.appLoc().BoardDefaultName())
	if err != nil {
		b.host.report(err)
		return
	}
	if err := b.selectBoard(d.ID); err != nil {
		b.host.report(err)
		return
	}
	b.refreshBoards()
	b.invalidate()
}

func (b *boardUI) copyAsPNG(ids []doc.ID, concealed bool) error {
	data, _, _, _, err := b.renderTiledPNG(ids)
	if err != nil {
		return err
	}
	item := transfer.NewItem(transfer.Bytes(transfer.PNG, data))
	return boardWriteClipboardData(transfer.New(item), concealed)
}

func (b *boardUI) copyAsSVG(ids []doc.ID, concealed bool) error {
	svg, err := b.exportSVGString(ids)
	if err != nil {
		return err
	}
	return boardWriteClipboardData(transfer.TextData(svg), concealed)
}

// writeDragPNGTemp renders the selection or board to a temporary PNG file and
// returns transfer.FileData(path), because mygo does not support virtual file promises.
func (b *boardUI) writeDragPNGTemp(ids []doc.ID, dir string) (string, transfer.Data, error) {
	data, _, _, _, err := b.renderTiledPNG(ids)
	if err != nil {
		return "", transfer.Data{}, err
	}
	if dir == "" {
		dir = filepath.Join(os.TempDir(), "rakko-board-drag")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", transfer.Data{}, err
	}
	f, err := os.CreateTemp(dir, b.safeFileBaseName()+"-*.png")
	if err != nil {
		return "", transfer.Data{}, err
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", transfer.Data{}, err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", transfer.Data{}, err
	}
	b.dragTempFiles = append(b.dragTempFiles, path)
	td, err := transfer.FileData(path)
	if err != nil {
		return path, transfer.Data{}, err
	}
	return path, td, nil
}
