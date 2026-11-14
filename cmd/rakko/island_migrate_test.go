package main

// Saved island layouts must survive a change of display identity. Windows
// used to key them by the adapter output's name; the current identity is the
// physical monitor's, so a layout written by an earlier build has to be
// re-keyed before anything reads it, or the user's place is silently lost.

import (
	"testing"

	"rakko/internal/settings"
)

// TestOldIslandLayoutRestores checks that a place saved under a display id an
// earlier build wrote is migrated to the id the running system uses and is then
// read back through the consumer that restores it.
func TestOldIslandLayoutRestores(t *testing.T) {
	oldMigrator := displayIDMigrator
	displayIDMigrator = func(id int64) int64 {
		if id == 41 {
			return 99
		}
		return id
	}
	defer func() { displayIDMigrator = oldMigrator }()

	saved := settings.IslandPos{X: 640, Y: 12}
	layouts := map[string]settings.IslandPos{
		islandSpotKey(41): saved,
		"not-a-display":   {X: 1, Y: 2},
	}
	migrated, changed := migrateIslandSpots(layouts)
	if !changed {
		t.Fatal("a layout saved under the old display id was not migrated")
	}
	if _, ok := migrated[islandSpotKey(41)]; ok {
		t.Fatal("the old display id is still a key")
	}
	if got := migrated[islandSpotKey(99)]; got != saved {
		t.Fatalf("migrated place = %+v, want the saved %+v", got, saved)
	}
	if got := migrated["not-a-display"]; got != (settings.IslandPos{X: 1, Y: 2}) {
		t.Fatalf("a key that is not a display id was dropped: %+v", got)
	}

	// The reader asks by the current id: before the migration it finds
	// nothing, which is the bug, and after it the saved place restores.
	a := &shelf{settings: settings.Default()}
	a.dispID = 99
	a.settings.IslandPos = layouts
	if _, ok := a.savedIslandSpot(); ok {
		t.Fatal("a place saved under the old id already matched the current display")
	}
	a.settings.IslandPos = migrated
	if pos, ok := a.savedIslandSpot(); !ok || pos != saved {
		t.Fatalf("savedIslandSpot = %+v, %v; want the migrated %+v", pos, ok, saved)
	}
}

// TestMigrationKeepsCurrentKeys checks that a place already stored under the
// current id wins over a migrated one, and that nothing is rewritten when no
// key needs it.
func TestMigrationKeepsCurrentKeys(t *testing.T) {
	oldMigrator := displayIDMigrator
	displayIDMigrator = func(id int64) int64 {
		if id == 41 {
			return 99
		}
		return id
	}
	defer func() { displayIDMigrator = oldMigrator }()

	current := settings.IslandPos{X: 10, Y: 20}
	lay := map[string]settings.IslandPos{
		islandSpotKey(41): {X: 640, Y: 12},
		islandSpotKey(99): current,
	}
	migrated, changed := migrateIslandSpots(lay)
	if !changed {
		t.Fatal("the old key was not noticed")
	}
	if got := migrated[islandSpotKey(99)]; got != current {
		t.Fatalf("the current place = %+v, want %+v", got, current)
	}
	if len(migrated) != 1 {
		t.Fatalf("migrated layouts = %+v, want only the current one", migrated)
	}

	// With nothing to map, the same map comes back and no save is attempted.
	same := map[string]settings.IslandPos{islandSpotKey(99): current}
	if out, changed := migrateIslandSpots(same); changed || len(out) != 1 {
		t.Fatalf("a current layout was rewritten: %+v, changed %v", out, changed)
	}
}
