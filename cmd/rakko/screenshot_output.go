//go:build darwin

package main

// What the screenshot session produces: the cropped PNG, the clipboard
// pair of picture and file URL, the saved file, the recognized text and
// the pinned window. The temporary folder belongs to this file too.

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	stdpng "image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// Outputs.

// outputPNG crops the frozen picture to the selection and, when asked,
// composites the annotations at the crop's own pixel size. Mosaic cells and
// strokes are scaled from points, so the export matches the preview.
func (s *shotSession) outputPNG(withAnnotations bool) ([]byte, error) {
	d := s.ownerDisplay()
	if d == nil || !s.hasSel {
		return nil, errors.New("no selection")
	}
	crop := shotCropRect(s.sel, d.shotTarget, d.pixW, d.pixH)
	w, h := crop.Dx(), crop.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty selection")
	}
	base := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(base, base.Bounds(), d.img, crop.Min, draw.Src)
	if withAnnotations && len(s.hist.annots) > 0 {
		sx := float64(w) / float64(max(s.sel.Width, 1))
		sy := float64(h) / float64(max(s.sel.Height, 1))
		origin := mygo.Point{X: s.sel.X, Y: s.sel.Y}
		scale := float32((sx + sy) / 2)
		layer := ui.Render(func(c *ui.Context) {
			c.Root().Background(ui.Transparent)
			ui.Box(c.Key("marks")).Fill().Draw(func(p *ui.Painter, r ui.Rect) {
				for _, an := range s.hist.annots {
					shotDrawAnnotation(p, an, origin, scale)
				}
			})
		}, w, h, 1)
		draw.Draw(base, base.Bounds(), layer, image.Point{}, draw.Over)
	}
	var buf bytes.Buffer
	if err := stdpng.Encode(&buf, base); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// copy puts the annotated crop on the pasteboard as PNG and as a file URL,
// records it in the clipboard page, and leaves the overlay.
func (s *shotSession) copy() {
	s.finishText()
	png, err := s.outputPNG(true)
	if err != nil {
		s.a.report(err)
		return
	}
	if !s.a.copyShotPNG(png) {
		return
	}
	s.close(true)
}

// copyShotPNG is the clipboard half shared by the overlay and a pinned
// screenshot: write the picture and its file URL, record the copy, say so.
func (a *shelf) copyShotPNG(png []byte) bool {
	path, err := a.writeShotTempPNG(png)
	if err != nil {
		a.report(err)
		return false
	}
	if err := shotClipboard(png, path); err != nil {
		a.report(err)
		return false
	}
	a.recordShotClip(png)
	a.toastStatus(a.appLoc().ShotCopied(), shotClock().Add(3*time.Second), statusOK)
	return true
}

// save writes the annotated crop to the system screenshot folder.
func (s *shotSession) save() {
	s.finishText()
	png, err := s.outputPNG(true)
	if err != nil {
		s.a.report(err)
		return
	}
	if _, err := s.a.saveShotPNG(png); err != nil {
		s.a.report(err)
		return
	}
	s.close(true)
	s.a.toastStatus(s.a.appLoc().ShotSaved(), shotClock().Add(3*time.Second), statusOK)
}

// recognize sends the unannotated selection through the app's one Vision
// path; the result lands on the pasteboard with the 框选文字 wording.
func (s *shotSession) recognize() {
	s.finishText()
	png, err := s.outputPNG(false)
	if err != nil {
		s.a.report(err)
		return
	}
	a := s.a
	if a.ocrScan {
		a.toastStatus(a.appLoc().OcrBusy(), shotClock().Add(3*time.Second), statusInfo)
		return
	}
	path, err := a.writeShotTempPNG(png)
	if err != nil {
		a.report(err)
		return
	}
	s.close(true)
	a.ocrScan = true
	a.ocrBusyAt = shotClock()
	a.ocrRunID++
	runID := a.ocrRunID
	timer := shotAfter(ocrProgressDelay, func() {
		if a.ocrRunID == runID {
			ocrInvalidate(a)
		}
	})
	shotWork(func() {
		defer os.Remove(path)
		lines, err := ocrRecognize(path)
		text := joinOCRLines(lines)
		shotDispatch(func() { a.finishScreenOCR(timer, text, true, err) })
	})
}

// shotOpenPin is the pin window's seam: a headless shelf must not create
// one.
var shotOpenPin = func(a *shelf, png []byte, frame mygo.Rectangle, scale float64) {
	a.openShotPin(png, frame, scale)
}

// pin opens a floating copy of the annotated crop on the desktop.
func (s *shotSession) pin() {
	s.finishText()
	png, err := s.outputPNG(true)
	if err != nil {
		s.a.report(err)
		return
	}
	d := s.ownerDisplay()
	if d == nil {
		return
	}
	frame := s.pinFrame(d)
	s.close(true)
	shotOpenPin(s.a, png, frame, d.scale())
}

// scale is the display's backing scale, or 1.
func (d *shotDisplay) scale() float64 {
	if d.Scale <= 0 {
		return 1
	}
	return d.Scale
}

// pinFrame is where a pinned crop first appears: over its selection, kept
// inside the display's work area.
func (s *shotSession) pinFrame(d *shotDisplay) mygo.Rectangle {
	w, h := shotPixelSize(s.sel, d.shotTarget, d.pixW, d.pixH)
	width := int(math.Round(float64(w) / d.scale()))
	height := int(math.Round(float64(h) / d.scale()))
	area := workAreaOf(d.ID)
	if area.Width == 0 {
		area = d.Bounds
	}
	maxW := int(float64(area.Width) * 0.9)
	maxH := int(float64(area.Height) * 0.9)
	if width > maxW || height > maxH {
		k := min(float64(maxW)/float64(max(width, 1)), float64(maxH)/float64(max(height, 1)))
		width = max(1, int(float64(width)*k))
		height = max(1, int(float64(height)*k))
	}
	f := mygo.Rectangle{X: s.sel.X, Y: s.sel.Y, Width: max(width, 32), Height: max(height, 32)}
	return shotClampRect(f, area)
}

// workAreaOf is the display's visible frame, or the primary's when the
// display is gone (unplugged during the capture).
func workAreaOf(id int64) mygo.Rectangle {
	var primary mygo.Rectangle
	for _, d := range shotDisplays() {
		if d.ID == id {
			return d.WorkArea
		}
		if d.Primary || primary == (mygo.Rectangle{}) {
			primary = d.WorkArea
		}
	}
	return primary
}

// writeShotTempPNG puts png in the app's temporary screenshot folder under
// the system-style name and returns the path.
func (a *shelf) writeShotTempPNG(png []byte) (string, error) {
	dir := shotTempDir()
	name := shotUniqueName(a.shotName(shotClock()), func(n string) bool {
		_, err := os.Stat(filepath.Join(dir, n))
		return err == nil
	})
	path := filepath.Join(dir, name)
	return path, shotWriteFile(path, png, 0o600)
}

// saveShotPNG saves png into the system screenshot folder.
func (a *shelf) saveShotPNG(png []byte) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := shotSaveDir(shotReadSaveDir(), home)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		dir = filepath.Join(home, "Desktop")
	}
	name := shotUniqueName(a.shotName(shotClock()), func(n string) bool {
		_, err := os.Stat(filepath.Join(dir, n))
		return err == nil
	})
	path := filepath.Join(dir, name)
	return path, os.WriteFile(path, png, 0o644)
}

// shotName is the system screenshot's file name in the app's language.
func (a *shelf) shotName(t time.Time) string {
	l := a.appLoc()
	return shotFileName(t, l.ShotFileNamePrefix(), l.ShotFileNameAt())
}

// recordShotClip adds the copied crop to the clipboard page as a screenshot
// of Rakko's own: the pasteboard write also carries a file URL, which the
// ordinary watcher skips (Finder files are never recorded), so this is the
// one record.
func (a *shelf) recordShotClip(png []byte) {
	if a.store == nil {
		return
	}
	a.clip.skipCurrent()
	if err := a.store.AddClipImage(png, clipSourceShot); err != nil {
		a.report(err)
		return
	}
	a.changed(nil)
}

// shotTempDir is the folder copy's file URL lives in, under the system
// temporary directory. It is emptied at the start of each screenshot; the
// report and the gallery live beside it, not in it, and are never touched.
func shotTempDir() string {
	return filepath.Join(os.TempDir(), "rakko-shot", "clip")
}

// cleanShotTempDir empties the folder at the start of a session; a pinned
// screenshot keeps its own window and bytes, not a file.
func cleanShotTempDir() {
	dir := shotTempDir()
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	os.MkdirAll(dir, 0o700)
}

// readScreenCaptureLocation reads where macOS saves screenshots, from
// `defaults read com.apple.screencapture location`. A missing preference
// is an error, and shotSaveDir falls back to ~/Desktop.
func readScreenCaptureLocation() string {
	out, err := exec.Command("/usr/bin/defaults", "read", "com.apple.screencapture", "location").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// shotClipboardData is what a screenshot copy offers: the picture and the
// file URL as two items, so an app that wants a picture gets one and Finder
// gets a file to paste.
func shotClipboardData(png []byte, path string) (transfer.Data, error) {
	files, err := transfer.FileData(path)
	if err != nil {
		return transfer.Data{}, err
	}
	items := append([]transfer.Item{transfer.NewItem(transfer.Bytes(transfer.PNG, png))}, files.Items()...)
	return transfer.New(items...), nil
}

// writeShotClipboard puts that data on the system pasteboard.
func writeShotClipboard(png []byte, path string) error {
	data, err := shotClipboardData(png, path)
	if err != nil {
		return err
	}
	return mygo.Clipboard.Write(data)
}

// clipSourceShot is the SourceApp marker of screenshots Rakko produced.
// Like clipSourceOCR it is not a bundle id; the clipboard row names it.
const clipSourceShot = "shelf:shot"
