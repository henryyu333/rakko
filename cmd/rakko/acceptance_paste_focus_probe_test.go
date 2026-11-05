//go:build darwin

package main

import "testing"

func TestAcceptancePasteMustNotReactivateAfterUserSwitch(t *testing.T) {
	h := newPasteHooks(t)
	h.front = 777
	a, _, _ := pasteShelf(t, "must stay copied")
	a.pasteClipSelection()
	h.runWork()
	if len(h.activated) != 0 {
		t.Fatalf("user switched to PID 777, but pending paste requested activation of %v", h.activated)
	}
	if len(h.pastes.sent) != 0 {
		t.Fatalf("paste was sent after user switched away: %v", h.pastes.sent)
	}
}
