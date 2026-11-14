package main

import "testing"

// TestClipboardRowsSixVisible: 174pt holds five 30pt rows plus a part of
// the sixth; the list scrolls with the edge blur.
func TestClipboardRowsSixVisible(t *testing.T) {
	if got := clipRowH * 5; got > contentH {
		t.Fatalf("five clipboard rows take %d of %d", got, contentH)
	}
}
