//go:build darwin

package main

// TestUIGallery renders Rakko's headlessly drawable interfaces and their key
// states to PNGs — one per language — for the local 界面总览 page. It runs
// only with RAKKO_GALLERY=<output dir> and writes <ID>-zh.png / <ID>-en.png
// plus items.json describing the set. Nothing here asserts behaviour; the
// only checks are that every entry rendered and that no file was empty.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"rakko/internal/agentnotify"
	"rakko/internal/aiusage"
	"rakko/internal/layout"
	"rakko/internal/lyrics"
	"rakko/internal/mac"
	"rakko/internal/note"
	"rakko/internal/settings"
	"rakko/internal/store"
	"rakko/locales"
)

// The gallery's backdrop: the light desktop the island smoke shots use, and
// a dark menu-bar strip above a notch shell so the ears stay readable.
var (
	galleryDesktop = color.RGBA{0xEC, 0xED, 0xEF, 0xFF}
	galleryMenuBar = color.RGBA{0x28, 0x28, 0x2B, 0xFF}
	galleryMenuH   = 32 // points of menu bar above a notch shell
)

const galleryScale = 2

// galleryExtra is an intermediate animation frame of an entry: the suffix
// goes into its file name (ID-t1-zh.png) and the picture is the tester's
// image at that moment.
type galleryExtra struct {
	suffix string
	img    *image.RGBA
}

// galleryRender is what one entry's build produces: the settled tester that
// becomes the entry's picture, plus any frozen intermediate frames.
type galleryRender struct {
	view   *ui.Tester
	extras []galleryExtra
}

// galleryItem is one row of the gallery: a stable id, its group and Chinese
// heading, a short state line, and the build.
type galleryItem struct {
	ID     string
	Group  string
	Title  string
	State  string
	Wide   bool // the picture is at least 600pt wide
	Notch  bool // composite under a menu bar strip
	Art    bool // needs the otter artwork; skipped (and marked) without it
	Build  func(t *testing.T, lang string) galleryRender
	frames []string // extra-frame suffixes, filled while rendering
}

type galleryMeta struct {
	ID      string            `json:"id"`
	Group   string            `json:"group"`
	Title   string            `json:"title"`
	State   string            `json:"state"`
	Files   map[string]string `json:"files,omitempty"`
	Frames  []string          `json:"frames"`
	Wide    bool              `json:"wide"`
	Missing string            `json:"missing"`
}

type galleryManifest struct {
	Items []*galleryMeta `json:"items"`
}

// TestUIGallery is the whole gallery. RAKKO_GALLERY names the output
// directory; elsewhere it skips so `go test ./cmd/rakko` stays fast.
func TestUIGallery(t *testing.T) {
	out := os.Getenv("RAKKO_GALLERY")
	if out == "" {
		t.Skip("set RAKKO_GALLERY=<dir> to write the UI gallery")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	art := galleryStageOtter(t)
	items := galleryItems()
	metas := make([]*galleryMeta, len(items))
	for i := range items {
		metas[i] = &galleryMeta{
			ID: items[i].ID, Group: items[i].Group, Title: items[i].Title,
			State: items[i].State, Wide: items[i].Wide, Files: map[string]string{},
			Frames: []string{},
		}
	}
	for _, lang := range []string{"zh-Hans", "en"} {
		short := galleryLangShort(lang)
		for i := range items {
			it := &items[i]
			meta := metas[i]
			if it.Art && !art {
				meta.Missing = "otter art"
				continue
			}
			if meta.Missing != "" {
				continue
			}
			render := it.Build(t, lang)
			if render.view == nil {
				t.Fatalf("%s: no view", it.ID)
			}
			name := fmt.Sprintf("%s-%s.png", it.ID, short)
			galleryWrite(t, out, name, render.view.Image(), it.Notch)
			meta.Files[short] = name
			for _, ex := range render.extras {
				frameName := fmt.Sprintf("%s-%s-%s.png", it.ID, ex.suffix, short)
				galleryWrite(t, out, frameName, ex.img, it.Notch)
				meta.Frames = append(meta.Frames, frameName)
			}
		}
	}
	data, err := json.MarshalIndent(galleryManifest{Items: metas}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "items.json"), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("gallery: %d entries in %s", len(items), out)
}

func galleryLangShort(lang string) string {
	if strings.HasPrefix(lang, "zh") {
		return "zh"
	}
	return "en"
}

// galleryStageOtter points the artwork loader at the resources folder the
// build ships, so the otter renders in the panel, drop zone, pet and
// settings. It reports whether a resting pose decoded.
func galleryStageOtter(t *testing.T) bool {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("..", "..", "resources", "otter"))
	if err != nil {
		return false
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) == 0 {
		return false
	}
	old := otterDir
	otterDir = func() string { return dir }
	t.Cleanup(func() { otterDir = old; resetOtters() })
	resetOtters()
	return petArt() != nil
}

// galleryWrite composites the transparent tester picture onto the desktop
// (and, for a notch shell, a menu-bar strip) and writes the PNG.
func galleryWrite(t *testing.T, dir, name string, src *image.RGBA, notch bool) {
	t.Helper()
	if src == nil {
		t.Fatalf("%s: nil image", name)
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), &image.Uniform{galleryDesktop}, image.Point{}, draw.Src)
	if notch {
		band := int(galleryMenuH * galleryScale)
		band = min(band, dst.Bounds().Dy())
		draw.Draw(dst, image.Rect(0, 0, dst.Bounds().Dx(), band),
			&image.Uniform{galleryMenuBar}, image.Point{}, draw.Src)
	}
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, dst); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// galleryView makes a tester for a view, at 2× and in the requested
// language, and settles its first two frames.
func galleryView(t *testing.T, lang string, view func(c *ui.Context), w, h int) *ui.Tester {
	t.Helper()
	v := ui.NewTester(view, w, h)
	v.SetLocale(lang)
	v.SetScale(galleryScale)
	v.Frame()
	v.Frame()
	return v
}

// galleryPanel renders a shelf's own view at 2× in a language and settles
// it. The shelf's app-level locale follows so strings measured outside the
// frame (result rows, notices) match the picture.
func galleryPanel(t *testing.T, lang string, a *shelf, w, h int) *ui.Tester {
	t.Helper()
	a.locale = lang
	v := ui.NewTester(a.view, w, h)
	v.SetLocale(lang)
	v.SetScale(galleryScale)
	v.Frame()
	v.Frame()
	return v
}

// gallerySettle advances a tester a few frames so a spring comes to rest.
func gallerySettle(v *ui.Tester) {
	for range 3 {
		v.Advance(400 * time.Millisecond)
		v.Frame()
	}
}

// galleryItems is the whole inventory, in the order the overview page lists
// it: groups A…M.
func galleryItems() []galleryItem {
	items := galleryGroupA()
	items = append(items, galleryGroupB()...)
	items = append(items, galleryGroupC()...)
	items = append(items, galleryGroupD()...)
	items = append(items, galleryGroupE()...)
	items = append(items, galleryGroupF()...)
	items = append(items, galleryGroupG()...)
	items = append(items, galleryGroupH()...)
	items = append(items, galleryGroupI()...)
	items = append(items, galleryGroupJ()...)
	items = append(items, galleryGroupK()...)
	items = append(items, galleryGroupL()...)
	items = append(items, galleryGroupM()...)
	items = append(items, galleryGroupU()...)
	items = append(items, galleryGroupN()...)
	items = append(items, galleryGroupV()...)
	return items
}

// --- V: agent approvals ---

// galleryQuote writes a string as a JSON literal for a fixture payload.
func galleryQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// galleryApproval is one approval card state on one shell. The fixture
// builds a request through the same analysis and queue the app uses.
func galleryApproval(kind string, island bool) func(t *testing.T, lang string) galleryRender {
	return func(t *testing.T, lang string) galleryRender {
		a := liveShelf()
		a.settings.Approval.Enabled = true
		a.island = island
		f := a.approvalFeature()
		f.enabled.Store(true)

		zh := lang != "en"
		desc := map[bool][3]string{
			true:  {"运行测试并生成覆盖率报告", "清理构建产物后重新编译", "清理构建产物"},
			false: {"Run the tests and write a coverage report", "Clean the build and compile the release", "Clean the build directory"},
		}[zh]
		d := func(which int) string { return desc[which] }
		tool, input := "claude", `{"command":"npm test -- --coverage","description":`+galleryQuote(d(0))+`}`
		switch kind {
		case "risky", "hold", "late", "queue":
			tool, input = "claude", `{"command":"rm -rf build && make release","description":`+galleryQuote(d(1))+`}`
		case "expanded":
			desc := "想修改 2 个文件"
			if !zh {
				desc = "Edit 2 files"
			}
			tool, input = "codex", `{"input":"*** Begin Patch\n*** Update File: cmd/rakko/agent_notify.go\n@@\n-old\n+new\n*** Update File: internal/agentnotify/socket.go\n@@\n-a\n+b\n+c\n*** End Patch","description":`+galleryQuote(desc)+`}`
		case "denied":
			tool, input = "claude", `{"command":"git clean -fdx","description":`+galleryQuote(d(2))+`}`
		case "expired":
			tool, input = "codex", `{"command":"npm publish --access public"}`
		}
		ap := testApproval(t, testID(900), tool, input)
		now := time.Now()
		switch kind {
		case "hold":
			ap.state = approvalHolding
			ap.holdFrom = now.Add(-380 * time.Millisecond)
		case "late":
			ap.at = now.Add(-33 * time.Second)
		case "expired":
			ap.state = approvalExpired
			ap.resultUntil = now.Add(time.Minute)
		case "denied":
			ap.state = approvalDenied
			ap.resultUntil = now.Add(time.Minute)
		case "expanded":
			ap.expanded = true
		}
		f.enqueue(ap)
		if kind == "queue" {
			second := testApproval(t, testID(901), "codex", `{"command":"npm test"}`)
			third := testApproval(t, testID(902), "cursor", `{"command":"docker compose down -v"}`)
			f.enqueue(second)
			f.enqueue(third)
		}
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		w, h := a.approvalCardSize(ap)
		winW := int(max(w+80, 400))
		return galleryRender{view: galleryPanel(t, lang, a, winW, int(h)+90)}
	}
}

func galleryGroupV() []galleryItem {
	const g = "Agent 审批"
	plain := galleryApproval("plain", false)
	return []galleryItem{
		{ID: "V1", Group: g, Title: "审批卡", State: "无风险 · 单击允许", Notch: true, Build: plain},
		{ID: "V2", Group: g, Title: "审批卡", State: "有风险 · 按住允许 63%", Notch: true, Build: galleryApproval("hold", false)},
		{ID: "V3", Group: g, Title: "审批卡", State: "已等 33 秒 · 警示色", Notch: true, Build: galleryApproval("late", false)},
		{ID: "V4", Group: g, Title: "审批卡", State: "已超时 · 交回终端", Notch: true, Build: galleryApproval("expired", false)},
		{ID: "V5", Group: g, Title: "审批卡", State: "已拒绝", Notch: true, Build: galleryApproval("denied", false)},
		{ID: "V6", Group: g, Title: "审批卡", State: "展开 · 文件列表与增删行", Notch: true, Build: galleryApproval("expanded", false)},
		{ID: "V7", Group: g, Title: "审批卡", State: "排队 · +2", Notch: true, Build: galleryApproval("queue", false)},
		{ID: "V8", Group: g, Title: "审批卡", State: "灵动岛 · 无风险", Build: galleryApproval("plain", true)},
		{ID: "V9", Group: g, Title: "审批卡", State: "灵动岛 · 有风险按住", Build: galleryApproval("hold", true)},
		{ID: "V10", Group: g, Title: "审批卡", State: "灵动岛 · 已等 33 秒", Build: galleryApproval("late", true)},
		{ID: "V11", Group: g, Title: "审批卡", State: "灵动岛 · 已超时", Build: galleryApproval("expired", true)},
		{ID: "V12", Group: g, Title: "审批卡", State: "灵动岛 · 已拒绝", Build: galleryApproval("denied", true)},
		{ID: "V13", Group: g, Title: "审批卡", State: "灵动岛 · 展开文件列表", Build: galleryApproval("expanded", true)},
		{ID: "V14", Group: g, Title: "审批卡", State: "灵动岛 · +2 排队", Build: galleryApproval("queue", true)},
	}
}

// --- A: the closed notch ---

func galleryGroupA() []galleryItem {
	const g = "刘海收起态与提示"
	return []galleryItem{
		{ID: "A1", Group: g, Title: "小箭头", State: "悬停 · 没有播放器", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 90)}
		}},
		{ID: "A2", Group: g, Title: "两翼", State: "播放中 · 封面与均衡器", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			fakeTrack(a, true)
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 90)}
		}},
		{ID: "A3", Group: g, Title: "两翼", State: "暂停", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			fakeTrack(a, false)
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 90)}
		}},
		{ID: "A4", Group: g, Title: "松开放入", State: "文件拖到刘海带上", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			a.dragging, a.dropHot = true, true
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 110)}
		}},
		{ID: "A5", Group: g, Title: "拖拽提示", State: "文件在菜单栏下方经过", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			a.dragging = true
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 80)}
		}},
		{ID: "A6", Group: g, Title: "松开放回", State: "固定卡片拖到刘海上", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			a.pinReturning = true
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 110)}
		}},
		{ID: "A7", Group: g, Title: "松开收进刘海", State: "桌宠拖到刘海上", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			a.petReturning = true
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 110)}
		}},
		{ID: "A8", Group: g, Title: "完成胶囊", State: "成功 · 绿色对勾", Notch: true, Build: galleryCapsule("已转换 3 项", "Converted 3 items", statusOK)},
		{ID: "A9", Group: g, Title: "完成胶囊", State: "失败 · 红色感叹号与一次抖动", Notch: true, Build: galleryCapsule("压缩失败 · 磁盘空间不足", "Compression failed · Not enough disk space", statusErr)},
		{ID: "A10", Group: g, Title: "完成胶囊", State: "信息 · 灰色圆点", Notch: true, Build: galleryCapsule("没有识别到文字", "No text found", statusInfo)},
		{ID: "A11", Group: g, Title: "完成胶囊", State: "进行中 · 呼吸的橙点", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionWindow()
			a.settings = settings.Default()
			a.conv = &converter{host: a, job: fixLiveJob()}
			a.visible, a.progress = false, 0
			v := galleryPanel(t, lang, a, sheetW, 90)
			grownCapsule(a, v)
			v.Frame()
			return galleryRender{view: v}
		}},
		{ID: "A12", Group: g, Title: "实时活动", State: "充电 · 80%", Notch: true, Build: galleryLive(func(lang string) liveEvent {
			l := galleryLoc(lang)
			return liveEvent{kind: liveCharging, label: l.LiveCharging(), pct: 80}
		})},
		{ID: "A13", Group: g, Title: "实时活动", State: "充满 · 100%", Notch: true, Build: galleryLive(func(lang string) liveEvent {
			l := galleryLoc(lang)
			return liveEvent{kind: liveCharged, label: l.LiveCharged(), pct: 100}
		})},
		{ID: "A14", Group: g, Title: "实时活动", State: "低电量 · 10%", Notch: true, Build: galleryLive(func(lang string) liveEvent {
			l := galleryLoc(lang)
			return liveEvent{kind: liveLow, label: l.LiveLow(), pct: 10}
		})},
		{ID: "A15", Group: g, Title: "实时活动", State: "下载进度 · 环形进度 42%", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := liveShelf()
			a.settings.LiveActivities.Downloads = true
			a.dl = &downloadsModule{host: a, items: []downloadFile{{name: "Xcode_16.2.xip", done: 4200, total: 10000}}}
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 90)}
		}},
		{ID: "A16", Group: g, Title: "实时活动", State: "下载未知 · 旋转弧与已收字节", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := liveShelf()
			a.settings.LiveActivities.Downloads = true
			a.dl = &downloadsModule{host: a, items: []downloadFile{{name: "movie.mp4", done: 84 * 1024 * 1024}}}
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 90)}
		}},
		{ID: "A17", Group: g, Title: "实时活动", State: "耳机 · 左 80% 右 78% 盒 50%", Notch: true, Build: galleryLive(func(lang string) liveEvent {
			return liveEvent{kind: liveHeadset, label: "AirPods Pro",
				levels: []batteryLevel{{part: batteryLeft, pct: 80}, {part: batteryRight, pct: 78}, {part: batteryCase, pct: 50}}}
		})},
		{ID: "A18", Group: g, Title: "实时活动", State: "耳机 · 已连接（没有电量）", Notch: true, Build: galleryLive(func(lang string) liveEvent {
			return liveEvent{kind: liveHeadset, label: "AirPods Pro"}
		})},
		{ID: "A19", Group: g, Title: "实时活动", State: "耳机 · 已断开", Notch: true, Build: galleryLive(func(lang string) liveEvent {
			return liveEvent{kind: liveHeadsetOff, label: "AirPods Pro"}
		})},
		{ID: "A20", Group: g, Title: "实时活动", State: "下载完成 · 文件名中途省略", Notch: true, Build: galleryLive(func(lang string) liveEvent {
			return liveEvent{kind: liveDownloadDone, label: "WWDC-2026-session-notes.pdf",
				paths: []string{"/tmp/WWDC-2026-session-notes.pdf"}}
		})},
		{ID: "A21", Group: g, Title: "Agent 完成卡片", State: "工具 + 任务两行", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := liveShelf()
			a.settings.AgentNotify.Enabled = true
			task := "整理发布说明并更新 CHANGELOG"
			if lang == "en" {
				task = "Write the release notes and update the CHANGELOG"
			}
			a.pushLive(liveEvent{kind: liveAgentDone, agent: "Codex", task: task, tool: agentnotify.ToolCodex}, liveHoldAgent, time.Now())
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW, 130)}
		}},
		{ID: "A22", Group: g, Title: "应用两翼", State: "固定 4 + 运行中 3 + 更多", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			a.settings = settings.Default()
			saved := []string{"com.apple.finder", "com.apple.Safari", "com.apple.Terminal", "com.apple.Notes"}
			a.settings.PinnedApps = &saved
			stubApps(t, &appsStub{
				running: []mac.RunningApp{
					{BundleID: "com.apple.finder", Name: "访达", PID: 101},
					{BundleID: "com.apple.Terminal", Name: "终端", PID: 103},
					{BundleID: "com.apple.dt.Xcode", Name: "Xcode", PID: 105},
					{BundleID: "com.tinyspeck.slackmacgap", Name: "Slack", PID: 106},
					{BundleID: "us.zoom.xos", Name: "zoom.us", PID: 107},
				},
				paths: map[string]string{"com.apple.Safari": "/Safari.app", "com.apple.Notes": "/Notes.app"},
				names: map[string]string{"com.apple.Safari": "Safari", "com.apple.Notes": "备忘录"},
			})
			a.dispWork = mygo.Rectangle{X: 0, Y: 0, Width: 2560, Height: 1415}
			a.dispBounds = a.dispWork
			a.apps.on = true
			a.apps.list = a.appSnapshot()
			a.refreshAppsDerived()
			a.apps.sel = 1
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, 800, 110)}
		}},
		{ID: "A23", Group: g, Title: "编辑常用应用", State: "更多浮层 · 管理列表", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionShelf()
			a.settings = settings.Default()
			saved := []string{"com.apple.finder", "com.apple.Safari"}
			a.settings.PinnedApps = &saved
			inst := []mygo.InstalledApp{
				{ID: "com.apple.finder", Name: "访达"},
				{ID: "com.apple.Safari", Name: "Safari"},
				{ID: "com.apple.dt.Xcode", Name: "Xcode"},
				{ID: "com.tinyspeck.slackmacgap", Name: "Slack"},
				{ID: "com.apple.Preview", Name: "预览"},
			}
			stubApps(t, &appsStub{
				running: []mac.RunningApp{
					{BundleID: "com.apple.finder", Name: "访达", PID: 101},
					{BundleID: "com.apple.Terminal", Name: "终端", PID: 103},
					{BundleID: "com.apple.dt.Xcode", Name: "Xcode", PID: 105},
					{BundleID: "com.tinyspeck.slackmacgap", Name: "Slack", PID: 106},
				},
				paths:     map[string]string{"com.apple.Safari": "/Safari.app"},
				names:     map[string]string{"com.apple.Safari": "Safari"},
				installed: inst,
			})
			a.notchX = 400 // the clamp pulls the popover inside the tester
			a.frame = mygo.Rectangle{X: 0, Y: 0, Width: 800, Height: 560}
			a.dispWork = mygo.Rectangle{X: 0, Y: 0, Width: 750, Height: 1415}
			a.dispBounds = a.dispWork
			a.apps.on = true
			a.apps.list = a.appSnapshot()
			a.refreshAppsDerived()
			a.apps.installed = inst
			a.apps.manageDirty = true
			a.apps.more, a.apps.manage = true, true
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, 800, 560)}
		}},
	}
}

// galleryLoc is the message catalog for a gallery language.
func galleryLoc(lang string) locales.Messages { return locales.In(lang) }

// galleryCapsule builds a capsule entry that posts a status, lets it grow on
// the fake clock, and freezes the settled frame.
func galleryCapsule(zh, en string, kind statusKind) func(t *testing.T, lang string) galleryRender {
	return func(t *testing.T, lang string) galleryRender {
		a := motionWindow()
		text := zh
		if lang == "en" {
			text = en
		}
		a.toastStatus(text, time.Now().Add(8*time.Second), kind)
		v := galleryPanel(t, lang, a, sheetW, 100)
		grownCapsule(a, v)
		v.Advance(time.Second)
		v.Frame()
		return galleryRender{view: v}
	}
}

// galleryLive builds a peek entry for one realtime activity.
func galleryLive(makeEvent func(lang string) liveEvent) func(t *testing.T, lang string) galleryRender {
	return func(t *testing.T, lang string) galleryRender {
		a := liveShelf()
		a.pushLive(makeEvent(lang), 5*time.Second, time.Now())
		a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
		return galleryRender{view: galleryPanel(t, lang, a, sheetW, 120)}
	}
}

// --- C: the home page ---

func galleryGroupC() []galleryItem {
	const g = "首页"
	home := func(lang string) homeShotContent { return homeShots[galleryLangShort(lang)] }
	player := func(paused, cover bool) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			c := home(lang)
			st := homeTrack()
			st.Title, st.Artist = c.title, c.artist
			st.Playing = !paused
			a, view, _ := homeShelf(t, &st)
			if cover {
				a.np.art, a.np.artColor = shotArt(), ui.Hex("#D86B9E")
			}
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}
	}
	lyrics := func(state string) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			c := home(lang)
			st := homeTrack()
			st.Title, st.Artist, st.Elapsed = c.title, c.artist, 10.2
			a, view, _ := homeShelfWith(t, &st, homeLyricsW+2*earR, func(a *shelf, p *homePage) {
				a.settings.SetLyrics(true)
				p.key = homeTrackKey(st)
				switch state {
				case "loading":
					p.fetched, p.loading = true, true
				case "none":
					p.fetched, p.none = true, true
				case "failed":
					p.fetched, p.failed, p.failedAt = true, true, time.Now()
				default:
					var b []string
					for i, line := range c.lyrics {
						b = append(b, "["+[]string{"00:00", "00:05", "00:10", "00:15", "00:20"}[i]+".00]"+line)
					}
					p.lines = lyrics.ParseLRC(stJoin(b, "\n"))
					p.fetched = true
				}
			})
			a.np.art, a.np.artColor = shotArt(), ui.Hex("#D86B9E")
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.SetSize(homeLyricsW+2*earR, panelTestH)
			view.Frame()
			view.Advance(700 * time.Millisecond)
			view.Frame()
			return galleryRender{view: view}
		}
	}
	return []galleryItem{
		{ID: "C1", Group: g, Title: "播放器", State: "有歌 · 播放中", Build: player(false, true)},
		{ID: "C2", Group: g, Title: "播放器", State: "暂停 · 播放按钮", Build: player(true, true)},
		{ID: "C3", Group: g, Title: "播放器", State: "有歌 · 没有封面", Build: player(true, false)},
		{ID: "C4", Group: g, Title: "歌词栏", State: "滚动中 · 当前行加粗", Wide: true, Build: lyrics("scroll")},
		{ID: "C5", Group: g, Title: "歌词栏", State: "正在找歌词…", Wide: true, Build: lyrics("loading")},
		{ID: "C6", Group: g, Title: "歌词栏", State: "没有找到这首歌的歌词", Wide: true, Build: lyrics("none")},
		{ID: "C7", Group: g, Title: "歌词栏", State: "歌词暂时取不到", Wide: true, Build: lyrics("failed")},
		{ID: "C8", Group: g, Title: "无音乐", State: "海獭站着 + 状态行（Agent 完成）", Art: true, Build: func(t *testing.T, lang string) galleryRender {
			c := home(lang)
			a, view, _ := homeShotAgent(t, c)
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}},
		{ID: "C9", Group: g, Title: "无音乐", State: "海獭睡着 · 十分钟没有动静", Art: true, Build: func(t *testing.T, lang string) galleryRender {
			a, view, _ := homeShelf(t, nil)
			a.lastAct = time.Now().Add(-15 * time.Minute)
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}},
		{ID: "C10", Group: g, Title: "无音乐", State: "没有状态行 · 只有日期与海獭", Art: true, Build: func(t *testing.T, lang string) galleryRender {
			a, view, _ := homeShelf(t, nil)
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}},
	}
}

// --- B: the search bar, results, capsule and pills ---

func galleryGroupB() []galleryItem {
	const g = "搜索条与结果"
	bar := func(query string, focus bool, scope mac.SearchScope) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			st := galleryEmptyStore(t)
			a := galleryShelf(st, t.TempDir())
			a.level, a.visible, a.progress = 1, true, 1
			a.homeQuery, a.focusMode, a.homeScope = query, focus, scope
			a.locale = lang
			w := homeW
			if query != "" {
				w = homeTypingW
			}
			return galleryRender{view: galleryPanel(t, lang, a, w+2*earR, homeShellH+navGap+navH+4)}
		}
	}
	results := func(prep func(t *testing.T, a *shelf, lang string)) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a := gallerySearchShelf(t, lang)
			if prep != nil {
				prep(t, a, lang)
			}
			a.locale = lang
			hits := a.homeHits()
			h := float32(homeShellH) + resultsGap + resultsHeight(hits) + navGap + navH + 4
			v := ui.NewTester(a.view, homeTypingW+2*earR, int(h))
			v.SetLocale(lang)
			v.SetScale(galleryScale)
			v.Frame()
			v.Frame()
			return galleryRender{view: v}
		}
	}
	nav := func(tab pageID, badge bool) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			a.visible, a.level, a.tab = true, 2, tab
			a.ensurePages()
			if badge {
				dir := t.TempDir()
				st := a.store
				for _, name := range []string{"设计稿-首页.png", "IMG_2043.HEIC", "2026 规划.pdf", "发布说明.md", "素材"} {
					p := filepath.Join(dir, name)
					if strings.Contains(name, ".") {
						os.WriteFile(p, testPNGBytes(t), 0o600)
					} else {
						os.MkdirAll(p, 0o755)
					}
					st.AddFiles([]string{p})
				}
			}
			w := int(a.navW())
			return galleryRender{view: galleryView(t, lang, func(c *ui.Context) {
				a.navView(c, ui.Rect{X: 4, Y: 4, W: float32(w), H: navH}, 1)
			}, w+8, navH+8)}
		}
	}
	update := func(kind string) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			stubSettingsSeams(t, false, nil, nil)
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			a.visible, a.level, a.tab, a.progress = true, 2, pageTray, 1
			a.update = &mygo.Update{Version: "1.14.0"}
			switch kind {
			case "installing":
				a.updateInstalling, a.updateProgress = true, 0.42
			case "restart":
				a.updateInstalled = true
			}
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}
	}
	toast := func(kind string) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			a.visible, a.level, a.tab, a.progress = true, 2, pageTray, 1
			a.settings.SetLyrics(false)
			now := time.Now()
			switch kind {
			case "ok":
				a.status, a.statusKind = galleryLoc(lang).ItemCopied(), statusOK
				a.statusUntil = now.Add(3 * time.Second)
			case "err":
				a.status, a.statusKind = galleryText(lang, "解压失败 · 文件已损坏", "Couldn't unzip · The archive is damaged"), statusErr
				a.statusUntil = now.Add(3 * time.Second)
			case "running":
				a.status, a.statusKind = galleryText(lang, "正在转成 JPEG · 2/5", "Converting to JPEG · 2/5"), statusInfo
				a.statusUntil = now.Add(3 * time.Second)
			case "undo":
				a.rn = &renameUI{host: a, undoUntil: now.Add(10 * time.Second),
					done: &renameDone{ops: []renameOp{{}}}} // count only: the pill says 2 项 below
				a.rn.done.ops = []renameOp{{}, {}}
			}
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}
	}
	return []galleryItem{
		{ID: "B1", Group: g, Title: "搜索条", State: "空 · 占位「搜索」", Notch: true, Build: bar("", false, mac.ScopeAll)},
		{ID: "B2", Group: g, Title: "搜索条", State: "输入中 · 加宽并显示光标", Notch: true, Build: bar("git merge --ff-only", true, mac.ScopeAll)},
		{ID: "B19", Group: g, Title: "搜索条", State: "应用模式 · 空框带 Tab 键帽", Notch: true, Build: bar("", false, mac.ScopeApps)},
		{ID: "B20", Group: g, Title: "搜索条", State: "文件模式 · 有输入无键帽", Notch: true, Build: bar("git merge --ff-only", true, mac.ScopeFiles)},
		{ID: "B3", Group: g, Title: "结果表", State: "各分组都有命中 · 选中一行", Build: results(func(t *testing.T, a *shelf, lang string) { a.homeSel = 1 })},
		{ID: "B4", Group: g, Title: "结果表", State: "悬停一行", Build: func(t *testing.T, lang string) galleryRender {
			r := results(nil)(t, lang)
			hits := r.view.Texts()
			_ = hits
			if rect, ok := r.view.Find(galleryText(lang, "设置页的间距改成 12pt", "Set the settings sheet padding to 12pt")); ok {
				r.view.Move(rect.X+rect.W/2, rect.Y+rect.H/2)
			}
			r.view.Frame()
			return r
		}},
		{ID: "B5", Group: g, Title: "结果表", State: "没有找到", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			a.level, a.visible, a.progress = 1, true, 1
			a.homeQuery = galleryText(lang, "没有这个", "nothing-here")
			a.homeSpot.query, a.homeSpot.done = a.homeQuery, true
			a.locale = lang
			h := float32(homeShellH) + resultsGap + resultsHeight(nil) + navGap + navH + 4
			return galleryRender{view: galleryPanel(t, lang, a, homeTypingW+2*earR, int(h))}
		}},
		{ID: "B6", Group: g, Title: "结果表", State: "只有便利贴命中", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			q := galleryText(lang, "发版", "release")
			a.homeQuery = q
			a.homeSpot.query, a.homeSpot.done = q, true
			writeGalleryNote(t, a, galleryText(lang,
				"# 发版清单\n– 改 README\n– 跑一遍回归测试\n",
				"# Release checklist\n– Update the README\n– Run the regression suite\n"))
			a.level, a.visible, a.progress = 1, true, 1
			a.locale = lang
			h := float32(homeShellH) + resultsGap + resultsHeight(a.homeHits()) + navGap + navH + 4
			return galleryRender{view: galleryPanel(t, lang, a, homeTypingW+2*earR, int(h))}
		}},
		{ID: "B7", Group: g, Title: "页面胶囊", State: "首页选中", Build: nav(pageHome, false)},
		{ID: "B8", Group: g, Title: "页面胶囊", State: "托盘选中 · 角标 5", Build: nav(pageTray, true)},
		{ID: "B9", Group: g, Title: "页面胶囊", State: "剪贴板选中", Build: nav(pageClipboard, false)},
		{ID: "B10", Group: g, Title: "页面胶囊", State: "工具选中", Build: nav(pageTools, false)},
		{ID: "B11", Group: g, Title: "页面胶囊", State: "AI 用量选中（功能已开启）", Build: func(t *testing.T, lang string) galleryRender {
			fx := newUsageFixture(t, true)
			a := fx.a
			a.visible, a.level, a.tab = true, 2, pageUsage
			a.locale = lang
			w := int(a.navW())
			return galleryRender{view: galleryView(t, lang, func(c *ui.Context) {
				a.navView(c, ui.Rect{X: 4, Y: 4, W: float32(w), H: navH}, 1)
			}, w+8, navH+8)}
		}},
		{ID: "B12", Group: g, Title: "更新提示条", State: "可更新 · v1.14.0", Build: update("available")},
		{ID: "B13", Group: g, Title: "更新提示条", State: "安装中 · 42%", Build: update("installing")},
		{ID: "B14", Group: g, Title: "更新提示条", State: "重启", Build: update("restart")},
		{ID: "B15", Group: g, Title: "底部提示条", State: "成功", Build: toast("ok")},
		{ID: "B16", Group: g, Title: "底部提示条", State: "出错 · 红点", Build: toast("err")},
		{ID: "B17", Group: g, Title: "底部提示条", State: "进行中", Build: toast("running")},
		{ID: "B18", Group: g, Title: "底部提示条", State: "改名撤销", Build: toast("undo")},
	}
}

// --- D: the tray page ---

func galleryGroupD() []galleryItem {
	const g = "托盘"
	page := func(n int, prep func(t *testing.T, a *shelf, lang string)) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			st := galleryEmptyStore(t)
			dir := t.TempDir()
			for i := range n {
				name := galleryTrayName(lang, i)
				p := filepath.Join(dir, name)
				col := color.RGBA{uint8(70 + i*17), uint8(120 + (i%4)*24), uint8(190 - i*5), 255}
				if strings.Contains(name, ".") && !strings.HasSuffix(name, "/") {
					os.WriteFile(p, gallerySolidPNG(col), 0o600)
				} else {
					os.MkdirAll(p, 0o755)
				}
				if err := st.AddFiles([]string{p}); err != nil {
					t.Fatal(err)
				}
			}
			a := galleryShelf(st, t.TempDir())
			a.tab, a.visible, a.level, a.progress = pageTray, true, 2, 1
			galleryFillThumbs(t, a, st)
			if prep != nil {
				prep(t, a, lang)
			}
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}
	}
	return []galleryItem{
		{ID: "D1", Group: g, Title: "托盘", State: "空", Build: page(0, nil)},
		{ID: "D2", Group: g, Title: "托盘", State: "少量 · 图片、PDF、文件夹", Build: page(4, nil)},
		{ID: "D3", Group: g, Title: "托盘", State: "满一屏 · 二十项", Build: page(20, nil)},
		{ID: "D4", Group: g, Title: "托盘", State: "带置顶 · 置顶项排在最前", Build: page(9, func(t *testing.T, a *shelf, lang string) {
			ids := []string{a.visibleItems()[2].ID}
			for _, it := range ids {
				if err := a.store.SetPinned(it, true); err != nil {
					t.Fatal(err)
				}
			}
		})},
		{ID: "D5", Group: g, Title: "托盘", State: "文件已移动 · 变暗并标「已移动」", Build: page(5, func(t *testing.T, a *shelf, lang string) {
			for i := range a.store.Items {
				if i == 1 {
					os.Remove(a.store.Items[i].Path)
				}
			}
			a.store.Refresh()
		})},
		{ID: "D6", Group: g, Title: "托盘", State: "转换格式角标 · JPG", Build: page(4, func(t *testing.T, a *shelf, lang string) {
			var it *store.Item
			for i := range a.store.Items {
				if strings.EqualFold(filepath.Ext(a.store.Items[i].Path), ".png") {
					it = &a.store.Items[i]
					break
				}
			}
			if it == nil {
				t.Fatal("no image item to mark as converted")
			}
			old := it.Path
			it.Path = strings.TrimSuffix(old, filepath.Ext(old)) + ".JPG"
			it.Converted = true
			if err := os.Rename(old, it.Path); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(it.Path); err == nil {
				if b, err := ui.DecodeBitmap(data); err == nil {
					a.thumbnails[it.ID] = b
				}
			}
		})},
		{ID: "D7", Group: g, Title: "托盘", State: "文字预览格", Build: func(t *testing.T, lang string) galleryRender {
			st := galleryEmptyStore(t)
			text := galleryText(lang,
				"周五发版前先跑一遍回归测试，再把 README 的截图换掉。",
				"Run the regression suite before Friday's release, then replace the README screenshots.")
			if err := st.AddText(text); err != nil {
				t.Fatal(err)
			}
			a := galleryShelf(st, t.TempDir())
			a.tab, a.visible, a.level, a.progress = pageTray, true, 2, 1
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}},
		{ID: "D8", Group: g, Title: "托盘", State: "多选 · 已选 3 项与清空按钮", Build: page(8, func(t *testing.T, a *shelf, lang string) {
			var ids []string
			for i, it := range a.visibleItems() {
				if i < 3 {
					ids = append(ids, it.ID)
				}
			}
			a.selection.onlyAll(ids)
		})},
		{ID: "D9", Group: g, Title: "托盘", State: "滚动到第三行", Build: func(t *testing.T, lang string) galleryRender {
			r := page(20, nil)(t, lang)
			r.view.Scroll(sheetW/2, 140, 0, 400)
			for range 4 {
				r.view.Advance(120 * time.Millisecond)
				r.view.Frame()
			}
			return r
		}},
	}
}

// --- E: the clipboard page ---

func galleryGroupE() []galleryItem {
	const g = "剪贴板"
	page := func(prep func(t *testing.T, st *store.Store, a *shelf, lang string), state string) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			st := galleryEmptyStore(t)
			a := galleryShelf(st, t.TempDir())
			a.appNames = map[string]string{
				"com.apple.Terminal": "Terminal", "com.tinyspeck.slackmacgap": "Slack",
				"com.apple.Safari": "Safari", "com.googlecode.iterm2": "iTerm2",
				"com.apple.Notes": "Notes",
			}
			if prep != nil {
				prep(t, st, a, lang)
			}
			// Dates that read like a real history rather than one instant.
			base := galleryTodayAt(0, 0).AddDate(0, 0, -1)
			for i := range st.Items {
				st.Items[i].CreatedAt = base
			}
			a.tab, a.visible, a.level, a.progress = pageClipboard, true, 2, 1
			a.locale = lang
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}
	}
	rows := func(t *testing.T, st *store.Store, a *shelf, lang string) {
		lines := []struct{ text, app string }{
			{galleryText(lang, "git rebase --onto fix f868c80", "git rebase --onto fix f868c80"), "com.apple.Terminal"},
			{galleryText(lang, "把首页卡片的间距改成 4pt，别动侧栏。", "Change the home cards' gap to 4pt, leave the sidebar alone."), "com.tinyspeck.slackmacgap"},
			{galleryText(lang, "https://github.com/egoist/rakko/releases/tag/v1.13.1", "https://github.com/egoist/rakko/releases/tag/v1.13.1"), "com.apple.Safari"},
			{galleryText(lang, "export PATH=\"$HOME/.local/bin:$PATH\"", "export PATH=\"$HOME/.local/bin:$PATH\""), "com.googlecode.iterm2"},
			{galleryText(lang, "设计评审：周三 15:00，会议室 A", "Design review: Wednesday 3pm, room A"), "com.apple.Notes"},
		}
		for i := len(lines) - 1; i >= 0; i-- {
			if err := st.AddClipText(lines[i].text, lines[i].app); err != nil {
				t.Fatal(err)
			}
		}
		// One screenshot and one 框选文字 capture, both older.
		if err := st.AddClipImage(gallerySolidPNG(color.RGBA{80, 160, 120, 255}), "com.apple.Safari"); err != nil {
			t.Fatal(err)
		}
		for _, item := range st.Items {
			if item.Kind == store.KindImage {
				if img := decodeThumb(item.Path); img != nil {
					a.thumbnails[item.ID] = ui.NewBitmap(img)
				}
			}
		}
		if err := st.AddClipText(galleryText(lang, "识别出来的两行字", "Two recognized lines"), clipSourceOCR); err != nil {
			t.Fatal(err)
		}
	}
	filtered := func(query string, focus bool) func(t *testing.T, lang string) galleryRender {
		return page(func(t *testing.T, st *store.Store, a *shelf, lang string) {
			rows(t, st, a, lang)
			a.search = query
			a.focusMode = focus
		}, "")
	}
	return []galleryItem{
		{ID: "E1", Group: g, Title: "剪贴板", State: "空", Build: page(nil, "")},
		{ID: "E2", Group: g, Title: "剪贴板", State: "多行 · 截图与框选文字来源", Build: page(rows, "")},
		{ID: "E3", Group: g, Title: "剪贴板", State: "置顶在前", Build: page(func(t *testing.T, st *store.Store, a *shelf, lang string) {
			rows(t, st, a, lang)
			if err := st.SetPinned(st.Items[len(st.Items)-3].ID, true); err != nil {
				t.Fatal(err)
			}
		}, "")},
		{ID: "E4", Group: g, Title: "剪贴板", State: "搜索中 · 命中并选中", Build: filtered("git", true)},
		{ID: "E5", Group: g, Title: "剪贴板", State: "搜索无结果", Build: filtered("没有这段文字", true)},
		{ID: "E6", Group: g, Title: "剪贴板", State: "键盘提示条 · ↑↓ 选择 ⏎ 粘贴", Build: func(t *testing.T, lang string) galleryRender {
			r := filtered(galleryText(lang, "改成", "Change"), true)(t, lang)
			if len(r.view.Texts()) > 0 {
				r.view.Frame()
			}
			return r
		}},
	}
}

// --- shared fixtures ---

func galleryText(lang, zh, en string) string {
	if lang == "en" {
		return en
	}
	return zh
}

// galleryTodayAt is a stable today-time for the update rows.
func galleryTodayAt(h, m int) time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
}

func galleryEmptyStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// galleryShelf is a bare shelf with the bookkeeping the panel expects.
func galleryShelf(st *store.Store, dataDir string) *shelf {
	return &shelf{
		store: st, dataDir: dataDir, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{},
		appNames: map[string]string{},
	}
}

// gallerySolidPNG is a small opaque PNG in one colour, for thumbnails.
func gallerySolidPNG(col color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 80, 80))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = col.R, col.G, col.B, 0xFF
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// galleryFillThumbs decodes the stored image files into the shelf's
// thumbnail cache, as the live app's background loader would.
func galleryFillThumbs(t *testing.T, a *shelf, st *store.Store) {
	t.Helper()
	for _, it := range st.Items {
		if it.Kind != store.KindImage && !imageExts[strings.ToLower(filepath.Ext(it.Path))] {
			continue
		}
		data, err := os.ReadFile(it.Path)
		if err != nil {
			continue
		}
		if b, err := ui.DecodeBitmap(data); err == nil {
			a.thumbnails[it.ID] = b
		}
	}
}

// galleryTrayName is the nth file's realistic name.
func galleryTrayName(lang string, i int) string {
	zh := []string{
		"设计稿-首页.png", "2026 规划.pdf", "发布说明.md", "素材",
		"IMG_2043.HEIC", "截图 2026-11-03 09.12.45.png", "合同扫描件.pdf",
		"旅行照片 01.jpg", "会议记录.txt", "账单-十月.pdf", "产品原型.fig",
		"屏幕录制 2026-10-30.mov", "图标导出", "logo-深色.png", "定价表.xlsx",
		"周报-第 45 周.pdf", "参考图 02.png", "录音 14.32.m4a", "简历.pdf", "旧版备份",
	}
	en := []string{
		"design-home.png", "2026-plan.pdf", "release-notes.md", "Assets",
		"IMG_2043.HEIC", "Screenshot 2026-11-03 at 09.12.45.png", "contract-scan.pdf",
		"trip-01.jpg", "meeting-notes.txt", "invoice-october.pdf", "product.fig",
		"screen-recording-2026-10-30.mov", "icon-export", "logo-dark.png", "pricing.xlsx",
		"weekly-report-45.pdf", "reference-02.png", "recording-14.32.m4a", "resume.pdf", "old-backup",
	}
	names := zh
	if lang == "en" {
		names = en
	}
	return names[i%len(names)]
}

// writeGalleryNote writes one note file into the shelf's notes folder.
func writeGalleryNote(t *testing.T, a *shelf, text string) {
	t.Helper()
	dir := filepath.Join(a.dataDir, note.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if err := os.WriteFile(filepath.Join(dir, id+note.FileExt), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gallerySearchShelf is the home search with every group hit by one query.
func gallerySearchShelf(t *testing.T, lang string) *shelf {
	t.Helper()
	st := galleryEmptyStore(t)
	dataDir := t.TempDir()
	dir := t.TempDir()
	names := []string{"设计稿-首页.png", "设计方案-v3.pdf"}
	query := "设"
	if lang == "en" {
		names = []string{"settings-export.png", "setup-notes.md"}
		query = "set"
	}
	for i, name := range names {
		p := filepath.Join(dir, name)
		os.WriteFile(p, gallerySolidPNG(color.RGBA{uint8(90 + i*60), 120, 190, 255}), 0o600)
		if err := st.AddFiles([]string{p}); err != nil {
			t.Fatal(err)
		}
	}
	clip := galleryText(lang, "设置页的间距改成 12pt", "Set the settings sheet padding to 12pt")
	if err := st.AddClipText(clip, "com.apple.Terminal"); err != nil {
		t.Fatal(err)
	}
	a := galleryShelf(st, dataDir)
	a.level, a.visible, a.progress = 1, true, 1
	a.homeQuery = query
	a.appNames["com.apple.Terminal"] = "Terminal"
	writeGalleryNote(t, a, galleryText(lang, "# 设计评审\n– 首页卡片对齐 4pt 网格\n", "# Settings review\n– Align the cards to the 4pt grid\n"))
	app, folder, file := "设计工具", "设计素材", "设计规范.pdf"
	if lang == "en" {
		app, folder, file = "Setapp", "Settings Backup", "setup-notes.md"
	}
	a.homeSpot.query, a.homeSpot.done = query, true
	a.homeSpot.items = []mac.SpotlightHit{
		{Path: "/Applications/" + app + ".app", Name: app, App: true},
		{Path: "/Users/x/Documents/" + folder, Name: folder, Folder: true},
		{Path: "/Users/x/Documents/" + file, Name: file},
	}
	base := galleryTodayAt(0, 0).AddDate(0, 0, -1)
	for i := range st.Items {
		st.Items[i].CreatedAt = base
	}
	galleryFillThumbs(t, a, st)
	a.spotIcons = make(map[string]*ui.Bitmap)
	for _, item := range st.Items {
		if imageExts[strings.ToLower(filepath.Ext(item.Path))] {
			a.spotIcons[item.Path] = a.thumbnails[item.ID]
		}
	}
	return a
}

func galleryOtterView(pose string, h int) func(t *testing.T, lang string) galleryRender {
	return func(t *testing.T, lang string) galleryRender {
		b := otterImage(pose)
		if b == nil {
			t.Fatalf("no otter art for %s", pose)
		}
		bw, bh := b.Size()
		w := h * bw / bh
		if w < 1 {
			w = h
		}
		return galleryRender{view: galleryView(t, lang, func(c *ui.Context) {
			c.Root().Background(ui.Transparent)
			ui.Row(c.Key("pet-art")).Fill().Center().Children(func() {
				ui.Image(c.Key("art"), b).Size(float32(w), float32(h)).Fit(ui.Contain)
			})
		}, w+16, h+16)}
	}
}

// --- F: the tools page ---

func galleryGroupF() []galleryItem {
	const g = "工具页"
	card := func(id string, span, rows int) []layout.Card {
		return []layout.Card{{ID: id, Span: span, Rows: rows}}
	}
	tools := func(cards []layout.Card, tweak func(t *testing.T, a *shelf, p *toolsPage, lang string)) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			st := galleryEmptyStore(t)
			dir := t.TempDir()
			// Recent files and clipboard history for the cards that show them.
			for i, name := range []string{galleryTrayName(lang, 0), galleryTrayName(lang, 1), galleryTrayName(lang, 3)} {
				p := filepath.Join(dir, name)
				if strings.Contains(name, ".") {
					os.WriteFile(p, gallerySolidPNG(color.RGBA{uint8(80 + i*50), 130, 200, 255}), 0o600)
				} else {
					os.MkdirAll(p, 0o755)
				}
				if err := st.AddFiles([]string{p}); err != nil {
					t.Fatal(err)
				}
			}
			for _, s := range []string{
				galleryText(lang, "git rebase --onto fix f868c80", "git rebase --onto fix f868c80"),
				galleryText(lang, "把首页卡片的间距改成 4pt", "Change the home cards' gap to 4pt"),
			} {
				if err := st.AddClipText(s, "com.apple.Terminal"); err != nil {
					t.Fatal(err)
				}
			}
			a := galleryShelf(st, dir)
			galleryFillThumbs(t, a, st)
			a.settings.AIUsage.Enabled = true
			a.tab, a.visible, a.level, a.progress = pageTools, true, 2, 1
			a.toolsLay = &layout.Layout{Version: layout.Version, Cards: cards}
			fakeTrack(a, true)
			now := time.Now()
			a.awake = &awakeModule{host: a, id: mac.PowerAssertionID(1), duration: time.Hour,
				until: now.Add(42 * time.Minute), now: func() time.Time { return now }}
			attachGalleryUsage(a, now)
			noteText := galleryText(lang,
				"# 周五发版前\n– 改 README\n– 跑回归\n", "# Before Friday's release\n– Update the README\n– Run the regression suite\n")
			writeGalleryNote(t, a, noteText)
			if n := a.noteSession(); n != nil {
				n.text, n.draft = noteText, noteText
			}
			a.ensurePages()
			if tweak != nil {
				tweak(t, a, a.toolsPage(), lang)
			}
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}
	}
	busy := func(t *testing.T, a *shelf, p *toolsPage, lang string) {
		a.conv = &converter{host: a, job: fixLiveJob()}
	}
	drag := func(class dropClass, names ...string) func(t *testing.T, a *shelf, p *toolsPage, lang string) {
		return func(t *testing.T, a *shelf, p *toolsPage, lang string) {
			dir := t.TempDir()
			var paths []string
			for _, name := range names {
				path := filepath.Join(dir, name)
				os.WriteFile(path, gallerySolidPNG(color.RGBA{120, 160, 90, 255}), 0o600)
				paths = append(paths, path)
			}
			a.dragging, a.dragOpenTools = true, true
			a.drop = dropZoneState{files: paths, class: class, loaded: true}
		}
	}
	def := layout.DefaultWith(true).Cards
	return []galleryItem{
		{ID: "F1", Group: g, Title: "工具页", State: "默认网格", Build: tools(def, nil)},
		{ID: "F2", Group: g, Title: "音乐卡片", State: "2×2 · 完整播放器", Build: tools(card("playing", 2, 2), nil)},
		{ID: "F3", Group: g, Title: "音乐卡片", State: "2×1 · 横向条", Build: tools(card("playing", 2, 1), nil)},
		{ID: "F4", Group: g, Title: "音乐卡片", State: "1×1 · 封面与状态", Build: tools(card("playing", 1, 1), nil)},
		{ID: "F5", Group: g, Title: "保持唤醒", State: "1×1 · 剩余 42 分钟", Build: tools(card("awake", 1, 1), nil)},
		{ID: "F6", Group: g, Title: "最近文件", State: "2×1", Build: tools(card("recent", 2, 1), nil)},
		{ID: "F7", Group: g, Title: "剪贴板最新", State: "2×1", Build: tools(card("clip", 2, 1), nil)},
		{ID: "F8", Group: g, Title: "便利贴", State: "2×1 · 预览三行", Build: tools(card("note", 2, 1), nil)},
		{ID: "F9", Group: g, Title: "便利贴", State: "1×1 · 图标与名称", Build: tools(card("note", 1, 1), nil)},
		{ID: "F10", Group: g, Title: "AI 用量", State: "1×1", Build: tools(card("usage", 1, 1), nil)},
		{ID: "F11", Group: g, Title: "AI 用量", State: "2×1 · 双环与剩余额度", Build: tools(card("usage", 2, 1), nil)},
		{ID: "F12", Group: g, Title: "工具页", State: "编辑模式 · ⊖、尺寸手柄与虚线添加格", Build: tools(func() []layout.Card {
			var out []layout.Card
			for _, c := range def {
				if c.ID != "zip" {
					out = append(out, c)
				}
			}
			return out
		}(), func(t *testing.T, a *shelf, p *toolsPage, lang string) {
			p.editing, p.editAt = true, time.Now()
		})},
		{ID: "F13", Group: g, Title: "工具页", State: "卡片目录 · 添加卡片", Build: tools(def, func(t *testing.T, a *shelf, p *toolsPage, lang string) {
			p.picking = true
		})},
		{ID: "F14", Group: g, Title: "工具页", State: "拖入时 · 动作格高亮可接受", Build: tools(def, drag(classImages, "封面.png", "照片 02.png"))},
		{ID: "F15", Group: g, Title: "工具页", State: "拖入时 · 不接受的动作格变暗", Build: tools(def, drag(classArchives, "素材备份.zip"))},
		{ID: "F16", Group: g, Title: "工具页", State: "转换中忙碌 · 仅点击被拒，界面无变化", Build: tools(def, busy)},
	}
}

// attachGalleryUsage gives a shelf a working AI 用量 card with two quota
// answers, without registering the page.
func attachGalleryUsage(a *shelf, now time.Time) {
	a.usage = &usageFeature{host: a, deps: usageDeps{
		now:         func() time.Time { return now },
		fetchClaude: func(context.Context) aiusage.Quota { return aiusage.Quota{} },
		fetchCodex:  func(context.Context) aiusage.Quota { return aiusage.Quota{} },
	}}
	a.usage.quota[quotaIndex(aiusage.ToolCodex)] = usageQuotaState{latest: okQuota(aiusage.ToolCodex, "Plus", now)}
	a.usage.quota[quotaIndex(aiusage.ToolClaude)] = usageQuotaState{latest: windowUsed(okQuota(aiusage.ToolClaude, "Pro", now), 0, 38)}
}

// --- G: the AI usage page ---

func galleryGroupG() []galleryItem {
	const g = "AI 用量页"
	page := func(setup func(t *testing.T, fx *usageFixture)) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			fx := newUsageFixture(t, true)
			fx.a.locale = lang
			fx.view.SetLocale(lang)
			fx.view.SetScale(galleryScale)
			fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
			fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
			fx.scan.summaries = usageShotSummaries(*fx.now)
			if setup != nil {
				setup(t, fx)
			}
			fx.a.tab = pageUsage
			fx.f.tick(*fx.now)
			fx.view.Frame()
			fx.view.Frame()
			return galleryRender{view: fx.view}
		}
	}
	return []galleryItem{
		{ID: "G1", Group: g, Title: "AI 用量", State: "正常 · 两张额度卡与本机统计", Build: page(nil)},
		{ID: "G2", Group: g, Title: "AI 用量", State: "过期变暗 · 网络不通", Build: func(t *testing.T, lang string) galleryRender {
			fx := newUsageFixture(t, true)
			fx.a.locale = lang
			fx.view.SetLocale(lang)
			fx.view.SetScale(galleryScale)
			fx.scan.summaries = usageShotSummaries(*fx.now)
			fx.codex.qs = []aiusage.Quota{
				okQuota(aiusage.ToolCodex, "Plus", *fx.now),
				{Tool: aiusage.ToolCodex, Status: aiusage.QuotaFailed, FetchedAt: fx.now.Add(5 * time.Minute)},
			}
			fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
			fx.a.tab = pageUsage
			fx.f.tick(*fx.now)
			*fx.now = fx.now.Add(5 * time.Minute)
			fx.f.tick(*fx.now)
			fx.view.Frame()
			fx.view.Frame()
			return galleryRender{view: fx.view}
		}},
		{ID: "G3", Group: g, Title: "AI 用量", State: "额度紧张变红 · 5 小时已用 94%", Build: page(func(t *testing.T, fx *usageFixture) {
			fx.claude.q = windowUsed(okQuota(aiusage.ToolClaude, "Pro", *fx.now), 0, 94)
		})},
		{ID: "G4", Group: g, Title: "AI 用量", State: "节奏预警 · 1:42 后用完", Build: page(func(t *testing.T, fx *usageFixture) {
			fx.f.deps.pace = func(w aiusage.Window, now time.Time) aiusage.PaceInfo {
				return aiusage.PaceInfo{ExpectedLeftPct: 60, AheadPct: 17, RunsOutIn: time.Hour + 42*time.Minute, LastsUntilReset: false}
			}
		})},
		{ID: "G5", Group: g, Title: "AI 用量", State: "错误文案 · 未登录 / 登录已过期", Build: page(func(t *testing.T, fx *usageFixture) {
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaNotLoggedIn, FetchedAt: *fx.now}
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaExpired, FetchedAt: *fx.now}
		})},
		{ID: "G6", Group: g, Title: "AI 用量", State: "首次统计进度", Build: func(t *testing.T, lang string) galleryRender {
			fx := newUsageFixture(t, true)
			stubUsageSeams(t, true)
			fx.a.locale = lang
			fx.view.SetLocale(lang)
			fx.view.SetScale(galleryScale)
			fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
			fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
			fx.a.tab = pageUsage
			fx.f.tick(*fx.now)
			fx.view.Frame()
			fx.view.Frame()
			return galleryRender{view: fx.view}
		}},
		{ID: "G7", Group: g, Title: "AI 用量", State: "本机记录读取出错", Build: page(func(t *testing.T, fx *usageFixture) {
			fx.scan.err = errors.New("permission denied")
		})},
		{ID: "G8", Group: g, Title: "AI 用量", State: "悬停详情 · 计划与各窗口", Build: func(t *testing.T, lang string) galleryRender {
			r := page(func(t *testing.T, fx *usageFixture) {
				q := okQuota(aiusage.ToolClaude, "Pro", *fx.now)
				q.Windows = append(q.Windows,
					aiusage.Window{Name: "7 days · Opus", Model: "opus", UsedPct: 37, Length: 7 * 24 * time.Hour},
					aiusage.Window{Name: "7 days · Sonnet", Model: "sonnet", UsedPct: 2, Length: 7 * 24 * time.Hour})
				fx.claude.q = q
			})(t, lang)
			r.view.Move(usageLeftW/2, 2*usageCardH+usageCardGap+usageCardH/2)
			r.view.Frame()
			r.view.Frame()
			return r
		}},
		{ID: "G9", Group: g, Title: "AI 用量", State: "Claude 未登录 · 登录按钮", Build: page(func(t *testing.T, fx *usageFixture) {
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn,
				FetchedAt: *fx.now}
		})},
		{ID: "G10", Group: g, Title: "AI 用量", State: "Claude 登录中 · 在浏览器中完成登录", Build: page(func(t *testing.T, fx *usageFixture) {
			fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNotLoggedIn,
				FetchedAt: *fx.now}
			fx.f.claudeLogin = usageClaudeLogin{busy: true, cancel: func() {}}
		})},
	}
}

// --- H: the drop zone and the catching otter ---

func galleryGroupH() []galleryItem {
	const g = "投放与接住"
	files := func(t *testing.T, names ...string) []string {
		dir := t.TempDir()
		var out []string
		for _, name := range names {
			p := filepath.Join(dir, name)
			switch {
			case strings.HasSuffix(name, ".pdf"):
				os.WriteFile(p, []byte("%PDF-1.7\n% fake\n"), 0o600)
			case strings.HasSuffix(name, ".zip"):
				os.WriteFile(p, []byte("PK\x03\x04 fake archive"), 0o600)
			case strings.HasSuffix(name, ".txt"):
				os.WriteFile(p, []byte("notes"), 0o600)
			default:
				os.WriteFile(p, gallerySolidPNG(color.RGBA{110, 160, 210, 255}), 0o600)
			}
			out = append(out, p)
		}
		return out
	}
	zone := func(prep func(t *testing.T, a *shelf), paths func(t *testing.T, lang string) []string) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a, _ := dropViewShelf(t, paths(t, lang))
			a.settings = settings.Default()
			if prep != nil {
				prep(t, a)
			}
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}
	}
	otter := func(hot, caught bool) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a, _ := dropViewShelf(t, files(t, galleryText(lang, "封面.png", "cover.png")))
			a.settings = settings.Default()
			a.dropHot = hot
			if caught {
				a.dragging = false
				a.win = &mygo.Window{}
				a.drop.catchAt = time.Now()
				a.drop.files = files(t, galleryText(lang, "封面.png", "cover.png"))
				a.drop.class = classImages
				a.drop.loaded = true
			}
			a.locale = lang
			v := ui.NewTester(a.view, sheetW+2*earR, panelTestH)
			v.SetLocale(lang)
			v.SetScale(galleryScale)
			v.Frame()
			r := galleryRender{view: v}
			if caught {
				for i, d := range []time.Duration{70 * time.Millisecond, 80 * time.Millisecond, 220 * time.Millisecond} {
					v.Advance(d)
					v.Frame()
					r.extras = append(r.extras, galleryExtra{suffix: fmt.Sprintf("t%d", i+1), img: v.Image()})
				}
			}
			return r
		}
	}
	return []galleryItem{
		{ID: "H1", Group: g, Title: "投放区", State: "图片 · 压缩到 1 MB / 合成 PDF / 转 JPEG", Build: zone(nil, func(t *testing.T, lang string) []string {
			return files(t, galleryText(lang, "封面.png", "cover.png"), galleryText(lang, "照片 02.png", "photo-02.png"), galleryText(lang, "截图.png", "screenshot.png"))
		})},
		{ID: "H2", Group: g, Title: "投放区", State: "PDF · 转 Word / 页面转图片 / 打成 zip", Build: zone(nil, func(t *testing.T, lang string) []string {
			return files(t, galleryText(lang, "合同.pdf", "contract.pdf"), galleryText(lang, "报价单.pdf", "quote.pdf"))
		})},
		{ID: "H3", Group: g, Title: "投放区", State: "压缩包 · 解压", Build: zone(nil, func(t *testing.T, lang string) []string {
			return files(t, galleryText(lang, "素材备份.zip", "assets-backup.zip"))
		})},
		{ID: "H4", Group: g, Title: "投放区", State: "混合 · 打成 zip 与隔空投送", Build: zone(nil, func(t *testing.T, lang string) []string {
			return files(t, galleryText(lang, "封面.png", "cover.png"), galleryText(lang, "说明.pdf", "readme.pdf"), galleryText(lang, "素材.zip", "assets.zip"))
		})},
		{ID: "H5", Group: g, Title: "投放区", State: "转换忙碌 · 动作格变灰", Build: zone(func(t *testing.T, a *shelf) {
			a.conv = &converter{host: a, job: fixLiveJob()}
		}, func(t *testing.T, lang string) []string {
			return files(t, galleryText(lang, "封面.png", "cover.png"))
		})},
		{ID: "H6", Group: g, Title: "接住", State: "海獭等待 · 从行下探头", Art: true, Build: otter(false, false)},
		{ID: "H7", Group: g, Title: "接住", State: "海獭接住 · 文件飞入怀里（含中间帧）", Art: true, Build: otter(false, true)},
	}
}

// --- I: other panels ---

func galleryGroupI() []galleryItem {
	const g = "其他面板"
	return []galleryItem{
		{ID: "I1", Group: g, Title: "框选文字权限卡", State: "没有屏幕录制权限", Build: func(t *testing.T, lang string) galleryRender {
			ocrSeams(t)
			ocrPreflight = func() bool { return false }
			a := galleryShelf(nil, t.TempDir())
			a.startScreenOCR()
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}},
		{ID: "I2", Group: g, Title: "排列窗口", State: "有辅助功能权限 · 六个区域", Build: func(t *testing.T, lang string) galleryRender {
			a := snapTestShelf()
			a.visible, a.snap.panel, a.snap.drawn, a.snap.trusted = true, true, true, true
			a.snap.hot = -1
			return galleryRender{view: galleryPanel(t, lang, a, snapW, snapH)}
		}},
		{ID: "I3", Group: g, Title: "排列窗口", State: "没有辅助功能权限 · 区域变淡与开启", Build: func(t *testing.T, lang string) galleryRender {
			a := snapTestShelf()
			a.visible, a.snap.panel, a.snap.drawn, a.snap.trusted = true, true, true, false
			a.snap.hot = -1
			return galleryRender{view: galleryPanel(t, lang, a, snapW, snapH)}
		}},
		{ID: "I4", Group: g, Title: "排列预览框", State: "目标区域 · 16% 填充与 2pt 描边", Build: func(t *testing.T, lang string) galleryRender {
			return galleryRender{view: galleryView(t, lang, snapGhostView, 320, 190)}
		}},
		{ID: "I5", Group: g, Title: "截图", State: "选择窗口 · 高亮与放大镜", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			s := galleryShotSession(t, lang, 900, 600)
			s.cursor = mygo.Point{X: 400, Y: 300}
			s.hover = mygo.Rectangle{X: 120, Y: 90, Width: 420, Height: 300}
			s.hoverOK = true
			return galleryRender{view: galleryView(t, lang, func(c *ui.Context) { s.overlayView(c, 0) }, 900, 600)}
		}},
		{ID: "I6", Group: g, Title: "截图", State: "选区 · 标注、属性栏与工具栏", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			s := galleryShotSession(t, lang, 900, 600)
			s.cursor = mygo.Point{X: 520, Y: 350}
			s.hasSel = true
			s.sel = mygo.Rectangle{X: 250, Y: 150, Width: 420, Height: 260}
			s.tool = shotToolRect
			s.color = shotAnnotationColors[0]
			word := "截图说明"
			if lang != "zh-Hans" {
				word = "Screenshot note"
			}
			cells := map[[2]int]shotMosaicCell{}
			var mosaic []shotMosaicCell
			shotMosaicCells(s.displays[0].img, s.displays[0].shotTarget, shotMosaicBlockPixels(4), shotMosaicBrushRadius(4),
				mygo.Point{X: 300, Y: 378}, mygo.Point{X: 600, Y: 402},
				func(col, row int, cell shotMosaicCell) {
					if cells[[2]int{col, row}].rect.Width == 0 {
						cells[[2]int{col, row}] = cell
						mosaic = append(mosaic, cell)
					}
				})
			s.hist.annots = []shotAnnotation{
				{tool: shotToolRect, color: shotAnnotationColors[0], width: 4,
					a: mygo.Point{X: 280, Y: 190}, b: mygo.Point{X: 430, Y: 270}},
				{tool: shotToolArrow, color: shotAnnotationColors[1], width: 4,
					a: mygo.Point{X: 450, Y: 200}, b: mygo.Point{X: 620, Y: 270}},
				{tool: shotToolPen, color: shotAnnotationColors[3], width: 4,
					points: []mygo.Point{{X: 300, Y: 350}, {X: 340, Y: 330}, {X: 380, Y: 360}}},
				{tool: shotToolText, color: shotAnnotationColors[4], width: 4,
					a: mygo.Point{X: 290, Y: 300}, text: word},
				{tool: shotToolMosaic, color: shotAnnotationColors[0], width: 4, cells: mosaic},
				{tool: shotToolNumber, color: shotAnnotationColors[2], width: 2,
					a: mygo.Point{X: 630, Y: 380}, number: 1},
			}
			s.layoutBar()
			return galleryRender{view: galleryView(t, lang, func(c *ui.Context) { s.overlayView(c, 0) }, 900, 600)}
		}},
		{ID: "I7", Group: g, Title: "截图贴图", State: "桌面浮窗 · 双击或 Esc 关闭", Build: func(t *testing.T, lang string) galleryRender {
			img := galleryShotImage(360, 240)
			p := &shotPin{bitmap: ui.NewBitmap(img), pixW: 360, pixH: 240}
			return galleryRender{view: galleryView(t, lang, p.view, 360, 240)}
		}},
		{ID: "I8", Group: g, Title: "截图文字框", State: "多行编辑 · 左右把手 · 自动长高", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			s := galleryShotSession(t, lang, 900, 600)
			s.cursor = mygo.Point{X: 520, Y: 300}
			s.hasSel = true
			s.sel = mygo.Rectangle{X: 220, Y: 120, Width: 460, Height: 320}
			s.tool = shotToolText
			s.color = shotAnnotationColors[4]
			s.width = shotAnnotationWidths[1]
			text := "把首页卡片的间距改成 4pt，\n第二行继续折行显示。"
			if lang != "zh-Hans" {
				text = "Change the home cards' gap to 4pt,\nand let this second line wrap too."
			}
			s.text = &shotTextEdit{
				at: mygo.Point{X: 270, Y: 170}, width: 240,
				text: text, color: s.color, stroke: s.width, focus: true,
			}
			s.layoutBar()
			return galleryRender{view: galleryView(t, lang, func(c *ui.Context) { s.overlayView(c, 0) }, 900, 600)}
		}},
		{ID: "I9", Group: g, Title: "截图文字", State: "完成后 · 按框宽折行且没有边框", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			s := galleryShotSession(t, lang, 900, 600)
			s.cursor = mygo.Point{X: 760, Y: 520}
			s.hasSel = true
			s.sel = mygo.Rectangle{X: 220, Y: 120, Width: 460, Height: 320}
			s.tool = shotToolNone
			text := "截图里的多行文字\n按框宽自动折行，导出后没有边框。"
			if lang != "zh-Hans" {
				text = "Multi-line text in a screenshot\nwraps to its box and exports without a frame."
			}
			s.hist.annots = []shotAnnotation{
				{tool: shotToolText, color: shotAnnotationColors[4], width: 4,
					a: mygo.Point{X: 270, Y: 170}, boxW: 220, text: text},
				{tool: shotToolText, color: shotAnnotationColors[1], width: 7,
					a: mygo.Point{X: 270, Y: 280}, boxW: 260, text: "Second size\n24pt wraps too"},
			}
			s.layoutBar()
			return galleryRender{view: galleryView(t, lang, func(c *ui.Context) { s.overlayView(c, 0) }, 900, 600)}
		}},
	}
}

// galleryShotSession is an overlay over a synthetic desktop, with no
// window and no real capture.
func galleryShotSession(t *testing.T, lang string, w, h int) *shotSession {
	t.Helper()
	s := shotTestSession(t, mygo.Rectangle{Width: w, Height: h}, w, h)
	s.a.locale = lang
	img := galleryShotImage(w, h)
	s.displays[0].img = img
	s.displays[0].bitmap = ui.NewBitmap(img)
	s.cursorDpy = 0
	return s
}

// galleryShotImage is the wallpaper and a couple of windows the overlay
// screenshots: enough to read the veil, the selection and the highlight.
func galleryShotImage(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*img.Stride + x*4
			img.Pix[i] = uint8(32 + 70*x/max(w, 1))
			img.Pix[i+1] = uint8(52 + 50*y/max(h, 1))
			img.Pix[i+2] = uint8(96 + 60*x/max(w, 1))
			img.Pix[i+3] = 0xFF
		}
	}
	win := func(r image.Rectangle, top color.RGBA) {
		draw.Draw(img, r, &image.Uniform{color.RGBA{236, 238, 242, 255}}, image.Point{}, draw.Src)
		draw.Draw(img, image.Rect(r.Min.X, r.Min.Y, r.Max.X, r.Min.Y+28), &image.Uniform{top}, image.Point{}, draw.Src)
	}
	win(image.Rect(120, 90, 540, 390), color.RGBA{60, 66, 78, 255})
	win(image.Rect(500, 220, 800, 470), color.RGBA{80, 72, 90, 255})
	return img
}

// --- J: desktop things ---

func galleryGroupJ() []galleryItem {
	const g = "桌面上的东西"
	pin := func(id string, span, rows, w int) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			a.settings = settings.Default()
			fakeTrack(a, true)
			switch id {
			case "recent":
				dir := t.TempDir()
				for i := range 3 {
					p := filepath.Join(dir, galleryTrayName(lang, i))
					os.WriteFile(p, gallerySolidPNG(color.RGBA{uint8(70 + i*60), 140, 200, 255}), 0o600)
					a.store.AddFiles([]string{p})
				}
			case "clip":
				a.store.AddClipText(galleryText(lang, "把首页卡片的间距改成 4pt", "Change the home cards' gap to 4pt"), "com.apple.Terminal")
			case "note":
				if n := a.noteSession(); n != nil {
					n.text = galleryText(lang, "# 周五发版前\n– 改 README\n– 跑一遍回归", "# Before Friday\n– Update the README\n– Run the regression suite")
					n.draft = n.text
				}
			}
			it := a.pinRegister(&pinItem{
				cardID: id, span: span, rows: rows, cardW: w, display: 1, edge: pinFree, expanded: true,
				frame: mygo.Rectangle{Width: w, Height: pinCardH},
			})
			return galleryRender{view: galleryView(t, lang, func(c *ui.Context) { it.view(c) }, w, pinCardH)}
		}
	}
	note := func(zh, en string, locked bool) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			n := a.noteSession()
			if n == nil {
				t.Fatal("no note session")
			}
			t.Cleanup(func() {
				if n.watch != nil {
					n.watch.Close()
				}
			})
			text := galleryText(lang, zh, en)
			n.text, n.draft, n.locked = text, text, locked
			return galleryRender{view: galleryView(t, lang, a.noteView, noteW, noteH)}
		}
	}
	basket := func(state string) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			a.basket.shown = true
			switch state {
			case "dropped":
				a.basket.dropped = true
			case "failed":
				a.basket.failed = true
				a.basket.failMsg = galleryText(lang, "磁盘空间不足", "Not enough disk space")
			}
			return galleryRender{view: galleryView(t, lang, a.basketView, basketWidth, basketHeight)}
		}
	}
	rename := func(mode renameMode, a1, a2 string) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			stubSettingsSeams(t, false, nil, nil)
			a, view, _ := renameShelf(t, galleryText(lang, "IMG_2043.png", "IMG_2043.png"),
				galleryText(lang, "IMG_2044.png", "IMG_2044.png"), galleryText(lang, "IMG_2045.png", "IMG_2045.png"))
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			a.locale = lang
			r := openHeadlessRename(t, a)
			r.win = &mygo.Window{}
			selectAllTray(t, a)
			r.mode = int(mode)
			switch mode {
			case modeReplace:
				r.find, r.replace = a1, a2
			case modeAdd:
				r.addText, r.addSeg = a1, 1
			case modeFormat:
				r.fmtName, r.fmtStartText, r.digitsSeg, r.posSeg = a1, "1", 0, 1
			}
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}
	}
	conflict := func(t *testing.T, lang string) galleryRender {
		stubSettingsSeams(t, false, nil, nil)
		a, view, _ := renameShelf(t, "a.txt", "b.txt")
		view.SetLocale(lang)
		view.SetScale(galleryScale)
		a.locale = lang
		r := openHeadlessRename(t, a)
		r.win = &mygo.Window{}
		selectAllTray(t, a)
		r.find, r.replace = "a", "b"
		view.Frame()
		view.Frame()
		return galleryRender{view: view}
	}
	return []galleryItem{
		{ID: "J1", Group: g, Title: "固定卡片", State: "136 · 打成 zip", Build: pin("zip", 1, 1, pinCardW)},
		{ID: "J2", Group: g, Title: "固定卡片", State: "278 · 音乐播放器", Build: pin("playing", 2, 2, pinCardW2)},
		{ID: "J3", Group: g, Title: "固定卡片", State: "278 · 便利贴预览", Build: pin("note", 2, 1, pinCardW2)},
		{ID: "J4", Group: g, Title: "固定卡片", State: "边条收起 · 8 × 56", Build: func(t *testing.T, lang string) galleryRender {
			a := galleryShelf(galleryEmptyStore(t), t.TempDir())
			it := a.pinRegister(&pinItem{cardID: "zip", span: 1, rows: 1, cardW: pinCardW, display: 1, edge: pinLeft,
				expanded: false, frame: mygo.Rectangle{Width: pinStripW, Height: pinStripH}})
			return galleryRender{view: galleryView(t, lang, it.view, pinStripW, pinStripH)}
		}},
		{ID: "J5", Group: g, Title: "便利贴窗口", State: "空", Build: note("", "", false)},
		{ID: "J6", Group: g, Title: "便利贴窗口", State: "有内容", Build: note(
			"# 周五发版前\n– 改 README\n– 跑一遍回归测试\n– 更新截图\n\n发版后在群里说一声。",
			"# Before Friday's release\n– Update the README\n– Run the regression suite\n– Replace the screenshots\n\nSay something in the channel afterwards.", false)},
		{ID: "J7", Group: g, Title: "便利贴窗口", State: "锁定 · 文件读不出来", Build: note("# 周五发版前", "# Before Friday", true)},
		{ID: "J8", Group: g, Title: "桌宠", State: "站立 · 中号 96pt", Art: true, Build: galleryOtterView(petIdle, 96)},
		{ID: "J9", Group: g, Title: "桌宠", State: "小号 72pt", Art: true, Build: galleryOtterView(petIdle, 72)},
		{ID: "J10", Group: g, Title: "桌宠", State: "大号 128pt", Art: true, Build: galleryOtterView(petIdle, 128)},
		{ID: "J11", Group: g, Title: "桌宠", State: "睡着 · 十分钟没有动静", Art: true, Build: galleryOtterView(petSleep, 96)},
		{ID: "J12", Group: g, Title: "桌宠", State: "开心 · 刚接住东西", Art: true, Build: galleryOtterView(petHappy, 96)},
		{ID: "J13", Group: g, Title: "桌宠", State: "受惊 · 有拖拽经过", Art: true, Build: galleryOtterView(petStartled, 96)},
		{ID: "J14", Group: g, Title: "桌宠", State: "被拿起", Art: true, Build: galleryOtterView(petLifted, 96)},
		{ID: "J15", Group: g, Title: "桌宠", State: "等待 · 投放区下方", Art: true, Build: galleryOtterView(petWait, 96)},
		{ID: "J16", Group: g, Title: "桌宠", State: "准备 · 举起前爪", Art: true, Build: galleryOtterView(petReady, 96)},
		{ID: "J17", Group: g, Title: "桌宠", State: "接住 · 抱着文件", Art: true, Build: galleryOtterView(petCaught, 96)},
		{ID: "J18", Group: g, Title: "篮子", State: "空 · 放到这里", Build: basket("idle")},
		{ID: "J19", Group: g, Title: "篮子", State: "已放入 Rakko", Build: basket("dropped")},
		{ID: "J20", Group: g, Title: "篮子", State: "失败 · 磁盘空间不足", Build: basket("failed")},
		{ID: "J21", Group: g, Title: "改名窗口", State: "替换", Wide: false, Build: rename(modeReplace, "IMG_", "DSC_")},
		{ID: "J22", Group: g, Title: "改名窗口", State: "添加 · 名称后", Build: rename(modeAdd, "-2026", "")},
		{ID: "J23", Group: g, Title: "改名窗口", State: "格式 · 名称 + 序号", Build: rename(modeFormat, "Trip", "")},
		{ID: "J24", Group: g, Title: "改名窗口", State: "冲突 · 目标名称已存在", Build: conflict},
	}
}

// --- K: the settings window ---

func galleryGroupK() []galleryItem {
	const g = "设置窗口"
	page := func(p settingsPageID, tweak func(t *testing.T, a *shelf)) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			stubSettingsSeams(t, false, nil, nil)
			stubKeyboard(t)
			stubPermissions(t)
			a, view := newSettingsShelf(t)
			a.settingsPage = p
			if tweak != nil {
				tweak(t, a)
			}
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}
	}
	return []galleryItem{
		{ID: "K1", Group: g, Title: "通用", State: "登录项、语言、默认搜索模式", Wide: true, Build: page(settingsPageGeneral, nil)},
		{ID: "K16", Group: g, Title: "通用", State: "默认搜索模式 · 应用", Wide: true, Build: page(settingsPageGeneral, func(t *testing.T, a *shelf) {
			a.settings.SearchMode = settings.SearchModeApps
		})},
		{ID: "K2", Group: g, Title: "外形与提示", State: "灵动岛、桌宠、实时活动、贴边排列", Wide: true, Build: page(settingsPageNotch, nil)},
		{ID: "K3", Group: g, Title: "托盘", State: "保留时长与拖出后移除", Wide: true, Build: page(settingsPageTray, nil)},
		{ID: "K4", Group: g, Title: "剪贴板", State: "条数上限与排除的应用", Wide: true, Build: page(settingsPageClipboard, func(t *testing.T, a *shelf) {
			a.settings.ExcludedApps = []string{"com.apple.Terminal", "com.apple.Safari", "com.apple.Notes"}
			a.appNames = map[string]string{
				"com.apple.Terminal": "Terminal", "com.apple.Safari": "Safari",
				"com.apple.Notes": "Notes",
			}
		})},
		{ID: "K5", Group: g, Title: "AI", State: "额度开关与 Agent 完成提醒", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			stubSettingsSeams(t, false, nil, nil)
			a, home, _ := agentShelf(t)
			agentInstall(t, home, agentnotify.ToolClaude, agentnotify.ToolCodex, agentnotify.ToolGemini)
			a.setAgentNotify(true)
			a.setAgentNotifyTool(agentnotify.ToolGemini, false)
			now := time.Now()
			a.settings.AIUsage.Enabled = true
			attachGalleryUsage(a, now)
			a.settingsPage = settingsPageAIUsage
			a.locale = lang
			view := ui.NewTester(a.settingsView, settingsWidth, settingsHeight)
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			for range 20 {
				if r, ok := view.Find("Gemini CLI"); ok && r.W > 0 && r.H > 0 && r.Y >= 0 && r.Y+r.H <= settingsHeight {
					break
				}
				view.Scroll(settingsWidth/2, settingsHeight/2, 0, 140)
				view.Frame()
			}
			view.Frame()
			return galleryRender{view: view}
		}},
		{ID: "K6", Group: g, Title: "快捷键", State: "四个全局组合与面板只读键", Wide: true, Build: page(settingsPageShortcuts, nil)},
		{ID: "K7", Group: g, Title: "系统权限", State: "三项权限与一个开关", Wide: true, Build: page(settingsPagePermissions, nil)},
		{ID: "K8", Group: g, Title: "关于", State: "版本、更新、隐私与源码", Wide: true, Build: page(settingsPageAbout, func(t *testing.T, a *shelf) {
			a.settings.AutoCheckUpdates = true
			a.updateLastCheck = galleryTodayAt(9, 12)
			a.updateLastResult = updateResultLatest
		})},
		{ID: "K9", Group: g, Title: "快捷键", State: "录制中 · 按下新的组合键…", Wide: true, Build: page(settingsPageShortcuts, func(t *testing.T, a *shelf) {
			a.settingsRecording = shortcutToggle
		})},
		{ID: "K10", Group: g, Title: "快捷键", State: "已被占用", Wide: true, Build: page(settingsPageShortcuts, func(t *testing.T, a *shelf) {
			a.shortcutBusy = map[shortcutKind]bool{shortcutToggle: true}
		})},
		{ID: "K9b", Group: g, Title: "快捷键", State: "确认 · 单独按键", Wide: true, Build: page(settingsPageShortcuts, func(t *testing.T, a *shelf) {
			a.settingsRecording = shortcutToggle
			a.shortcutConfirm = &shortcutConfirmation{kind: shortcutToggle, acc: "Q"}
		})},
		{ID: "K11", Group: g, Title: "系统权限", State: "缺 2 项 · 还差辅助功能与屏幕录制", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			stubSettingsSeams(t, false, nil, nil)
			st, _ := stubPermissions(t)
			st.fda, st.ax, st.screen = true, false, false
			a, view := newSettingsShelf(t)
			a.settingsPage = settingsPagePermissions
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}},
		{ID: "K12", Group: g, Title: "系统权限", State: "全部开启", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			stubSettingsSeams(t, false, nil, nil)
			st, _ := stubPermissions(t)
			st.fda, st.ax, st.screen = true, true, true
			a, view := newSettingsShelf(t)
			a.settingsPage = settingsPagePermissions
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}},
		{ID: "K13", Group: g, Title: "系统权限", State: "引导第 1 项 · 可停止", Wide: true, Build: func(t *testing.T, lang string) galleryRender {
			stubSettingsSeams(t, false, nil, nil)
			stubKeyboard(t)
			stubPermissions(t)
			oldOpen := openSettingsWindow
			openSettingsWindow = func(*shelf) {}
			t.Cleanup(func() { openSettingsWindow = oldOpen })
			a, view := newSettingsShelf(t)
			a.settingsPage = settingsPagePermissions
			a.showPermissions()
			a.locale = lang
			view.SetLocale(lang)
			view.SetScale(galleryScale)
			view.Frame()
			view.Frame()
			return galleryRender{view: view}
		}},
		{ID: "K14", Group: g, Title: "通用", State: "更新可用 · v1.14.0", Wide: true, Build: page(settingsPageGeneral, func(t *testing.T, a *shelf) {
			a.update = &mygo.Update{Version: "1.14.0"}
			a.updateLastCheck = galleryTodayAt(10, 5)
			a.updateLastResult = updateResultAvailable
			a.updateLastVersion = "1.14.0"
		})},
		{ID: "K15", Group: g, Title: "通用", State: "设置文件损坏 · 横幅与锁定", Wide: true, Build: page(settingsPageGeneral, func(t *testing.T, a *shelf) {
			a.settingsBroken = true
		})},
	}
}

// --- L: the island shell ---

func galleryGroupL() []galleryItem {
	const g = "灵动岛样式"
	island := func() *shelf { return islandShelf(true, settings.ShellStyleIsland) }
	return []galleryItem{
		{ID: "L1", Group: g, Title: "灵动岛", State: "收起胶囊 · 空闲 120 × 30", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := island()
			a.win = &mygo.Window{}
			a.visible, a.progress = false, 0
			a.locale = lang
			v := ui.NewTester(a.view, 220, 110)
			v.SetLocale(lang)
			v.SetScale(galleryScale)
			v.Frame()
			gallerySettle(v)
			return galleryRender{view: v}
		}},
		{ID: "L2", Group: g, Title: "灵动岛", State: "两翼 · 顶部胶囊变宽", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := island()
			fakeTrack(a, true)
			a.peeking, a.peekProgress, a.peekFrom = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, 340, 100)}
		}},
		{ID: "L3", Group: g, Title: "灵动岛", State: "搜索条", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := island()
			a.visible, a.level, a.progress = true, 1, 1
			return galleryRender{view: galleryPanel(t, lang, a, homeW+40, 172)}
		}},
		{ID: "L4", Group: g, Title: "灵动岛", State: "页面", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := island()
			a.visible, a.level, a.progress, a.tab = true, 2, 1, pageHome
			st := homeTrack()
			if c, ok := homeShots[galleryLangShort(lang)]; ok {
				st.Title, st.Artist = c.title, c.artist
			}
			a.np = &nowPlayingModule{host: a, have: true, last: st}
			a.np.art, a.np.artColor = shotArt(), ui.Hex("#D86B9E")
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+40, 350)}
		}},
		{ID: "L5", Group: g, Title: "灵动岛", State: "完成胶囊 · 36 高整颗", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := island()
			a.win = &mygo.Window{}
			a.visible, a.progress = false, 0
			text := galleryText(lang, "已转换 3 项", "Converted 3 items")
			a.toastStatus(text, time.Now().Add(8*time.Second), statusOK)
			a.locale = lang
			v := ui.NewTester(a.view, 460, 120)
			v.SetLocale(lang)
			v.SetScale(galleryScale)
			grownCapsule(a, v)
			v.Frame()
			return galleryRender{view: v}
		}},
		{ID: "L6", Group: g, Title: "灵动岛", State: "其他显示器上的空闲胶囊", Build: func(t *testing.T, lang string) galleryRender {
			return galleryRender{view: galleryView(t, lang, idleIslandView, islandIdleW, islandIdleH)}
		}},
	}
}

// --- M: Reduce Motion ---

func galleryGroupM() []galleryItem {
	const g = "减少动态效果"
	return []galleryItem{
		{ID: "M1", Group: g, Title: "减少动态效果", State: "面板打开 · 直接到最终尺寸，无过冲", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionWindow()
			a.settings = settings.Default()
			a.locale = lang
			v := ui.NewTester(a.view, sheetW+2*earR, panelTestH)
			v.SetLocale(lang)
			v.SetScale(galleryScale)
			v.SetPreferences(ui.Preferences{ReduceMotion: true})
			a.visible = true
			a.progress, a.animationFrom, a.animationStart = 1, 1, time.Time{}
			v.Frame()
			return galleryRender{view: v}
		}},
		{ID: "M2", Group: g, Title: "减少动态效果", State: "完成胶囊 · 直接出现", Notch: true, Build: func(t *testing.T, lang string) galleryRender {
			a := motionWindow()
			a.settings = settings.Default()
			text := galleryText(lang, "已转换 3 项", "Converted 3 items")
			a.toastStatus(text, time.Now().Add(8*time.Second), statusOK)
			a.locale = lang
			v := ui.NewTester(a.view, sheetW, 100)
			v.SetLocale(lang)
			v.SetScale(galleryScale)
			v.SetPreferences(ui.Preferences{ReduceMotion: true})
			v.Frame()
			v.Frame()
			return galleryRender{view: v}
		}},
	}
}

// --- U: the update window ---

// galleryUpdateNotes is a release-notes body shaped like the CHANGELOG's:
// ### section names and bulleted items, long enough to scroll.
func galleryUpdateNotes(lang string) string {
	if strings.HasPrefix(lang, "zh") {
		return `### 新增
- 发现新版本时打开更新窗口，说明里写清这一版改了什么
- 托盘菜单直接安装更新，不再跳转浏览器
- 说明可以滚动，长版本也不会被截断

### 改进
- 面板展开的跟手感更好
- 剪贴板搜索支持拼音首字母
- 下载进度按字节显示，不再只给一个百分比

### 修复
- 修复拖动固定卡片时偶尔丢焦点的问题
- 修复浅色模式下分隔线过亮
- 修复更新检查超时后按钮一直不可用`
	}
	return `### New
- An update window shows what changed, with skip and remind later
- Install from the tray menu without opening a browser
- Notes scroll, so a long release is never cut off

### Improved
- The panel follows the pointer more closely
- Clipboard search matches pinyin initials
- The download reports bytes, not just a percentage

### Fixed
- Losing focus while dragging a pinned card
- Separators too bright in light mode
- The check button staying disabled after a timeout`
}

// galleryStageUpdateIcon points the update window's hero image at the
// shipped icon, so the gallery draws what the bundle draws.
func galleryStageUpdateIcon(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "resources", "icon.png"))
	if err != nil {
		return
	}
	b, err := ui.DecodeBitmap(data)
	if err != nil {
		return
	}
	// Mark the lazy load done first: bundleIcon would otherwise retry the
	// bundle path and overwrite this with nil.
	appIconOnce.Do(func() {})
	old := appIconBitmap
	appIconBitmap = b
	t.Cleanup(func() { appIconBitmap = old })
}

// galleryUpdateFace renders one face of the update window at its own size,
// in one appearance and language. tweak runs before the first frame.
func galleryUpdateFace(t *testing.T, lang string, dark bool, stage updateStage, tweak func(*shelf)) galleryRender {
	t.Helper()
	galleryStageUpdateIcon(t)
	oldVer := appVersionProbe
	appVersionProbe = func() string { return "1.13.0" }
	t.Cleanup(func() { appVersionProbe = oldVer })
	a := updateShelf()
	a.dataDir = t.TempDir()
	a.update = &mygo.Update{Version: "1.14.0", Notes: galleryUpdateNotes(lang)}
	a.updateWinStage = stage
	a.updateLastCheck = galleryTodayAt(10, 5)
	a.updateLastResult = updateResultAvailable
	a.updateLastVersion = "1.14.0"
	if tweak != nil {
		tweak(a)
	}
	a.locale = lang
	w, h := a.updateWinSize(stage)
	v := ui.NewTester(a.updateView, w, h)
	v.SetLocale(lang)
	v.SetScale(galleryScale)
	v.SetDark(dark)
	v.Frame()
	v.Frame()
	return galleryRender{view: v}
}

func galleryGroupU() []galleryItem {
	const g = "更新窗口"
	faces := []struct {
		id, title, state string
		stage            updateStage
		tweak            func(*shelf)
	}{
		{"1", "发现新版本", "说明、复选框与三个动作", updateStageFound, nil},
		{"2", "正在下载更新", "进度 42% 与字节", updateStageDownloading, func(a *shelf) {
			a.updateProgress = 0.42
			a.updateBytes, a.updateTotal = 7_000_000, 11_400_000
		}},
		{"3", "可以开始安装了", "安装并重启应用", updateStageReady, nil},
		{"4", "已是最新版本", "版本与检查时间", updateStageLatest, nil},
		{"5", "检查更新失败", "原因与重试", updateStageFailed, nil},
	}
	var items []galleryItem
	for _, dark := range []bool{true, false} {
		for _, f := range faces {
			id := "U" + f.id
			state := "深色 · " + f.state
			if !dark {
				id += "L"
				state = "浅色 · " + f.state
			}
			stage, tweak := f.stage, f.tweak
			items = append(items, galleryItem{
				ID: id, Group: g, Title: f.title, State: state,
				Build: func(t *testing.T, lang string) galleryRender {
					return galleryUpdateFace(t, lang, dark, stage, tweak)
				},
			})
		}
	}
	return items
}

// --- N: the first-launch guide ---

func galleryGroupN() []galleryItem {
	const g = "首次启动引导"
	build := func(origin runOrigin, step int, tweak func(t *testing.T, o *onboardingFeature)) func(t *testing.T, lang string) galleryRender {
		return func(t *testing.T, lang string) galleryRender {
			galleryStageUpdateIcon(t)
			a := motionShelf()
			a.settings = settings.Default()
			a.settings.Onboarding = settings.OnboardingSettings{Pending: true}
			a.visible = true
			o := &onboardingFeature{host: a, origin: origin}
			o.step = min(max(step, 0), len(o.steps())-1)
			if tweak != nil {
				tweak(t, o)
			}
			a.onb = o
			a.registerPage(o)
			a.tab, a.tabFrom = pageOnboarding, pageOnboarding
			return galleryRender{view: galleryPanel(t, lang, a, sheetW+2*earR, panelTestH)}
		}
	}
	downloads := originDownloads
	return []galleryItem{
		{ID: "N1", Group: g, Title: "第一步", State: "住在刘海 · 中性水獭与快捷键", Build: build(originInstalled, 0, nil)},
		{ID: "N2", Group: g, Title: "第二步", State: "从「下载」移入「应用程序」", Build: build(downloads, 1, nil)},
		{ID: "N3", Group: g, Title: "第二步", State: "从磁盘映像移入 · 只读源", Build: build(originDiskImage, 1, nil)},
		{ID: "N4", Group: g, Title: "第二步", State: "搬移中", Build: build(downloads, 1, func(_ *testing.T, o *onboardingFeature) { o.moving = true })},
		{ID: "N5", Group: g, Title: "第二步", State: "已到位 · 重新打开", Build: build(downloads, 1, func(_ *testing.T, o *onboardingFeature) { o.moved = true })},
		{ID: "N6", Group: g, Title: "第二步", State: "搬移失败 · 重试", Build: build(downloads, 1, func(_ *testing.T, o *onboardingFeature) {
			o.moveErr = errGalleryMove
		})},
		{ID: "N7", Group: g, Title: "第三步", State: "权限 · 三项未开", Build: build(originInstalled, 1, nil)},
		{ID: "N8", Group: g, Title: "第四步", State: "试一试 · 示例文件与演示", Build: build(originInstalled, 2, nil)},
		{ID: "N9", Group: g, Title: "第四步", State: "拖到投放区 · 松开放入", Build: build(originInstalled, 2, func(_ *testing.T, o *onboardingFeature) { o.demoOn = true })},
		{ID: "N10", Group: g, Title: "完成", State: "文件已在托盘 · 开始使用", Build: build(originInstalled, 2, func(_ *testing.T, o *onboardingFeature) {
			o.tried = true
		})},
		{ID: "N11", Group: g, Title: "第三步", State: "权限 · 三项已开", Build: build(originInstalled, 1, func(t *testing.T, o *onboardingFeature) {
			allPermissionsOn(t)
		})},
		{ID: "N12", Group: g, Title: "跳过", State: "完成胶囊 · 随时点刘海呼出", Notch: true, Build: galleryCapsule("随时点刘海呼出", "Click the notch anytime", statusInfo)},
		{ID: "N13", Group: g, Title: "完成", State: "完成胶囊 · Rakko 已就绪", Notch: true, Build: galleryCapsule("Rakko 已就绪", "Rakko is ready", statusOK)},
	}
}

// allPermissionsOn makes the three probes answer granted for one build.
func allPermissionsOn(t *testing.T) {
	t.Helper()
	fda, ax, screen := fullDiskAccessProbe, accessibilityProbe, screenCaptureProbe
	fullDiskAccessProbe = func() bool { return true }
	accessibilityProbe = func() bool { return true }
	screenCaptureProbe = func() bool { return true }
	t.Cleanup(func() {
		fullDiskAccessProbe, accessibilityProbe, screenCaptureProbe = fda, ax, screen
	})
}

var errGalleryMove = errors.New("没有写入「应用程序」的权限")
