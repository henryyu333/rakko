//go:build darwin

package mac

// 「文件访问 · 完整磁盘访问」：macOS 没有只读的 FDA 查询 API，唯一的只读
// 办法是打开一个只有 FDA 才能读的受保护文件。TCC 数据库正是这样的文件：
// open(O_RDONLY) 成功即已授权，EPERM/EACCES 即未授权；其它错误（文件不存在、
// 沙盒等）同样按未授权处理。只 open 再立即 close，不读一个字节，因此这条
// 探测不会弹出任何系统对话框 —— TCC 的弹框只由带 prompt 的 API 或首次访问
// 受保护文件夹触发，被拒绝的 open 只返回 EPERM。
//
// 位置：较早的 macOS 有用户库 ~/Library/Application Support/com.apple.TCC/TCC.db；
// 本机 macOS 27.0.1 上没有这个文件，只有系统库
// /Library/Application Support/com.apple.TCC/TCC.db。两个位置都试，任一能打开
// 即已授权；在本机实测未授权的独立进程打开系统库拿到 EPERM，没有任何对话框。
//
// 申请只能由用户完成：系统设置 → 隐私与安全性 → 完整磁盘访问 里没有
// 「添加」以外的程序化入口。所以 RequestFullDiskAccess 只打开该面板，并把
// 正在运行的 .app 在访达里选中，用户既可以点 + 选它，也可以直接把它拖进
// 列表。不在 .app 里运行时（go run/go test）没有可拖动的 bundle，只开面板。

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// fullDiskAccessPane is 系统设置 → 隐私与安全性 → 完整磁盘访问.
	fullDiskAccessPane = "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles"
	// privacySecurityPane is the 隐私与安全性 root page, where every
	// permission switch lives. Used when the UI has nothing to switch.
	privacySecurityPane = "x-apple.systempreferences:com.apple.preference.security"
)

// Seams for the tests, which must never open System Settings, Finder or a
// real TCC-protected file.
var (
	fullDiskAccessPaths    = defaultFullDiskAccessPaths
	fullDiskAccessOpenFile = func(path string) (*os.File, error) { return os.Open(path) }
	revealBundleInFinder   = revealInFinder
	fullDiskAccessExe      = os.Executable
)

// defaultFullDiskAccessPaths are the TCC databases in the order the probe
// tries them: the per-user one first (macOS 25 and older), then the system
// one that also exists on the versions that dropped the user database.
func defaultFullDiskAccessPaths() []string {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, "Library", "Application Support", "com.apple.TCC", "TCC.db"))
	}
	return append(paths, "/Library/Application Support/com.apple.TCC/TCC.db")
}

// FullDiskAccessGranted reports whether this process may read the folders
// Full Disk Access protects (Desktop, Documents, Downloads, iCloud Drive,
// network and removable volumes). It opens a TCC database read-only and
// closes it at once: a success means the grant is in effect, any failure
// means it is not. It never prompts and never changes anything, so the
// Settings window may call it every frame; it is a plain open/close, so any
// goroutine may call it too.
func FullDiskAccessGranted() bool {
	for _, path := range fullDiskAccessPaths() {
		f, err := fullDiskAccessOpenFile(path)
		if err != nil {
			continue
		}
		_ = f.Close()
		return true
	}
	return false
}

// RequestFullDiskAccess is the user-initiated path to grant the permission:
// it opens the 完整磁盘访问 pane and selects the running .app bundle in a
// Finder window, so the user can press + or drag the bundle into the list.
// It reports whether the permission was already in effect; an already
// granted call leaves both System Settings and Finder alone. Main thread
// only (the reveal goes through NSWorkspace).
func RequestFullDiskAccess() bool {
	if FullDiskAccessGranted() {
		return true
	}
	openURL(fullDiskAccessPane)
	exe, err := fullDiskAccessExe()
	if err != nil {
		return false
	}
	if bundle, ok := AppBundleForExecutable(exe); ok {
		revealBundleInFinder(bundle)
	}
	return false
}

// OpenPrivacySecurityPane opens 系统设置 → 隐私与安全性. macOS gives no app
// a way to revoke a grant, so the UI that offers to switch permissions off
// can only show the user where they live.
func OpenPrivacySecurityPane() {
	openURL(privacySecurityPane)
}

// RevealInFinder selects path in a new Finder window, as Finder's 显示 in
// enclosing folder. Main thread only.
func RevealInFinder(path string) { revealInFinder(path) }

// revealInFinder selects path in a new Finder window, as Finder's 显示 in
// enclosing folder: NSWorkspace.activateFileViewerSelectingURLs:. Main
// thread only.
func revealInFinder(path string) {
	if err := Init(); err != nil {
		return
	}
	withPool(func() {
		url := send(class("NSURL"), "fileURLWithPath:", uintptr(nsString(path)))
		if url == 0 {
			return
		}
		urls := send(class("NSArray"), "arrayWithObject:", uintptr(url))
		send(send(class("NSWorkspace"), "sharedWorkspace"), "activateFileViewerSelectingURLs:", uintptr(urls))
	})
}

// AppBundleForExecutable reports the enclosing .app when exe lives at
// <Bundle>.app/Contents/MacOS/<binary>. It is a pure path check: the
// Full Disk Access reveal and the relaunch plan both only need the bundle
// path, never the file itself.
func AppBundleForExecutable(exe string) (string, bool) {
	const marker = "/Contents/MacOS/"
	i := strings.LastIndex(exe, marker)
	if i <= 0 {
		return "", false
	}
	bundle := exe[:i]
	if !strings.HasSuffix(bundle, ".app") {
		return "", false
	}
	return bundle, true
}
