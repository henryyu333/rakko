//go:build darwin

package main

import (
	"log"
	"time"
)

// retentionCheck is how often old items are looked for; the first check
// runs on the first tick after launch.
const retentionCheck = time.Hour

// retentionChoices are the periods offered in the menu-bar menu.
var retentionChoices = []int{1, 7, 30}

// retentionModule removes items older than the retentionDays setting. It
// reads the setting on every check, so a change from the menu applies at
// once without restarting.
type retentionModule struct {
	host *shelf
	last time.Time
}

func (m *retentionModule) tick(now time.Time) {
	if !m.last.IsZero() && now.Sub(m.last) < retentionCheck {
		return
	}
	m.last = now
	m.host.removeExpired(now)
}

// removeExpired applies the retention setting once.
func (a *shelf) removeExpired(now time.Time) {
	days := a.settings.RetentionDays
	if days <= 0 || a.store == nil {
		return
	}
	l := a.appLoc()
	n, err := a.store.RemoveOlderThan(now.AddDate(0, 0, -days))
	if err != nil {
		// n > 0: removed and saved, but an image file could not be deleted.
		// 后台自动清理不惊动刘海胶囊：留在面板底部提示里。
		a.toastQuiet(l.RetentionFailed(err.Error()), time.Time{}, statusErr)
		log.Print(a.status)
	}
	if n > 0 {
		a.dropStaleThumbnails()
		a.selection.keepExisting(a.visibleItems())
		if err == nil {
			a.toastQuiet(l.RetentionCleaned(n, days), now.Add(5*time.Second), statusInfo)
		}
		if a.win != nil {
			a.win.Invalidate()
		}
	}
}
