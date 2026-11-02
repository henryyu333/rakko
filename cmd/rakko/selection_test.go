package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

func listOf(ids ...string) []store.Item {
	items := make([]store.Item, len(ids))
	for i, id := range ids {
		items[i] = store.Item{ID: id}
	}
	return items
}

func chosenIDs(s *selection, items []store.Item) []string {
	var out []string
	for _, it := range s.chosen(items) {
		out = append(out, it.ID)
	}
	return out
}

func TestPlainClickSelectsOnlyThatItem(t *testing.T) {
	items := listOf("a", "b", "c")
	var s selection
	s.click(items, "a", ui.Cmd)
	s.click(items, "b", ui.Cmd)
	s.click(items, "c", 0)
	if got := chosenIDs(&s, items); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("plain click kept %v selected; want only c", got)
	}
}

func TestCmdClickTogglesWithoutTouchingOthers(t *testing.T) {
	items := listOf("a", "b", "c")
	var s selection
	s.click(items, "a", 0)
	s.click(items, "c", ui.Cmd)
	s.click(items, "a", ui.Cmd)
	if got := chosenIDs(&s, items); !slices.Equal(got, []string{"c"}) {
		t.Fatalf("⌘-click toggling gave %v; want [c]", got)
	}
}

func TestShiftClickSelectsRangeInEitherDirection(t *testing.T) {
	items := listOf("a", "b", "c", "d", "e")
	var s selection
	s.click(items, "d", 0)
	s.click(items, "b", ui.Shift)
	if got := chosenIDs(&s, items); !slices.Equal(got, []string{"b", "c", "d"}) {
		t.Fatalf("upward range = %v", got)
	}
	// The anchor stays put: a second ⇧-click re-ranges from d.
	s.click(items, "e", ui.Shift)
	if got := chosenIDs(&s, items); !slices.Equal(got, []string{"d", "e"}) {
		t.Fatalf("re-range from anchor = %v; want [d e]", got)
	}
}

func TestShiftClickWithoutAnchorSelectsOne(t *testing.T) {
	items := listOf("a", "b")
	var s selection
	s.click(items, "b", ui.Shift)
	if got := chosenIDs(&s, items); !slices.Equal(got, []string{"b"}) {
		t.Fatalf("⇧-click with nothing selected = %v", got)
	}
}

func TestRemovedItemsLeaveTheSelection(t *testing.T) {
	items := listOf("a", "b", "c")
	var s selection
	s.click(items, "a", 0)
	s.click(items, "c", ui.Shift)
	s.keepExisting(listOf("a", "c"))
	if s.count() != 2 || s.has("b") {
		t.Fatalf("removed item still selected: %v", s.ids)
	}
	// The anchor was kept (a still exists), so ranges still work.
	s.keepExisting(listOf("c"))
	s.click(listOf("c", "d"), "d", ui.Shift)
	if got := chosenIDs(&s, listOf("c", "d")); !slices.Equal(got, []string{"d"}) {
		t.Fatalf("range from a removed anchor = %v; want [d]", got)
	}
}
