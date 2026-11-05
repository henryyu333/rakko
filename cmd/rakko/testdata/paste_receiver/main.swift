// Rakko's clipboard-paste acceptance target: a tiny AppKit app with an Edit ▸
// Paste menu item. It is the controlled "other app" the real ⌘V lands in — not
// the user's editor. Arguments: <output dir> [text|image].
//
// It writes <output dir>/receiver.pid at launch and rewrites
// <output dir>/receiver.txt ten times a second with the window's state, so the
// test can assert what a real target editing control actually received.

import AppKit

let outputDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "/tmp/rakko-paste-receiver"
let mode = CommandLine.arguments.count > 2 ? CommandLine.arguments[2] : "text"

/// The image target: a view that takes Paste and reports the pasteboard.
final class PasteView: NSView, NSUserInterfaceValidations {
    override var acceptsFirstResponder: Bool { true }

    @objc func paste(_ sender: Any?) {
        let board = NSPasteboard.general
        let types = (board.types ?? []).map { $0.rawValue }.joined(separator: ",")
        let png = board.data(forType: .png) ?? Data()
        let tiff = board.data(forType: .tiff) ?? Data()
        Receiver.shared.paste = "types=\(types) png=\(png.count) tiff=\(tiff.count)"
    }

    func validateUserInterfaceItem(_ item: NSValidatedUserInterfaceItem) -> Bool { true }
}

final class Receiver: NSObject, NSApplicationDelegate {
    static let shared = Receiver()
    var textView: NSTextView?
    var paste = ""
    var window: NSWindow?

    func applicationDidFinishLaunching(_ note: Notification) {
        buildMenu()
        let frame = NSRect(x: 0, y: 0, width: 420, height: 180)
        let window = NSWindow(contentRect: frame,
                              styleMask: [.titled, .closable],
                              backing: .buffered, defer: false)
        window.title = "Rakko Paste Receiver"
        window.center()
        if mode == "image" {
            let view = PasteView(frame: frame)
            window.contentView = view
            window.makeFirstResponder(view)
        } else {
            let text = NSTextView(frame: frame)
            text.isEditable = true
            text.isRichText = false
            window.contentView = text
            window.makeFirstResponder(text)
            textView = text
        }
        self.window = window
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
        try? "\(ProcessInfo.processInfo.processIdentifier)"
            .write(toFile: outputDir + "/receiver.pid", atomically: true, encoding: .utf8)
        Timer.scheduledTimer(withTimeInterval: 0.1, repeats: true) { _ in self.dump() }
        dump()
    }

    func buildMenu() {
        let main = NSMenu()
        let editItem = NSMenuItem()
        let edit = NSMenu(title: "Edit")
        let paste = NSMenuItem(title: "Paste", action: Selector(("paste:")), keyEquivalent: "v")
        paste.target = nil // the responder chain: the text view, or PasteView
        edit.addItem(paste)
        editItem.submenu = edit
        main.addItem(editItem)
        NSApp.mainMenu = main
    }

    func dump() {
        let front = NSWorkspace.shared.frontmostApplication?.processIdentifier ?? 0
        var lines = [
            "pid=\(ProcessInfo.processInfo.processIdentifier)",
            "frontmost=\(front)",
            "text=\((textView?.string ?? "").replacingOccurrences(of: "\n", with: "\\n"))",
            "paste=\(paste)",
        ]
        lines.append("key=\(window?.isKeyWindow == true ? 1 : 0)")
        try? lines.joined(separator: "\n")
            .write(toFile: outputDir + "/receiver.txt", atomically: true, encoding: .utf8)
    }
}

let app = NSApplication.shared
app.setActivationPolicy(.accessory)
app.delegate = Receiver.shared
app.run()
