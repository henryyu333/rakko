//go:build darwin

package main

// The home search's 便利贴 group: the notes' text lives in memory, so the
// group appears with the clipboard's on the first keystroke instead of
// after a file read. The index is rebuilt when the notes themselves
// change — a save, a change another editor made, a search field that just
// took the keyboard — and never while typing.

import (
	"rakko/internal/note"
)

// noteIndex is the notes' text as the last scan read it.
type noteIndex struct {
	docs  []note.Doc
	built bool // a scan ran; stale says another has to
	stale bool
}

// noteDocs returns the notes the group searches, scanning the folder only
// when something invalidated the index.
func (a *shelf) noteDocs() []note.Doc {
	if a.dataDir == "" {
		return nil
	}
	if a.noteIdx.built && !a.noteIdx.stale {
		return a.noteIdx.docs
	}
	docs, err := note.Docs(a.dataDir)
	if err != nil {
		a.report(err)
		docs = a.noteIdx.docs // the notes the last good scan found
	}
	a.noteIdx = noteIndex{docs: docs, built: true}
	return docs
}

// noteStale asks for the next search to scan the notes again: a save, an
// outside change and a search field taking the keyboard all call it.
func (a *shelf) noteStale() { a.noteIdx.stale = true }

// noteHits is the group's rows for q: the notes that contain it, a heading
// hit before a body hit and the newest first — exactly the note.Search
// order, cut to the group's rows by homeHits. Only a body hit carries a
// second line: the matching line, since the heading already says the rest.
func (a *shelf) noteHits(docs []note.Doc, q string) []homeHit {
	hits := note.Search(docs, q)
	if len(hits) == 0 {
		return nil
	}
	group := a.appLoc().HomeGroupNotes()
	out := make([]homeHit, 0, len(hits))
	for _, h := range hits {
		id := h.ID
		sub := ""
		if h.Line != "" {
			sub = clipUnits(h.Line, 40)
		}
		out = append(out, homeHit{group: group, icon: noteSymbol, title: h.Title, sub: sub,
			run: func() { a.openNote(id) }})
	}
	return out
}

// openNoteFront is the note coming up after ⏎; tests replace it so no
// window has to open. Set in init: a var initializer naming the note's
// window would close an initialization cycle through the search paths.
var openNoteFront func(*noteUI) bool

func init() { openNoteFront = (*noteUI).showFront }

// openNote brings a note into view where it lives: the card's note becomes
// that one when it is not, and its window comes up with the keyboard — the
// same window a tap on the card opens, never a second surface.
func (a *shelf) openNote(id string) {
	n := a.noteSession()
	if n == nil {
		return
	}
	if err := n.selectNote(id); err != nil {
		a.report(err)
		return
	}
	a.hide(hideExplicit)
	openNoteFront(n)
}
