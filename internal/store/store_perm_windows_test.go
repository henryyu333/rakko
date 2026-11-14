//go:build windows

package store

import "testing"

// Windows access is decided by the file's ACL, which os.FileMode does not
// report: a mode check here would only read a copy of the read-only bit.
// The data directory's privacy is inherited from %AppData%\Rakko's ACL, so
// these checks have nothing to assert on this platform.
func requireOwnerOnlyFile(*testing.T, string) {}
func requireOwnerOnlyDir(*testing.T, string)  {}
