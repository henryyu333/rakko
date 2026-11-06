//go:build darwin

package main

// 「框选文字」：用户在屏幕上框一块区域，区域里的文字识别后直接进剪贴板，
// 刘海用完成胶囊报结果（设计稿第 5 节）。框选界面是系统 screencapture
// 自己的（十字光标、Esc 取消、空格切窗口模式），识别走全应用同一把锁的
// recognizeImageText —— 后台索引正在识别时框选排队，队列加运行超过
// ocrProgressDelay 就在刘海上亮「正在识别…」的进行中胶囊。
//
// 屏幕录制权限只读预检（CGPreflightScreenCaptureAccess）不会弹框：没有
// 权限时先展开面板出说明卡，「打开系统设置」才是用户主动发起的申请。
// 启动时不检查、不申请。

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/mac"
	"rakko/internal/weekly"
)

// ocrProgressDelay 是识别（含在后台索引后排队）能安静跑完的窗口：超过它
// 刘海才长出「正在识别…」的橙点呼吸胶囊（design §5 完成图）。
const ocrProgressDelay = 250 * time.Millisecond

// Seams for the headless tests: the permission probe, the interactive
// capture and the permission request are replaced; the worker goroutine and
// the main-thread dispatch mirror convertWork/convertDispatch.
var (
	ocrPreflight  = mac.ScreenCaptureAllowed
	ocrGrant      = mac.RequestScreenCapture
	ocrCapture    = captureRegion
	ocrWork       = func(fn func()) { go fn() }
	ocrDispatch   = mygo.RunOnMain
	ocrRecognize  = recognizeImageText
	ocrAfterFunc  = time.AfterFunc
	ocrInvalidate = func(a *shelf) {
		if a.win != nil {
			a.win.Invalidate()
		}
	}
)

// captureRegion runs the system's own region picker (screencapture -i):
// crosshair, Esc to cancel, Space for the window mode. -x keeps the shutter
// sound off. A cancelled pick (Esc) or a denied capture writes nothing, so
// the caller treats a missing file as cancellation and stays silent — a
// denied build's system dialog is shown by macOS itself.
func captureRegion(path string) error {
	return exec.Command("/usr/sbin/screencapture", "-i", "-x", path).Run()
}

// startScreenOCR is 「框选文字」's one entry for every trigger: the ⌃⌥O
// shortcut, the tray menu and the tools page's card (runToolCard). The
// whole start runs on the UI thread so the permission sheet and the
// capture flag stay single-owner; the interactive capture and
// recognition run off it.
func (a *shelf) startScreenOCR() {
	ocrDispatch(func() {
		switch {
		case a.ocrScan || a.ocrSheet:
			// One capture at a time, and no new round while the permission
			// card still waits for the user's grant — both are ignored.
			return
		case !ocrPreflight():
			// No prompt from us: the panel opens on the current page's
			// stage with the permission card instead (design §5).
			a.ocrSheet = true
			if !a.visible {
				a.show(false)
			} else if a.win != nil {
				a.win.Invalidate()
			}
			return
		}
		a.ocrScan = true
		a.ocrBusyAt = time.Now()
		a.ocrRunID++
		// The panel must not cover what the user wants to capture.
		if a.visible {
			a.hide(hideExplicit)
		}
		timer := ocrAfterFunc(ocrProgressDelay, func() {
			ocrInvalidate(a) // wakes the live progress capsule
		})
		ocrWork(func() { a.screenOCRRound(timer) })
	})
}

// screenOCRRound is the worker half: capture, recognize under the app's
// one Vision lock, then hand the verdict to the main thread. Nothing it
// fails to produce (Esc, denial, an unreadable shot) says anything.
func (a *shelf) screenOCRRound(timer *time.Timer) {
	path := filepath.Join(os.TempDir(), fmt.Sprintf("shelf-ocr-%d.png", os.Getpid()))
	defer os.Remove(path)
	if err := ocrCapture(path); err != nil {
		ocrDispatch(func() { a.finishScreenOCR(timer, "", false, nil) })
		return
	}
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		ocrDispatch(func() { a.finishScreenOCR(timer, "", false, nil) })
		return
	}
	lines, err := ocrRecognize(path)
	text := joinOCRLines(lines)
	ocrDispatch(func() { a.finishScreenOCR(timer, text, true, err) })
}

// finishScreenOCR lands the verdict on the UI thread: the progress capsule
// clears with the flag, and the result reuses 「识别文字并复制」's wording —
// success counts runes, nothing found stays a neutral info line.
func (a *shelf) finishScreenOCR(timer *time.Timer, text string, produced bool, err error) {
	timer.Stop()
	a.ocrScan = false
	switch {
	case err != nil:
		a.report(err)
	case !produced:
		// Esc 取消与系统拒绝都是安静地结束：设计稿 §5，Esc 不提示。
		// A toast would repaint on its own; the silent end must too, or
		// the live progress capsule breathes one more beat (~400ms).
		ocrInvalidate(a)
	case text == "":
		a.toastStatus(a.appLoc().OcrNoText(), time.Now().Add(3*time.Second), statusInfo)
	default:
		// A capture whose text reached the pasteboard is one completed
		// 框选文字 for the 每周简报; Esc, a refusal, an empty capture and a
		// failed copy never count.
		r := a.copyOCRText(text)
		if r.copied {
			a.weeklyEvent(weekly.EventScreenText)
		}
		a.toastOCRCopy(text, r)
	}
}

// copyDroppedText is 识别文字并复制 for files dropped on the 框选文字
// card: the paths never become tray items — each is recognized off the
// main thread under the same Vision lock, and the verdict reuses
// finishScreenOCR's wording. A file that fails is skipped; a batch with
// nothing recognized reports the last error.
func (a *shelf) copyDroppedText(paths []string) {
	if a.ocrScan {
		// One round at a time, same as startScreenOCR: a drop landing
		// while a capture (or an earlier batch) is still recognizing
		// must not start a second round — the two would share ocrScan,
		// so the first to finish would kill the other's progress
		// capsule and re-arm a new box early.
		a.toastProgress(a.appLoc().OcrBusy(), time.Now().Add(3*time.Second))
		return
	}
	a.ocrScan = true
	a.ocrBusyAt = time.Now()
	a.ocrRunID++
	timer := ocrAfterFunc(ocrProgressDelay, func() {
		ocrInvalidate(a) // wakes the live progress capsule
	})
	ocrWork(func() {
		var parts []string
		var lastErr error
		for _, p := range paths {
			lines, err := ocrRecognize(p)
			if err != nil {
				lastErr = err
				continue
			}
			if text := joinOCRLines(lines); text != "" {
				parts = append(parts, text)
			}
		}
		err := lastErr
		if len(parts) > 0 {
			err = nil
		}
		text := strings.Join(parts, "\n")
		ocrDispatch(func() { a.finishScreenOCR(timer, text, true, err) })
	})
}

// ocrLiveStatus is the capsule's running-job line while a screen OCR is
// under way longer than ocrProgressDelay; before that the round usually
// ends before the words would help.
func (a *shelf) ocrLiveStatus(now time.Time) (string, any, bool) {
	if !a.ocrScan || now.Sub(a.ocrBusyAt) < ocrProgressDelay {
		return "", nil, false
	}
	return a.appLoc().OcrProgress(), a.ocrRunID, true
}

// ocrSheetStage is the permission card that replaces the current page's
// stage while ocrSheet is up (design §5): icon, title, the privacy line
// (only the picked region, on-device, no screenshot kept) plus the
// relaunch note — a grant only reaches CGPreflightScreenCaptureAccess on
// the next launch — and the two actions.
func (a *shelf) ocrSheetStage(c *ui.Context) {
	card := ui.Row(c.Key("ocr-sheet")).Margin(0, 0, 6, 0).Radius(14).Background(fill1).
		Padding(16, 16, 16, 16).Gap(14).AlignItems(ui.Start)
	card.Children(func() {
		chip := ui.Column(c.Key("ic")).Size(36, 36).Radius(10).Background(fill2).Center()
		chip.Children(func() {
			ui.Icon(c, ocrSymbol).Size(18, 18).TextColor(ink)
		})
		ui.Column(c.Key("txt")).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, loc(c).OcrPermissionTitle()).FontSize(13).FontWeight(600).TextColor(ink)
			ui.Row(c.Key("acts")).Gap(8).Margin(10, 0, 0, 0).Children(func() {
				primary := ui.ButtonBase(c.Key("grant")).Height(24).Radius(12).Padding(0, 10, 0, 10).
					Background(accent).Center().Label(loc(c).OcrOpenSettings())
				primary.Children(func() {
					ui.Text(c, loc(c).OcrOpenSettings()).SingleLine().FontSize(11).FontWeight(600).TextColor(ink)
				})
				if primary.Clicked() {
					ocrGrant()
				}
				cancel := ui.ButtonBase(c.Key("cancel")).Height(24).Radius(12).Padding(0, 10, 0, 10).
					Center().Label(loc(c).Cancel())
				if cancel.Hovered() {
					cancel.Background(fill2)
				} else {
					cancel.Background(fill1)
				}
				cancel.Children(func() {
					ui.Text(c, loc(c).Cancel()).SingleLine().FontSize(11).FontWeight(600).TextColor(ink)
				})
				if cancel.Clicked() {
					a.ocrSheet = false
					if a.win != nil {
						a.win.Invalidate()
					}
				}
			})
		})
	})
}

// ocrSymbol is the design's 框选文字 glyph: the four crop corners with the
// text lines inside (tools-v1.html `I.ocr`).
var ocrSymbol = symbol(`<path d="M4 8V5a1 1 0 0 1 1-1h3M16 4h3a1 1 0 0 1 1 1v3M20 16v3a1 1 0 0 1-1 1h-3M8 20H5a1 1 0 0 1-1-1v-3"/><path d="M8 10h8M8 14h5"/>`)
