//go:build darwin

package main

// The support line's one home: the first day of companionship, the click
// that says the user has seen the sponsorship page, and the two places that
// ask (the home page's status line and 设置 → 关于). The facts live in
// companion.json; this file only reads them and opens the page.

import (
	"time"

	"rakko/internal/companion"
)

// supportURL is the site's sponsorship page. The app links to the page, not
// to a payment service, so the channels can change without a new release.
const supportURL = "https://rakko-yu.pages.dev/support/"

// companionHeart is the home line's lead: an outlined heart at 14pt in the
// 16pt slot the other status lines use.
var companionHeart = symbol(`<path d="M12 19.5s-7.5-4.6-7.5-10.2A4.1 4.1 0 0 1 12 7a4.1 4.1 0 0 1 7.5 2.3c0 5.6-7.5 10.2-7.5 10.2z"/>`)

// companionDay is which day of companionship now is. It reports false
// before startup loaded the file, or when the file cannot be read: then the
// app shows neither the days nor the home line.
func (a *shelf) companionDay(now time.Time) (int, bool) {
	if a.companion == nil {
		return 0, false
	}
	return a.companion.Day(now)
}

// companionClicked reports whether the user has already opened the support
// page from inside the app; after that the page only says thank you.
func (a *shelf) companionClicked() bool {
	return a.companion != nil && a.companion.Clicked()
}

// openSupport opens the sponsorship page and remembers the click, so the
// home line stops asking. A failed open leaves the ask in place: the user
// has not seen the page yet.
func (a *shelf) openSupport() {
	if err := openExternal(supportURL); err != nil {
		a.report(err)
		return
	}
	if a.companion != nil {
		if err := a.companion.MarkClicked(); err != nil {
			a.report(err)
		}
	}
	a.winInvalidate()
	a.repaintSettings()
}

// companionStart is the first day for a launch that found no companion
// file: today on a fresh install, the data folder's birth date on an
// existing one, and today when the folder has no birth date. born is nil
// when the platform cannot answer. The date is only ever a local calendar
// date.
func companionStart(freshInstall bool, now time.Time, born func() (time.Time, bool)) time.Time {
	if freshInstall || born == nil {
		return now
	}
	if b, ok := born(); ok && !b.IsZero() {
		return b
	}
	return now
}

// companionBorn answers with the data folder's creation date on this Mac.
var companionBorn = dataDirBorn

// loadCompanion opens the companion file and gives a new install its first
// day. A file that cannot be read stays untouched and this run simply shows
// no days. An absent one is written once, here, so every later launch knows
// the same first day even if the folder's dates change.
func loadCompanion(dir string, freshInstall bool, now time.Time) (*companion.Store, error) {
	s, err := companion.Open(dir)
	if err != nil {
		return s, err
	}
	if !s.Seeded() {
		if err := s.Seed(companionStart(freshInstall, now, func() (time.Time, bool) {
			if dir == "" {
				return time.Time{}, false
			}
			return companionBorn(dir)
		})); err != nil {
			return s, err
		}
	}
	return s, nil
}
