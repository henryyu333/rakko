//go:build windows

package platform

// ShareNearby is a stub for shared cross-platform callers; Windows builds
// omit AirDrop / nearby sharing from every UI entry point.
func ShareNearby(paths []string, texts []string) error {
	return errUnsupported
}
