//go:build darwin

package settings

// defaultShellStyle is the closed shelf's shape on a fresh install: the
// hardware notch decides the drawing on a notched display, and the notch
// style is the settled look of every Mac.
const defaultShellStyle = ShellStyleNotch
