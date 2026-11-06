//go:build darwin

package mac

// 「框选文字」的屏幕录制权限：CGPreflightScreenCaptureAccess 只读查询，
// 不弹系统对话框；CGRequestScreenCaptureAccess 是用户主动发起的申请，
// 会把本 app 放进「系统设置 → 隐私与安全性 → 屏幕录制」的列表。
//
// 预检只回答「本进程还能不能截屏」，答案在整个进程生命周期内被缓存：
// 用户在本进程第一次预检之后才打开开关时，预检仍是 false。因此这里
// 从不替用户清 TCC 记录（tccutil reset）——那可能删掉用户刚给的授权。
// 旧 build 留下的失效条目由用户自己移除：界面提供对应面板的入口和可拖
// 入列表的真实 app 图标，申请后由下一次启动重新读取真实答案。
//
// WindowServer 是否会交出别的 app 的窗口标题不缓存，它是「授权已经
// 生效」的只读证据；有它就说明开关已开，只是本进程的预检还停在旧答案，
// 界面提示重新打开 Rakko。
import (
	"os/exec"
	"sync"

	"github.com/ebitengine/purego"
)

var preflightOnce sync.Once

var (
	cgPreflightScreenCaptureAccess func() bool
	cgRequestScreenCaptureAccess   func() bool
)

const screenCapturePane = "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture"

// openURL is a seam for the tests, which must never spawn `open`.
var openURL = func(url string) { _ = exec.Command("/usr/bin/open", url).Start() }

// windowTitlesProveGrant is the non-cached evidence that the grant is live
// (see ScreenCaptureGrantedByWindowTitles). A seam so the tests never read
// the real WindowServer.
var windowTitlesProveGrant = ScreenCaptureGrantedByWindowTitles

// ScreenCapturePreflight reports whether this process may capture the
// screen right now. It never prompts and never clears anything, so UI that
// only displays the state can call it freely. Note macOS caches the answer
// for the lifetime of the process: a grant made while the app is running
// can keep answering false until the next launch.
func ScreenCapturePreflight() bool {
	if err := Init(); err != nil {
		return false
	}
	preflightOnce.Do(func() {
		purego.RegisterLibFunc(&cgPreflightScreenCaptureAccess, libCG, "CGPreflightScreenCaptureAccess")
		purego.RegisterLibFunc(&cgRequestScreenCaptureAccess, libCG, "CGRequestScreenCaptureAccess")
	})
	return cgPreflightScreenCaptureAccess()
}

// ScreenCaptureGranted proves the grant without the cached preflight: window
// titles keep their names only while the screen-recording grant is live.
// False means "not proven" — a screen with no other titled window looks the
// same — so callers must not read it as a denial.
func ScreenCaptureGranted() bool {
	return windowTitlesProveGrant()
}

// RequestScreenCapture is the user-initiated path to grant the
// permission: macOS is asked to prompt or register the app, and System
// Settings is opened on the 屏幕录制 pane. It never clears this app's TCC
// record, so a grant the user just made cannot be deleted. It reports
// whether macOS says the grant is active; a cached preflight can still say
// false until the next launch, which the caller's UI names.
//
// A grant proven by window titles is already active even though this
// process's cached preflight still says no: nothing is asked and nothing is
// cleared — re-registering adds nothing. The pane still opens so the user
// sees the switch is already on; the remedy that works is a relaunch, which
// the caller's UI names.
func RequestScreenCapture() bool {
	if ScreenCapturePreflight() {
		return true
	}
	if ScreenCaptureGranted() {
		OpenScreenCapturePane()
		return false
	}
	if cgRequestScreenCaptureAccess == nil {
		OpenScreenCapturePane()
		return false // Init failed; preflight could not have passed either
	}
	ok := cgRequestScreenCaptureAccess()
	// The request adds the app to the list and (never-asked builds) asks;
	// a build that was denied before is not re-prompted, so the pane is
	// opened either way and the user flips the switch there.
	OpenScreenCapturePane()
	return ok
}

// OpenScreenCapturePane opens 系统设置 → 隐私与安全性 → 屏幕录制.
func OpenScreenCapturePane() {
	openURL(screenCapturePane)
}
