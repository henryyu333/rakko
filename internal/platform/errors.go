package platform

import "errors"

// Errors the file operations report in a form every platform shares. The
// platform files translate their own error codes into these; callers match
// with errors.Is instead of comparing system constants.
var (
	// ErrExists means the target name is already taken.
	ErrExists = errors.New("platform: target exists")
	// ErrNotExist means the source no longer exists.
	ErrNotExist = errors.New("platform: target does not exist")
	// ErrPermission means the filesystem refused the operation.
	ErrPermission = errors.New("platform: permission denied")
	// ErrNameTooLong means the name exceeds the filesystem's limit.
	ErrNameTooLong = errors.New("platform: name too long")
	// ErrNotDirectory means a path component is not a directory.
	ErrNotDirectory = errors.New("platform: not a directory")
	// ErrHEIFExtensionMissing means the Windows build cannot open a HEIC
	// image at all: the HEIF Image Extensions that register its decoder and
	// encoder are not installed.
	ErrHEIFExtensionMissing = errors.New("platform: HEIF Image Extensions missing")
	// ErrHEVCExtensionMissing means the Windows build found the HEIC
	// container but cannot produce or write its pixels: the HEVC Video
	// Extensions are not installed.
	ErrHEVCExtensionMissing = errors.New("platform: HEVC Video Extensions missing")
)
