package main

// The deadline and a click can land in the same instant. Exactly one of
// them may decide: whichever commits first is the answer the hook is given
// and the state the card keeps. These tests drive both orders without
// waiting on a clock: the deadline is already due, and the main-thread
// callback stands in for what the UI does in that instant.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"rakko/internal/agentnotify"
	"rakko/locales"
)

// deadlineFeature is the approval feature alone, with a synchronous main
// thread, so each ending can be driven to the exact moment it matters.
func deadlineFeature(t *testing.T) *approvalFeature {
	t.Helper()
	old := agentRunOnMain
	agentRunOnMain = func(fn func()) { fn() }
	t.Cleanup(func() { agentRunOnMain = old })
	return &approvalFeature{host: &shelf{}, ids: map[string]*approval{}}
}

// deadlineApproval is one card whose deadline has already passed.
func deadlineApproval(t *testing.T, id string) *approval {
	t.Helper()
	ask := agentnotify.Ask{
		V: 2, Kind: "ask", ID: id, Tool: agentnotify.ToolClaude,
		Event: "PermissionRequest", Session: "deadline", Cwd: "/tmp/rakko-deadline",
		ToolName: "Bash", Input: json.RawMessage(`{"command":"ls"}`),
	}
	an, ok := agentnotify.Analyze(ask.Tool, ask.ToolName, ask.Input)
	if !ok {
		t.Fatal("analysis failed")
	}
	now := time.Now()
	return &approval{
		id: id, ask: ask, analysis: an,
		done:     make(chan agentnotify.Decision, 1),
		at:       now,
		deadline: now.Add(-time.Second),
		state:    approvalShown,
	}
}

// TestApprovalClickWinsAtTheDeadline: the click commits a hair before the
// deadline callback runs. The timeout must then leave the card alone and
// report the click's decision, not its own pass.
func TestApprovalClickWinsAtTheDeadline(t *testing.T) {
	f := deadlineFeature(t)
	ap := deadlineApproval(t, "0123456789abcdef0123456789abcdef")
	f.ids[ap.id] = ap
	// The main thread runs the click, then the deadline's own callback.
	syncMain := agentRunOnMain
	agentRunOnMain = func(fn func()) {
		agentRunOnMain = syncMain
		f.decide(ap.id, agentnotify.DecisionAllow, "")
		fn()
	}
	d := f.wait(context.Background(), ap)
	if d.ID != ap.id || d.Decision != agentnotify.DecisionAllow {
		t.Fatalf("the hook was told %+v while the click allowed the call", d)
	}
	if ap.state != approvalAllowed {
		t.Fatalf("the card shows %v, the hook was told %q", ap.state, d.Decision)
	}
	if label, ok := approvalResultLabel(locales.In("zh-Hans"), ap.state); !ok || label != locales.In("zh-Hans").LiveApprovalAllowed() {
		t.Fatalf("the card's result is %q (ok=%v), not the allow it delivered", label, ok)
	}
}

// TestApprovalDeadlineWinsOverLateClick: the deadline commits first. The
// click that follows is ignored, and the card keeps the expired state the
// hook was handed.
func TestApprovalDeadlineWinsOverLateClick(t *testing.T) {
	f := deadlineFeature(t)
	ap := deadlineApproval(t, "fedcba9876543210fedcba9876543210")
	f.ids[ap.id] = ap
	d := f.wait(context.Background(), ap)
	if d.ID != ap.id || d.Decision != agentnotify.DecisionPass {
		t.Fatalf("the expired hook was told %+v, want pass", d)
	}
	if ap.state != approvalExpired {
		t.Fatalf("the card shows %v after the deadline, want expired", ap.state)
	}
	// The click lands after the timeout: it must not decide anything, in
	// the card or for the hook.
	f.decide(ap.id, agentnotify.DecisionAllow, "")
	if ap.state != approvalExpired {
		t.Fatalf("a late click changed the card to %v", ap.state)
	}
	select {
	case late := <-ap.done:
		if late.Decision != agentnotify.DecisionPass {
			t.Fatalf("a late click reached the hook: %+v", late)
		}
	default:
		t.Fatal("the expired card left no decision for the hook")
	}
	if label, ok := approvalResultLabel(locales.In("zh-Hans"), ap.state); !ok || label != locales.In("zh-Hans").LiveApprovalExpired() {
		t.Fatalf("the card's result is %q (ok=%v), not the expiry it delivered", label, ok)
	}
}
