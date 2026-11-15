//go:build windows

package settings

// defaultShellStyle is the closed shelf's shape on a fresh install: a
// display with no camera housing draws the simulated notch (模拟小刘海) at
// the top edge, the Mac look this port is meant to match. The floating
// 灵动岛 capsule stays selectable and, like the notch style, is what every
// screen without a housing had before this default.
const defaultShellStyle = ShellStyleNotch
