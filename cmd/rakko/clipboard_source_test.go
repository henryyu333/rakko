//go:build darwin

package main

import (
	"testing"
	"time"
)

// TestClipboardSourceBeforeFirstSwitch: a copy followed by an app switch
// before the next poll belongs to the app the user copied from.
func TestClipboardSourceBeforeFirstSwitch(t *testing.T) {
	now := time.Now()
	m := &clipboardModule{
		tracked: true,
		front:   "com.example.Editor", lastPoll: now.Add(-100 * time.Millisecond),
		active: []clipActivation{{at: now.Add(-40 * time.Millisecond), prev: "com.example.Editor", next: "com.example.Browser"}},
	}
	m.front = "com.example.Browser"
	if got := m.sourceFor(now); got != "com.example.Editor" {
		t.Fatalf("source = %q, want the app before the switch", got)
	}
}

// TestClipboardSourceWithoutSwitch: no activation in the window leaves the
// frontmost app as the source.
func TestClipboardSourceWithoutSwitch(t *testing.T) {
	now := time.Now()
	m := &clipboardModule{tracked: true, front: "com.example.Editor", lastPoll: now.Add(-100 * time.Millisecond)}
	if got := m.sourceFor(now); got != "com.example.Editor" {
		t.Fatalf("source = %q, want the frontmost app", got)
	}
}

// TestClipboardSourceSwitchBeforePollWindow: a switch seen by an earlier
// poll is already attributed; the next copy belongs to the current app.
func TestClipboardSourceSwitchBeforePollWindow(t *testing.T) {
	now := time.Now()
	m := &clipboardModule{
		tracked: true,
		front:   "com.example.Browser", lastPoll: now.Add(-10 * time.Millisecond),
		active: []clipActivation{{at: now.Add(-500 * time.Millisecond), prev: "com.example.Editor", next: "com.example.Browser"}},
	}
	if got := m.sourceFor(now); got != "com.example.Browser" {
		t.Fatalf("source = %q, want the current app", got)
	}
}

// TestClipboardSourceConsecutiveSwitches: the app before the first switch
// in the window owns the copy, even when the user kept switching.
func TestClipboardSourceConsecutiveSwitches(t *testing.T) {
	now := time.Now()
	m := &clipboardModule{
		tracked: true,
		front:   "com.example.Terminal", lastPoll: now.Add(-100 * time.Millisecond),
		active: []clipActivation{
			{at: now.Add(-60 * time.Millisecond), prev: "com.example.Editor", next: "com.example.Browser"},
			{at: now.Add(-30 * time.Millisecond), prev: "com.example.Browser", next: "com.example.Terminal"},
		},
	}
	if got := m.sourceFor(now); got != "com.example.Editor" {
		t.Fatalf("source = %q, want the first app before the switches", got)
	}
}

// TestClipboardSourceAdvanceDropsSeenSwitches: once a poll closes the
// window, its activations cannot attribute a later copy.
func TestClipboardSourceAdvanceDropsSeenSwitches(t *testing.T) {
	now := time.Now()
	m := &clipboardModule{
		tracked: true,
		front:   "com.example.Browser", lastPoll: now.Add(-100 * time.Millisecond),
		active: []clipActivation{{at: now.Add(-50 * time.Millisecond), prev: "com.example.Editor", next: "com.example.Browser"}},
	}
	m.advancePoll(now)
	if len(m.active) != 0 {
		t.Fatalf("old activations survived: %v", m.active)
	}
	if got := m.sourceFor(now.Add(time.Millisecond)); got != "com.example.Browser" {
		t.Fatalf("source = %q, want the current app", got)
	}
}

// TestClipboardSourceSelfCopyHasNoSource: a copy made while Rakko itself
// was frontmost records no source, matching the old direct comparison.
func TestClipboardSourceSelfCopyHasNoSource(t *testing.T) {
	now := time.Now()
	m := &clipboardModule{
		tracked: true, front: "com.example.Editor", lastPoll: now.Add(-100 * time.Millisecond),
		active: []clipActivation{{at: now.Add(-40 * time.Millisecond), prev: "", next: "com.example.Editor"}},
	}
	if got := m.sourceFor(now); got != "" {
		t.Fatalf("source = %q, want empty for a self copy", got)
	}
}
