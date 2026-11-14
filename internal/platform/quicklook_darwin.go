//go:build darwin

package platform

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// qlState backs ShelfQLPreviewSource, the QLPreviewPanel data source
// registered at first use. The panel and this state live on the main
// thread; the panel keeps no copy of the paths, so a second Preview call
// replaces the items a visible panel shows (the contract's "replaces any
// items already shown").
var qlState struct {
	paths []string
}

var (
	qlSetupOnce sync.Once
	qlSetupErr  error
	qlSource    id // one shared ShelfQLPreviewSource instance, never freed

	qlFrameworkOnce sync.Once
)

// loadQuickLookUI pulls QuickLookUI.framework into the process; the ObjC
// runtime resolves QLPreviewPanel only once the framework is loaded.
func loadQuickLookUI() {
	qlFrameworkOnce.Do(func() {
		_, _ = purego.Dlopen(
			"/System/Library/Frameworks/Quartz.framework/Frameworks/QuickLookUI.framework/QuickLookUI",
			purego.RTLD_GLOBAL|purego.RTLD_NOW)
	})
}

// qlSharedPanel returns the shared QLPreviewPanel (0 when QLPreviewPanel is
// missing — a headless or stripped system).
func qlSharedPanel() id {
	load()
	if loadErr != nil {
		return 0
	}
	loadQuickLookUI()
	cls := objc.GetClass("QLPreviewPanel")
	if cls == 0 {
		return 0
	}
	return send(id(cls), "sharedPreviewPanel")
}

// qlItemCount answers numberOfPreviewItemsInPreviewPanel:.
func qlItemCount(_ id, _ objc.SEL, _ id) int {
	return len(qlState.paths)
}

// qlItemAt answers previewPanel:previewItemAtIndex:. The index is
// bounds-checked because the panel may query before reloadData has fully
// swapped a replaced item set. NSURL conforms to QLPreviewItem natively.
func qlItemAt(_ id, _ objc.SEL, _ id, index int) id {
	if index < 0 || index >= len(qlState.paths) {
		return 0
	}
	return send(class("NSURL"), "fileURLWithPath:", uintptr(nsString(qlState.paths[index])))
}

// These three IMPs form the panel's controller handshake. A QLPreviewPanel
// scans the application's responder chain — first responder, then window,
// window delegate, NSApplication, then NSApp's delegate — for an object
// that accepts control, and refuses to stay open without one. They are
// grafted onto the classes at both ends of that chain so the search finds
// them no matter which window (if any) is key; the shelf's own panel is
// nonactivating and never becomes key, so nothing in its chain would be
// found otherwise.
func qlAcceptsControl(_ id, _ objc.SEL, _ id) bool { return true }

func qlBeginControl(_ id, _ objc.SEL, panel id) {
	send(panel, "setDataSource:", uintptr(qlSource))
	send(panel, "setDelegate:", uintptr(qlSource))
	send(panel, "reloadData")
}

func qlEndControl(_ id, _ objc.SEL, panel id) {
	send(panel, "setDataSource:", 0)
	send(panel, "setDelegate:", 0)
}

// qlInstallController grafts the controller handshake onto the class of
// NSApp's delegate and onto NSApplication itself — the two ends of the
// responder chain the panel scans (which end it reaches depends on which
// window, if any, is key). class_replaceMethod only ever *adds* here: a
// class that already implements a selector is left alone. A bare
// NSObject delegate is skipped so the selectors do not leak onto every
// object in the process.
func qlInstallController() error {
	app := send(class("NSApplication"), "sharedApplication")
	if app == 0 {
		return errors.New("mac: NSApplication not initialized")
	}
	var classes []id
	if d := send(app, "delegate"); d != 0 {
		if cls := send(d, "class"); cls != 0 && cls != class("NSObject") {
			classes = append(classes, cls)
		}
	}
	if cls := send(app, "class"); cls != 0 && (len(classes) == 0 || classes[0] != cls) {
		classes = append(classes, cls)
	}
	if len(classes) == 0 {
		return errors.New("mac: cannot reach the application class")
	}
	for _, cls := range classes {
		add := func(name, types string, fn any) {
			if send(cls, "instancesRespondToSelector:", uintptr(sel(name))) != 0 {
				return
			}
			classReplaceMethod(uintptr(cls), sel(name), uintptr(objc.NewIMP(fn)), types)
		}
		add("acceptsPreviewPanelControl:", "B@:@", qlAcceptsControl)
		add("beginPreviewPanelControl:", "v@:@", qlBeginControl)
		add("endPreviewPanelControl:", "v@:@", qlEndControl)
		if send(cls, "instancesRespondToSelector:", uintptr(sel("acceptsPreviewPanelControl:"))) == 0 {
			return errors.New("mac: Quick Look controller install failed")
		}
	}
	return nil
}

// qlSetup registers ShelfQLPreviewSource once and keeps the instance alive
// in qlSource (panels hold their data source weakly, so the Go side must
// own it for the process's lifetime).
func qlSetup() error {
	qlSetupOnce.Do(func() {
		cls, err := objc.RegisterClass("ShelfQLPreviewSource", objc.GetClass("NSObject"), nil, nil, []objc.MethodDef{
			{Cmd: sel("numberOfPreviewItemsInPreviewPanel:"), Fn: qlItemCount},
			{Cmd: sel("previewPanel:previewItemAtIndex:"), Fn: qlItemAt},
		})
		if err != nil {
			qlSetupErr = fmt.Errorf("mac: Quick Look data source: %w", err)
			return
		}
		qlSource = send(id(cls), "new")
		if qlSource == 0 {
			qlSetupErr = errors.New("mac: cannot allocate Quick Look data source")
			return
		}
		qlSetupErr = qlInstallController()
	})
	return qlSetupErr
}

// Preview shows the system Quick Look panel for the given absolute file
// paths, starting at index 0; replaces any items already shown. Missing
// files are skipped; a call left with no previews is an error.
// Main thread only.
func Preview(paths []string) error {
	if err := Init(); err != nil {
		return err
	}
	var live []string
	for _, p := range paths {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			live = append(live, p)
		}
	}
	if len(live) == 0 {
		return errors.New("mac: Quick Look needs at least one existing file")
	}
	var lookErr error
	withPool(func() {
		if err := qlSetup(); err != nil {
			lookErr = err
			return
		}
		panel := qlSharedPanel()
		if panel == 0 {
			lookErr = errors.New("mac: Quick Look panel unavailable")
			return
		}
		qlState.paths = live
		// The panel may not have asked for control yet (first use): wire
		// the data source eagerly so reloadData sees the new set; the
		// controller handshake repeats it once the panel opens.
		send(panel, "setDataSource:", uintptr(qlSource))
		send(panel, "setDelegate:", uintptr(qlSource))
		send(panel, "reloadData")
		send(panel, "setCurrentPreviewItemIndex:", 0)
		send(panel, "makeKeyAndOrderFront:", 0)
		if send(panel, "isVisible") == 0 {
			send(panel, "orderFront:", 0)
		}
	})
	return lookErr
}

// PreviewVisible reports whether ShelfU's Quick Look panel is open.
// Main thread only. Off the main thread (headless tests) it reports false
// without instantiating the panel, which AppKit allows only on the main
// thread.
func PreviewVisible() bool {
	if !OnMainThread() {
		return false
	}
	visible := false
	withPool(func() {
		panel := qlSharedPanel()
		visible = panel != 0 && send(panel, "isVisible") != 0
	})
	return visible
}

// ClosePreview closes the Quick Look panel; no-op when closed.
// Main thread only.
func ClosePreview() {
	withPool(func() {
		if panel := qlSharedPanel(); panel != 0 {
			send(panel, "orderOut:", 0)
		}
	})
}
