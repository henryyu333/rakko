//go:build windows

package platform

import "errors"

// errShareUnsupported reports that system nearby sharing is not offered on Windows.
var errShareUnsupported = errors.New("platform: ShareNearby is unsupported on windows")

// ShareNearby is a stub for shared cross-platform callers; Windows builds
// omit AirDrop / nearby sharing from every UI entry point.
func ShareNearby(paths []string, texts []string) error {
	return errShareUnsupported
}
