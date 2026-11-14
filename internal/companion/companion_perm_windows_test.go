//go:build windows

package companion

import "testing"

// Windows access is decided by the file's ACL, which os.FileMode does not
// report: a mode check here would only read a copy of the read-only bit.
func requireOwnerOnlyFile(*testing.T, string) {}
