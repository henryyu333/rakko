//go:build windows

package main

// Data-directory migration. The old shelf names (ShelfU, Shelf) only ever
// shipped on macOS, so a Windows install has nothing to copy from; the
// signature stays so startup's single migration path keeps working.

import "errors"

// errLegacyRootLocked reports an old data directory another instance
// holds.
var errLegacyRootLocked = errors.New("migrate: the old data directory is in use by another instance")

// migrateDataRoot copies oldRoot to newRoot. There is no Windows legacy
// directory, so it reports that nothing was migrated.
func migrateDataRoot(oldRoot, newRoot string) (bool, error) { return false, nil }
