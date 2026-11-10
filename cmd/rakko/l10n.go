//go:build darwin

package main

// Every string the interface says lives in the catalogs of locales/, in the
// source language zh-Hans and in English. Views read the messages of the
// language of their window, which mygo redraws when it changes; native
// surfaces (the tray, its menus and dialogs) read the language the shelf
// shows, which OnLocaleChanged keeps up to date.

import (
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/i18n"
	"github.com/egoist/mygo/ui"

	"rakko/locales"
)

// loc returns the messages of the language of c's window. A view that is
// built again every frame follows a language change with no restart; a
// headless Tester's SetLocale drives what a test reads.
func loc(c *ui.Context) locales.Messages { return locales.In(c.Locale()) }

// tloc returns the messages of the language of c's window, formatted the way
// the user's regional settings say: dates and times follow the 12- or
// 24-hour clock and the first day of the week, whatever the language. A
// headless Tester has no region, so a test sees its language's CLDR default.
func tloc(c *ui.Context) locales.Messages {
	tag, r := c.Locale(), c.Region()
	if tag == "" || r == (i18n.Region{}) {
		return locales.In(tag)
	}
	return locales.In(r.FormatLocale(tag))
}

// stampIn is a moment in a clipboard row's title: the compact 「01-02 15:04」
// the source language has always shown, or the region's short date and time.
func stampIn(locale string, r i18n.Region, t time.Time) string {
	if strings.HasPrefix(locale, "zh") {
		return t.Format("01-02 15:04")
	}
	return i18n.FormatDate(r.FormatLocale(locale), t, i18n.DateOptions{
		Date: i18n.ShortStyle, Time: i18n.ShortStyle, HourCycle: r.HourCycle,
	})
}

// appLoc returns the messages of the language the shelf shows, for the
// native surfaces built outside a view: the tray, its menus and dialogs.
// A headless shelf keeps the source language until a test sets a.locale.
func (a *shelf) appLoc() locales.Messages { return locales.In(a.locale) }

// languageTag returns tag when it names one of the app's languages, else ""
// (follow the system): settings.json is user-editable, and mygo.App.SetLocale
// panics on a tag that names no language.
func languageTag(tag string) string {
	switch tag {
	case "zh-Hans", "en":
		return tag
	}
	return ""
}

// preferredLanguages says the system's languages, most preferred first, as
// the user orders them in System Settings. A test replaces it to say what a
// system would ask for.
var preferredLanguages = mygo.App.PreferredLanguages

// resolveLanguage is the one rule that picks the shelf's language: a language
// settings.json names is shown exactly as it says, never overridden; "" (follow
// the system) reads the system's first preferred language only, and shows
// Chinese to a Chinese first language (zh-Hans, zh-Hant, zh-TW, zh-HK…) and
// English to every other. mygo alone matches the whole list and falls back to
// the source language, so it reads a second language ([fr-FR, zh-Hans-CN]) as
// Chinese and a Traditional Chinese system as English; this rule does not.
func resolveLanguage(setting string, preferred []string) string {
	if tag := languageTag(setting); tag != "" {
		return tag
	}
	if len(preferred) > 0 {
		if lang := i18n.Normalize(preferred[0]); isChinese(lang) {
			return "zh-Hans"
		}
	}
	return "en"
}

// isChinese reports whether a BCP 47 tag names Chinese in any script or
// region: "zh", "zh-Hans", "zh-Hant-TW".
func isChinese(tag string) bool {
	return tag == "zh" || strings.HasPrefix(tag, "zh-")
}

// showLanguage shows the language settings.json names, or the one the system
// chooses when it follows the system, and returns it. The launch and a switch
// in Settings both run it, so the two cannot disagree: an explicit language is
// shown as it is, and Follow System reads the same rule as a launch does — not
// the source language mygo falls back to.
func (a *shelf) showLanguage() string {
	tag := resolveLanguage(a.settings.Language, preferredLanguages())
	setAppLocale(tag)
	return tag
}

// followSystemLanguage re-applies Follow System once the system's languages
// may have changed. mygo keeps the language the app chose (SetLocale) and
// reports a change of the user's languages only while the app follows them, so
// the shelf re-resolves when it becomes active — the moment the user comes
// back from System Settings, and the moment the window can show it.
func (a *shelf) followSystemLanguage() {
	// An explicit language wins; a shelf without a window (a headless view) or
	// that has not heard its language yet (before ready) has nothing to
	// re-resolve and nothing to draw it in.
	if a.settings.Language != "" || a.win == nil || a.locale == "" {
		return
	}
	if tag := resolveLanguage("", preferredLanguages()); tag != a.locale {
		a.showLanguage()
	}
}

// localeChanged follows the app's language as mygo changes it: the native
// surfaces that are built once — window titles and the tray's tooltip — say
// the new language; views redraw themselves because they are built per
// frame. It runs on the main thread, also when the app is not ready yet, so
// every window is checked.
func (a *shelf) localeChanged(tag string) {
	a.locale = tag
	m := a.appLoc()
	if a.tray != nil {
		a.tray.SetToolTip(m.AppTrayTooltip())
	}
	a.retitleWindows()
}

// retitleWindows gives the windows that outlive a language change their
// title in the language the shelf now says.
func (a *shelf) retitleWindows() {
	m := a.appLoc()
	if a.settingsWin != nil {
		a.settingsWin.SetTitle(m.WindowSettings())
	}
	if a.basket.win != nil {
		a.basket.win.SetTitle(m.WindowBasket())
	}
	if a.updateWin != nil {
		a.updateWin.SetTitle(a.updateWindowTitle())
	}
	for _, p := range a.pins {
		if p.win != nil {
			p.win.SetTitle(m.WindowPins())
		}
	}
	for _, isl := range a.idleIslands {
		if isl.win != nil {
			isl.win.SetTitle(m.WindowIsland())
		}
	}
	if a.mainIdle != nil && a.mainIdle.win != nil {
		a.mainIdle.win.SetTitle(m.WindowIsland())
	}
	if a.rn != nil && a.rn.win != nil {
		a.rn.win.SetTitle(a.renameTitle(len(a.rn.items)))
	}
}
