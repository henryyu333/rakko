//go:build darwin

package platform

// ConsumeServiceArgs exists for the Windows Explorer hand-off: macOS
// delivers "添加到 Rakko" through NSServices to the running app, so a
// command line never carries service paths and is returned untouched.
func ConsumeServiceArgs(args []string) []string { return args }
