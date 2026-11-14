//go:build windows

package settings

// defaultShellStyle is the closed shelf's shape on a fresh install: a
// display with no camera housing draws the floating 灵动岛 capsule by
// default. The notch style stays selectable and simulates a housing at the
// screen's top edge.
const defaultShellStyle = ShellStyleIsland
