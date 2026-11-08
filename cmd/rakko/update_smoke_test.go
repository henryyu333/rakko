//go:build darwin

package main

// Real-window shots of the update window, run only under
// RAKKO_BASKET_SMOKE=1 through the native gate. Three faces (found,
// downloading, ready) in both appearances: the window follows the system
// appearance, so forcing the app's appearance and reopening it is what the
// user sees when they flip the setting. Shots land in
// /tmp/Rakko-update-dialog/shots/native.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"

	"rakko/internal/mac"
	"rakko/internal/settings"
)

const updateShotDir = "/tmp/Rakko-update-dialog/shots/native"

// updateSmokeNotes is the fixture the real window renders: a CHANGELOG
// section's shape, not a file — the repository has no CHANGELOG.md yet.
const updateSmokeNotes = `### 新增
- 发现新版本时打开更新窗口，说明里写清这一版改了什么
- 托盘菜单直接安装更新，不再跳转浏览器

### 改进
- 面板展开的跟手感更好
- 剪贴板搜索支持拼音首字母

### 修复
- 修复拖动固定卡片时偶尔丢焦点的问题
- 修复浅色模式下分隔线过亮`

// TestUpdateWindowShots screenshots the real update window in three faces
// and both appearances. It also proves each face is a real window: the
// WindowServer knows its number and its bounds are the face's size.
func TestUpdateWindowShots(t *testing.T) {
	if os.Getenv("RAKKO_BASKET_SMOKE") != "1" {
		t.Skip("set RAKKO_BASKET_SMOKE=1 to run the native window smoke test")
	}
	a := smokeShelf
	if a == nil {
		t.Fatal("smoke shelf missing")
	}
	if err := os.MkdirAll(updateShotDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		onMain(func() {
			a.closeUpdateWindow()
			a.update = nil
			mygo.Theme.SetSource(mygo.ThemeSystem)
		})
	})
	// The body names the running version; a source build says "dev", so
	// the shots stand in for a packaged 1.13.6.
	var oldVer func() string
	onMain(func() {
		oldVer = appVersionProbe
		appVersionProbe = func() string { return "1.13.6" }
	})
	t.Cleanup(func() { onMain(func() { appVersionProbe = oldVer }) })

	notes := map[updateStage]string{
		updateStageFound: updateSmokeNotes,
	}
	faces := []struct {
		name  string
		stage updateStage
		tweak func()
	}{
		{"found", updateStageFound, nil},
		{"downloading", updateStageDownloading, func() {
			a.updateProgress = 0.42
			a.updateBytes, a.updateTotal = 7_000_000, 11_400_000
		}},
		{"ready", updateStageReady, nil},
	}
	for _, dark := range []bool{true, false} {
		appearance := mygo.ThemeLight
		appearanceName := "light"
		if dark {
			appearance, appearanceName = mygo.ThemeDark, "dark"
		}
		onMain(func() {
			mygo.Theme.SetSource(appearance)
			a.settings = settings.Default()
			a.locale = "zh-Hans"
			setAppLocale("zh-Hans")
		})
		// Let the appearance transition settle before the first frame.
		time.Sleep(600 * time.Millisecond)
		for _, f := range faces {
			stage := f.stage
			onMain(func() {
				a.closeUpdateWindow()
				a.update = &mygo.Update{Version: "1.14.0", Notes: notes[stage]}
				a.updateWinStage = stage
				if f.tweak != nil {
					f.tweak()
				}
				a.openUpdateWindow()
				a.updateSetStage(stage)
				if a.updateWin != nil {
					a.updateWin.Invalidate()
				}
			})
			time.Sleep(700 * time.Millisecond)
			onMain(func() {
				if a.updateWin != nil {
					a.updateWin.Invalidate()
				}
			})
			time.Sleep(250 * time.Millisecond)

			var num, wantW, wantH int
			var bounds mygo.Rectangle
			onMain(func() {
				if a.updateWin == nil {
					return
				}
				num = mac.WindowNumber(a.updateWin.NativeHandle())
				bounds = a.updateWin.Bounds()
				wantW, wantH = a.updateWinSize(stage)
			})
			if num == 0 {
				t.Fatalf("%s/%s: the window is not a real window", appearanceName, f.name)
			}
			if int(bounds.Width) != wantW || int(bounds.Height) != wantH {
				t.Fatalf("%s/%s: bounds = %dx%d, want %dx%d",
					appearanceName, f.name, int(bounds.Width), int(bounds.Height), wantW, wantH)
			}
			shot := filepath.Join(updateShotDir, fmt.Sprintf("%s-%s.png", f.name, appearanceName))
			out, err := exec.Command("screencapture", "-x", "-o", fmt.Sprintf("-l%d", num), shot).CombinedOutput()
			if err != nil {
				t.Fatalf("%s/%s: screencapture failed (%v): %s", appearanceName, f.name, err, out)
			}
			t.Logf("shot %s -> %s", f.name+"/"+appearanceName, shot)
		}
	}
}
