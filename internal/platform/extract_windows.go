//go:build windows

package platform

// Archive extraction and the shell's "add to Rakko" service. The extractor
// can reuse tar.exe later; both are batch items.
// WINDOWS-NEXT(extract), WINDOWS-NEXT(services)

// ExtractWithSystemTool runs the system's extractor with its streams
// captured separately. WINDOWS-NEXT(extract)
func ExtractWithSystemTool(args []string, canceled func() bool) ([]byte, error) {
	return nil, errDomain("extract")
}

// InstallAddToRakko registers the process as the handler of the Explorer
// context-menu entry. WINDOWS-NEXT(services)
func InstallAddToRakko(fn func(paths []string, text string)) error { return nil }
