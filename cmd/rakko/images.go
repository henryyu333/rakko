//go:build darwin

package main

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/store"
)

// maxConcurrentThumbs 限制同时在后台解码的缩略图数，避免多张大图同时解码。
const maxConcurrentThumbs = 2

// thumbPx is a thumbnail's longer side in pixels.
const thumbPx = 80

// goDecodable are the picture extensions decodeThumb reads with Go's
// decoders; every other picture in imageExts goes through ImageIO.
var goDecodable = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}

func (a *shelf) thumbnail(item store.Item) *ui.Bitmap {
	if item.Kind == store.KindText || item.Missing {
		return nil
	}
	if b, ok := a.thumbnails[item.ID]; ok {
		return b
	}
	if a.win == nil || a.thumbPending[item.ID] {
		return nil
	}
	// 并发解码上限 2：名额满则留待完成回调触发的重绘再试，
	// 不排队、不另起后台 goroutine。
	if a.thumbActive >= maxConcurrentThumbs {
		return nil
	}
	a.thumbActive++
	a.thumbPending[item.ID] = true
	go func() {
		var b *ui.Bitmap
		ext := strings.ToLower(filepath.Ext(item.Path))
		picture := item.Kind == store.KindImage || imageExts[ext]
		if goDecodable[ext] || item.Kind == store.KindImage {
			if img := decodeThumb(item.Path); img != nil {
				b = ui.NewBitmap(img)
			}
		}
		// HEIC, TIFF, AVIF and pictures too large for decodeThumb: ImageIO
		// decodes them downscaled and upright.
		if b == nil && picture {
			if img, err := mac.Thumbnail(item.Path, thumbPx); err == nil {
				b = ui.NewBitmap(img)
			}
		}
		a.win.Update(func() {
			a.thumbActive--
			if a.quitting {
				return
			}
			delete(a.thumbPending, item.ID)
			// invalidateThumbnail 标记的在途结果直接丢弃，由重绘按上限重新解码。
			if a.thumbDiscard[item.ID] {
				delete(a.thumbDiscard, item.ID)
				return
			}
			// 已删除的项不取 Finder 图标、不把缓存重新放回来。
			if a.store != nil {
				for _, current := range a.store.Items {
					if current.ID == item.ID {
						if b == nil {
							if icon := mac.FileIcon(item.Path, 40); len(icon) > 0 {
								b, _ = ui.DecodeBitmap(icon)
							}
						}
						a.thumbnails[item.ID] = b
						break
					}
				}
			}
		})
	}()
	return nil
}

// invalidateThumbnail 丢弃缓存的缩略图；若解码仍在进行，标记其结果
// 在完成回调中丢弃而不写回缓存，由随后的重绘按并发上限重新解码。
func (a *shelf) invalidateThumbnail(id string) {
	delete(a.thumbnails, id)
	if a.thumbPending[id] {
		if a.thumbDiscard == nil {
			a.thumbDiscard = make(map[string]bool)
		}
		a.thumbDiscard[id] = true
	}
}

// clearThumbnails 丢弃全部缓存缩略图；仍在解码的在途结果标记为完成回调
// 中丢弃而不写回缓存。清空后缩略图不会再被旧解码结果填回：完成回调里
// thumbDiscard 命中直接丢弃，未命中的也要求该项仍是 store 成员才写回。
// 同一项在旧解码在途时重新加入，thumbDiscard 也会丢弃过期结果，由随后的
// 重绘按并发上限重新解码。
func (a *shelf) clearThumbnails() {
	clear(a.thumbnails)
	for id := range a.thumbPending {
		if a.thumbDiscard == nil {
			a.thumbDiscard = make(map[string]bool)
		}
		a.thumbDiscard[id] = true
	}
}

// dropStaleThumbnails discards cached thumbnails of items no longer
// stored; a decode still in flight for such an item is marked stale too,
// so its result is dropped by the completion callback instead of writing
// back a thumbnail for a removed row.
func (a *shelf) dropStaleThumbnails() {
	present := make(map[string]bool, len(a.store.Items))
	for _, it := range a.store.Items {
		present[it.ID] = true
	}
	for id := range a.thumbnails {
		if !present[id] {
			a.invalidateThumbnail(id)
		}
	}
	for id := range a.thumbPending {
		if !present[id] {
			a.invalidateThumbnail(id)
		}
	}
}

// trayIconFile is the menu-bar template icon in the app's resources.
const trayIconFile = "tray.png"

// trayIcon loads the menu-bar icon from the resource directory dir. The
// image is black-and-transparent so macOS can tint it as a template; a
// missing or unreadable file returns nil and the tray then shows its title
// instead, which keeps the menu-bar item clickable.
func trayIcon(dir string) []byte {
	if dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(dir, trayIconFile))
	if err != nil {
		return nil
	}
	// A file that is not a PNG would make the tray fail to install; the
	// title fallback is better than a menu-bar item that never appears.
	if _, err := png.DecodeConfig(bytes.NewReader(b)); err != nil {
		return nil
	}
	return b
}
