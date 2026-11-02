package main

import (
	"github.com/egoist/mygo/ui"

	"rakko/internal/store"
)

// selection is the set of selected items, Finder-style: a click selects one
// item, ⌘-click adds or removes one, ⇧-click selects the range from the last
// plain or ⌘-clicked item.
type selection struct {
	ids    map[string]bool
	anchor string
}

func (s *selection) has(id string) bool { return s.ids[id] }

func (s *selection) count() int { return len(s.ids) }

func (s *selection) clear() {
	s.ids, s.anchor = nil, ""
}

// only selects id alone.
func (s *selection) only(id string) {
	s.ids, s.anchor = map[string]bool{id: true}, id
}

// onlyAll selects exactly ids (a batch of new items), anchored at the
// first; no ids clears the selection.
func (s *selection) onlyAll(ids []string) {
	if len(ids) == 0 {
		s.clear()
		return
	}
	s.ids, s.anchor = make(map[string]bool, len(ids)), ids[0]
	for _, id := range ids {
		s.ids[id] = true
	}
}

// click applies a click on id with mods; items is the list in display order.
func (s *selection) click(items []store.Item, id string, mods ui.Modifiers) {
	switch {
	case mods&ui.Cmd != 0:
		if s.ids[id] {
			delete(s.ids, id)
		} else {
			if s.ids == nil {
				s.ids = map[string]bool{}
			}
			s.ids[id] = true
		}
		s.anchor = id
	case mods&ui.Shift != 0 && s.anchor != "":
		from, to := indexOf(items, s.anchor), indexOf(items, id)
		if from < 0 || to < 0 {
			s.only(id)
			return
		}
		if from > to {
			from, to = to, from
		}
		s.ids = make(map[string]bool, to-from+1)
		for _, it := range items[from : to+1] {
			s.ids[it.ID] = true
		}
	default:
		s.only(id)
	}
}

// keepExisting forgets items that are no longer in the list.
func (s *selection) keepExisting(items []store.Item) {
	if len(s.ids) == 0 {
		return
	}
	present := make(map[string]bool, len(items))
	for _, it := range items {
		present[it.ID] = true
	}
	for id := range s.ids {
		if !present[id] {
			delete(s.ids, id)
		}
	}
	if !present[s.anchor] {
		s.anchor = ""
	}
}

// chosen returns the selected items in display order.
func (s *selection) chosen(items []store.Item) []store.Item {
	var out []store.Item
	for _, it := range items {
		if s.ids[it.ID] {
			out = append(out, it)
		}
	}
	return out
}

func indexOf(items []store.Item, id string) int {
	for i, it := range items {
		if it.ID == id {
			return i
		}
	}
	return -1
}
