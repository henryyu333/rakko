//go:build darwin

package main

import (
	"time"

	"rakko/internal/store"
)

// 拖出后从托盘移除 (the removeAfterDrag setting): items dragged out of the
// tray and dropped somewhere leave the tray at once, but are only taken
// off the shelf leaveGrace later. A screenshot or converted file lives in
// ShelfU's own folder and is deleted with its item, and the app it was
// dropped on may still be about to read it; the grace keeps it there
// until then. Pinned items stay.

// leaveGrace is how long a dragged-out item's file outlives its tray row.
const leaveGrace = 10 * time.Second

// setRemoveAfterDrag saves the removeAfterDrag setting; the next drag
// follows it.
func (a *shelf) setRemoveAfterDrag(on bool) {
	if a.settings.RemoveAfterDrag == on {
		return
	}
	next := a.settings
	next.RemoveAfterDrag = on
	a.applySettings(next)
}

// leaveTray hides the items with ids from the tray, as dropped at now, if
// the setting is on; leavingModule removes them for good later.
func (a *shelf) leaveTray(ids []string, now time.Time) {
	if !a.settings.RemoveAfterDrag || a.store == nil || len(ids) == 0 {
		return
	}
	marked := false
	for _, it := range a.store.Items {
		for _, id := range ids {
			if it.ID == id && !it.Pinned {
				if a.leaving == nil {
					a.leaving = map[string]time.Time{}
				}
				a.leaving[id] = now
				marked = true
			}
		}
	}
	if !marked {
		return
	}
	a.selection.keepExisting(a.visibleItems())
	a.syncWindowFrame()
	if a.win != nil {
		a.win.Invalidate()
	}
}

// isLeaving reports whether it was dragged out and is waiting to be
// removed. Adding the same item again (which refreshes its timestamp) or
// pinning it from the clipboard page brings it back.
func (a *shelf) isLeaving(it store.Item) bool {
	at, ok := a.leaving[it.ID]
	return ok && !it.Pinned && !it.CreatedAt.After(at)
}

// flushLeaving removes the leaving items dropped before cutoff, as the
// tray's 移除 would: a clipboard capture stays in the clipboard history.
// If saving fails the store rolls back; those items stay hidden and are
// tried again a grace later. Quitting passes a cutoff past every drop.
func (a *shelf) flushLeaving(cutoff time.Time) {
	if len(a.leaving) == 0 || a.store == nil {
		return
	}
	var gone []store.Item
	for _, it := range a.store.Items {
		if at, ok := a.leaving[it.ID]; ok && at.Before(cutoff) && a.isLeaving(it) {
			gone = append(gone, it)
		}
	}
	for id, at := range a.leaving {
		if at.Before(cutoff) {
			delete(a.leaving, id)
		}
	}
	if len(gone) == 0 {
		return
	}
	a.removeFrom(gone, pageTray)
	retry := map[string]bool{}
	for _, it := range gone {
		retry[it.ID] = true
	}
	now := time.Now()
	for _, it := range a.store.Items {
		if retry[it.ID] && !it.OffTray {
			a.leaving[it.ID] = now
		}
	}
}

// leavingModule finishes the removals once their grace is over.
type leavingModule struct{ host *shelf }

func (m *leavingModule) tick(now time.Time) { m.host.flushLeaving(now.Add(-leaveGrace)) }

// nextTick is the earliest dragged-out item's grace expiry.
func (m *leavingModule) nextTick(now time.Time) time.Time {
	var next time.Time
	for _, at := range m.host.leaving {
		due := at.Add(leaveGrace)
		if due.After(now) && (next.IsZero() || due.Before(next)) {
			next = due
		} else if !due.After(now) {
			return now
		}
	}
	return next
}
