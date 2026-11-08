package mac

import (
	"fmt"
	"unsafe"
)

// TrashFile moves the file, folder or bundle at path to the Trash, the way
// Finder's 移到废纸篓 does, and answers where it landed. It fails rather
// than deleting anything when the item cannot be trashed, so the caller's
// fallback can leave the item where it is. Callable from any thread: it is
// Foundation's NSFileManager (thread-safe) wrapped in an autorelease pool,
// with no AppKit or window server work, so the guide's move can trash the
// old bundle from its worker without stopping the panel.
func TrashFile(path string) (trashed string, err error) {
	if err := Init(); err != nil {
		return "", err
	}
	var out id
	var nerr id
	ok := false
	withPool(func() {
		fm := send(class("NSFileManager"), "defaultManager")
		url := send(class("NSURL"), "fileURLWithPath:", uintptr(nsString(path)))
		if url == 0 {
			return
		}
		ok = send(fm, "trashItemAtURL:resultingItemURL:error:", uintptr(url), uintptr(unsafe.Pointer(&out)), uintptr(unsafe.Pointer(&nerr))) != 0
	})
	switch {
	case ok:
		return goString(send(out, "path")), nil
	case nerr != 0:
		return "", fmt.Errorf("%s", goString(send(nerr, "localizedDescription")))
	default:
		return "", fmt.Errorf("trashItemAtURL failed")
	}
}
