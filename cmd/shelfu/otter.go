//go:build darwin

package main

// The brand otter, as static art. The pictures live outside the repository
// (resources/otter/, ignored by git: the brand artwork is local only), so a
// build from a public checkout — or any build with the folder missing —
// decodes nothing. Every caller falls back: no otter, or the existing glyph
// it used before. A missing picture is never an error.
//
// Two families live here. The eight ui_* pictures are busts with a white
// background for the panel's own surfaces. The pet_* pictures are the
// full-body desktop otter, all drawn on one canvas (491 × 640, feet on the
// same baseline, body on the same centre line) so swapping poses never
// makes it jump. The two families are never mixed on the desktop: a bust
// where a whole body stands reads as a different character.
//
// A pose may be missing while a cheaper one is present: every pet pose has
// a fallback (petSleep → petIdle → petWait) so shipping new artwork is
// dropping files in, never a code change. Nothing here animates: a new
// state is a different picture, nothing more.

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// Picture names. The ui_* ones are the panel's busts; the pet ones are the
// desktop otter's poses.
const (
	otterNeutral     = "neutral"
	otterHappy       = "happy"
	otterSurprised   = "surprised"
	otterSleepy      = "sleepy"
	otterCurious     = "curious"
	otterEmbarrassed = "embarrassed"
	otterFront       = "front"
	otterHead        = "head"

	petIdle     = "pet_idle"      // standing about
	petSleep    = "pet_sleep"     // asleep after ten quiet minutes
	petHappy    = "pet_happy"     // something good just happened
	petStartled = "pet_surprised" // a drag is crossing the shelf
	petLifted   = "pet_lifted"    // held by the scruff, being dragged
	petWait     = "pet_catch_wait"
	petReady    = "pet_catch_ready"
	petCaught   = "pet_caught"
)

// otterFiles maps an accepted name to its file's stem: the file is
// <prefix>otter_<stem>_v001.png, where the prefix is the build's own name.
// Each pose points at its own file; the ones the build does not ship are
// filled in by otterFallbacks, so adding a picture is dropping it in —
// never a mapping to edit.
var otterFiles = map[string]string{
	otterNeutral:     "ui_neutral",
	otterHappy:       "ui_happy",
	otterSurprised:   "ui_surprised",
	otterSleepy:      "ui_sleepy",
	otterCurious:     "ui_curious",
	otterEmbarrassed: "ui_embarrassed",
	otterFront:       "ui_front",
	otterHead:        "ui_head",

	petIdle:     "pet_idle",
	petSleep:    "pet_sleep",
	petHappy:    "pet_happy",
	petStartled: "pet_surprised",
	petLifted:   "pet_lifted",
	petWait:     "pet_catch_wait",
	petReady:    "pet_catch_ready",
	petCaught:   "pet_caught",
}

// otterFallbacks is the pose ladder: a picture that is not on disk is
// asked for as a cheaper pose instead. petIdle is the resting pose and
// petWait the one every other pet pose stands in for — the two files a
// public build may ship first.
var otterFallbacks = map[string]string{
	petIdle:     petWait,
	petSleep:    petIdle,
	petHappy:    petIdle,
	petStartled: petIdle,
	petReady:    petIdle,
	petLifted:   petIdle,
	petCaught:   petIdle,
}

// otterPrefixes are the names the artwork may have been installed under:
// the current build writes shelfu-*, and the otter's pictures kept from
// the earlier build are named rakko-*. Both are local files, never in git.
var otterPrefixes = []string{"shelfu-", "rakko-"}

var (
	otterMu     sync.Mutex
	otterCache  = map[string]*ui.Bitmap{}
	otterMisses = map[string]bool{}
)

// otterDir is where the pictures are found; tests point it at a temp folder.
var otterDir = func() string {
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		return filepath.Join(dir, "otter")
	}
	return ""
}

// otterImage returns the named picture, decoding it once. A name with no
// file of its own walks otterFallbacks, so a build that ships only the
// resting poses still draws every state. nil when the name is unknown or
// nothing on the ladder decodes.
func otterImage(name string) *ui.Bitmap {
	for range 4 {
		stem, known := otterFiles[name]
		if !known {
			return nil
		}
		if b := otterBitmap(stem); b != nil {
			return b
		}
		next, ok := otterFallbacks[name]
		if !ok {
			return nil
		}
		name = next
	}
	return nil
}

// otterBitmap decodes one file stem under either installed prefix,
// remembering misses: a pose the build does not ship costs one failed stat
// per prefix, not one per frame.
func otterBitmap(stem string) *ui.Bitmap {
	otterMu.Lock()
	defer otterMu.Unlock()
	if b, ok := otterCache[stem]; ok {
		return b
	}
	if otterMisses[stem] {
		return nil
	}
	dir := otterDir()
	if dir == "" {
		otterMisses[stem] = true
		return nil
	}
	for _, prefix := range otterPrefixes {
		data, err := os.ReadFile(filepath.Join(dir, prefix+"otter_"+stem+"_v001.png"))
		if err != nil {
			continue
		}
		b, err := ui.DecodeBitmap(data)
		if err != nil {
			continue
		}
		otterCache[stem] = b
		return b
	}
	otterMisses[stem] = true
	return nil
}

// petArt reports whether this build draws the desktop otter at all: the
// resting pose's file, or anything standing in for it. A build without the
// artwork never creates the window, never shows the setting, and never
// complains.
func petArt() *ui.Bitmap { return otterImage(petIdle) }

// resetOtters drops the decoded pictures and the miss marks: a test that
// puts resources in place after an earlier miss calls it.
func resetOtters() {
	otterMu.Lock()
	defer otterMu.Unlock()
	otterCache = map[string]*ui.Bitmap{}
	otterMisses = map[string]bool{}
}
