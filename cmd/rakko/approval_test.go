//go:build darwin

package main

// The approval card's rules: arrival, queue, preemption, the hold, the
// keyboard's two-step allow, deny reasons, timeouts and withdrawal. The
// socket and the hook client are covered in internal/agentnotify; here the
// card and its decisions are tested through the live queue.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rakko/internal/agentnotify"
	"rakko/locales"
)

func approvalTestShelf(t *testing.T) (*shelf, *approvalFeature) {
	t.Helper()
	a, _, _ := agentShelf(t)
	a.settings.Approval.Enabled = true
	f := a.approvalFeature()
	f.enabled.Store(true)
	oldReg, oldUnreg := registerKey, unregisterKey
	registered := map[string]func(){}
	registerKey = func(acc string, fn func()) error {
		registered[acc] = fn
		return nil
	}
	unregisterKey = func(acc string) { delete(registered, acc) }
	t.Cleanup(func() { registerKey, unregisterKey = oldReg, oldUnreg })
	return a, f
}

func testAsk(id, tool, input string) agentnotify.Ask {
	return agentnotify.Ask{
		V: 2, Kind: "ask", ID: id, Tool: agentnotify.Tool(tool),
		Event: "PermissionRequest", Session: "session", Cwd: "/tmp/rakko-demo",
		ToolName: "Bash", Input: json.RawMessage(input),
	}
}

func testApproval(t *testing.T, id, tool, input string) *approval {
	t.Helper()
	ask := testAsk(id, tool, input)
	an, ok := agentnotify.Analyze(ask.Tool, ask.ToolName, ask.Input)
	if !ok {
		t.Fatal("analysis failed")
	}
	now := time.Now()
	return &approval{
		id: id, ask: ask, analysis: an,
		done:     make(chan agentnotify.Decision, 1),
		at:       now,
		deadline: now.Add(approvalWait),
		state:    approvalShown,
	}
}

func testID(id int) string { return fmt.Sprintf("%032d", id) }

func TestApprovalCardRoundTrip(t *testing.T) {
	a, f := approvalTestShelf(t)
	ap := testApproval(t, testID(1), "claude", `{"command":"rm -rf build"}`)
	f.enqueue(ap)
	if ev, ok := a.liveNow(time.Now(), true); !ok || ev.kind != liveAgentAsk || ev.ask != ap {
		t.Fatalf("live event %+v ok=%v", ev, ok)
	}
	go f.decide(ap.id, agentnotify.DecisionAllow, "")
	d := f.wait(context.Background(), ap)
	if d.ID != ap.id || d.Decision != agentnotify.DecisionAllow {
		t.Fatalf("decision %+v", d)
	}
	if ap.state != approvalAllowed {
		t.Fatalf("card state %v, want allowed", ap.state)
	}
}

func TestApprovalPanelPreempts(t *testing.T) {
	a, f := approvalTestShelf(t)
	a.visible = true
	a.pushLive(liveEvent{kind: liveAgentDone, agent: "Codex", task: "Done", tool: agentnotify.ToolCodex},
		liveHoldAgent, time.Now())
	ap := testApproval(t, testID(2), "claude", `{"command":"ls"}`)
	if !f.enqueue(ap) {
		t.Fatal("enqueue refused")
	}
	if a.visible {
		t.Fatal("the panel did not fold for the card")
	}
	if ev, ok := a.liveNow(time.Now(), true); !ok || ev.kind != liveAgentAsk {
		t.Fatalf("the card is not on screen: %+v ok=%v", ev, ok)
	}
	f.decide(ap.id, agentnotify.DecisionAllow, "")
	a.liveNow(time.Now().Add(2*time.Second), true)
	if a.visible {
		t.Fatal("the panel reopened after the answer")
	}
}

func TestApprovalEOFWithdraws(t *testing.T) {
	a, f := approvalTestShelf(t)
	ctx, cancel := context.WithCancel(context.Background())
	ap := testApproval(t, testID(3), "claude", `{"command":"ls"}`)
	f.enqueue(ap)
	a.liveNow(time.Now(), true)
	cancel()
	if d := f.wait(ctx, ap); d.Decision != agentnotify.DecisionPass {
		t.Fatalf("withdrawn decision %q", d.Decision)
	}
	if ev, ok := a.liveNow(time.Now(), true); ok {
		t.Fatalf("the withdrawn card is still drawn: %+v", ev)
	}
	if a.live.cur.ask != nil {
		t.Fatal("the withdrawn card is still the current event")
	}
}

func TestApprovalQueue(t *testing.T) {
	a, f := approvalTestShelf(t)
	first := testApproval(t, testID(10), "claude", `{"command":"ls"}`)
	second := testApproval(t, testID(11), "codex", `{"command":"pwd"}`)
	third := testApproval(t, testID(12), "qwen", `{"command":"date"}`)
	for _, x := range []*approval{first, second, third} {
		if !f.enqueue(x) {
			t.Fatal("enqueue refused")
		}
	}
	now := time.Now()
	if ev, ok := a.liveNow(now, true); !ok || ev.ask != first {
		t.Fatalf("first card %+v", ev)
	}
	if n := f.queueCount(first.id); n != 2 {
		t.Fatalf("queue count %d, want 2", n)
	}
	f.decide(first.id, agentnotify.DecisionAllow, "")
	now = now.Add(approvalResultHold + time.Millisecond)
	if ev, ok := a.liveNow(now, true); !ok || ev.ask != second {
		t.Fatalf("second card %+v ok=%v", ev, ok)
	}
	if n := f.queueCount(second.id); n != 1 {
		t.Fatalf("queue count %d, want 1", n)
	}
}

func TestApprovalExpired(t *testing.T) {
	a, f := approvalTestShelf(t)
	ap := testApproval(t, testID(20), "claude", `{"command":"ls"}`)
	ap.deadline = time.Now().Add(150 * time.Millisecond)
	f.enqueue(ap)
	a.liveNow(time.Now(), true)
	if d := f.wait(context.Background(), ap); d.Decision != agentnotify.DecisionPass {
		t.Fatalf("expired decision %q, want pass", d.Decision)
	}
	if ap.state != approvalExpired {
		t.Fatalf("card state %v, want expired", ap.state)
	}
	if ev, ok := a.liveNow(time.Now().Add(approvalExpiredHold+time.Second), true); ok {
		t.Fatalf("the expired card outlived its result: %+v", ev)
	}
}

func TestApprovalDisabledPasses(t *testing.T) {
	_, f := approvalTestShelf(t)
	f.enabled.Store(false)
	d := f.ask(context.Background(), testAsk(testID(21), "claude", `{"command":"ls"}`))
	if d.Decision != agentnotify.DecisionPass {
		t.Fatalf("decision %q, want pass", d.Decision)
	}
}

func TestApprovalHold(t *testing.T) {
	a, f := approvalTestShelf(t)
	risky := testApproval(t, testID(30), "claude", `{"command":"rm -rf build"}`)
	if !f.enqueue(risky) {
		t.Fatal("enqueue refused")
	}
	now := time.Now()
	a.liveNow(now, true)
	risky.state = approvalHolding
	risky.holdFrom = now.Add(-300 * time.Millisecond)
	if f.holdTick(risky, now) {
		t.Fatal("a 300 ms hold decided")
	}
	risky.holdRelease()
	if risky.state != approvalShown {
		t.Fatalf("release left state %v", risky.state)
	}
	risky.state = approvalHolding
	risky.holdFrom = now.Add(-approvalHoldDelay)
	if !f.holdTick(risky, now) {
		t.Fatal("a full hold did not decide")
	}
	if d := <-risky.done; d.Decision != agentnotify.DecisionAllow {
		t.Fatalf("hold decision %q", d.Decision)
	}
}

func TestApprovalKeyboardGate(t *testing.T) {
	a, f := approvalTestShelf(t)
	plain := testApproval(t, testID(40), "claude", `{"command":"ls"}`)
	f.enqueue(plain)
	a.liveNow(time.Now(), true)
	f.keyAllow()
	if d := <-plain.done; d.Decision != agentnotify.DecisionAllow {
		t.Fatalf("plain keyboard decision %q", d.Decision)
	}

	risky := testApproval(t, testID(41), "claude", `{"command":"rm -rf build"}`)
	f.enqueue(risky)
	a.liveNow(time.Now().Add(2*time.Second), true)
	f.keyAllow()
	if risky.state != approvalArmed {
		t.Fatalf("first press left state %v, want armed", risky.state)
	}
	select {
	case d := <-risky.done:
		t.Fatalf("the first press decided: %+v", d)
	default:
	}
	f.keyAllow()
	if d := <-risky.done; d.Decision != agentnotify.DecisionAllow {
		t.Fatalf("second press decision %q", d.Decision)
	}
}

func TestApprovalKeyboardArmedExpires(t *testing.T) {
	a, f := approvalTestShelf(t)
	risky := testApproval(t, testID(42), "claude", `{"command":"rm -rf build"}`)
	f.enqueue(risky)
	now := time.Now()
	a.liveNow(now, true)
	f.keyAllow()
	risky.armedUntil = now.Add(-time.Second)
	f.keyAllow()
	if risky.state != approvalShown {
		t.Fatalf("an expired armed state left %v, want shown", risky.state)
	}
	select {
	case d := <-risky.done:
		t.Fatalf("an expired armed state decided: %+v", d)
	default:
	}
}

func TestApprovalDenyWithReason(t *testing.T) {
	_, f := approvalTestShelf(t)
	ap := testApproval(t, testID(50), "claude", `{"command":"ls"}`)
	f.enqueue(ap)
	f.decide(ap.id, agentnotify.DecisionDeny, "  先别删,\n留着我排查  ")
	d := <-ap.done
	if d.Decision != agentnotify.DecisionDeny || d.Message != "先别删, 留着我排查" {
		t.Fatalf("deny decision %+v", d)
	}
	empty := testApproval(t, testID(51), "claude", `{"command":"ls"}`)
	f.enqueue(empty)
	f.decide(empty.id, agentnotify.DecisionDeny, "  ")
	if d := <-empty.done; d.Decision != agentnotify.DecisionDeny || d.Message != "" {
		t.Fatalf("empty reason decision %+v", d)
	}
}

func TestApprovalAge(t *testing.T) {
	now := time.Now()
	ap := testApproval(t, testID(60), "claude", `{"command":"ls"}`)
	l := locales.In("zh-Hans")
	for _, tc := range []struct {
		age  int
		want string
	}{{0, "已等 0 秒"}, {29, "已等 29 秒"}, {30, "已等 30 秒"}, {44, "已等 44 秒"}} {
		ap.at = now.Add(-time.Duration(tc.age) * time.Second)
		line := approvalStatusLine(l, ap, now)
		if !strings.Contains(line, tc.want) {
			t.Errorf("age %d: %q does not carry %q", tc.age, line, tc.want)
		}
	}
	if approvalAgeColor(29) == approvalWarnText || approvalAgeColor(30) != approvalWarnText {
		t.Fatal("the 30 second warning colour is wrong")
	}
}

func TestApprovalApplyIndependent(t *testing.T) {
	a, home, _ := agentShelf(t)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	a.setAgentNotify(true)
	claude := filepath.Join(home, ".claude", "settings.json")
	raw, err := os.ReadFile(claude)
	if err != nil || !strings.Contains(string(raw), "--agent-hook --tool claude") {
		t.Fatalf("completion entry missing: %v\n%s", err, raw)
	}
	a.setApproval(true)
	raw, _ = os.ReadFile(claude)
	if !strings.Contains(string(raw), "--agent-approve --tool claude") {
		t.Fatalf("approval entry missing:\n%s", raw)
	}
	a.setApproval(false)
	raw, _ = os.ReadFile(claude)
	if strings.Contains(string(raw), "--agent-approve --tool claude") {
		t.Fatalf("approval entry survived the switch:\n%s", raw)
	}
	if !strings.Contains(string(raw), "--agent-hook --tool claude") {
		t.Fatalf("the approval switch removed the completion entry:\n%s", raw)
	}
}

func TestApprovalSwitchOffPassesWaiting(t *testing.T) {
	a, f := approvalTestShelf(t)
	ap := testApproval(t, testID(80), "claude", `{"command":"ls"}`)
	f.enqueue(ap)
	a.setApproval(false)
	if d := <-ap.done; d.Decision != agentnotify.DecisionPass {
		t.Fatalf("switch-off decision %q", d.Decision)
	}
	if ev, ok := a.liveNow(time.Now(), true); ok {
		t.Fatalf("card survived switch-off: %+v", ev)
	}
}

func TestApprovalCardSize(t *testing.T) {
	a, _ := approvalTestShelf(t)
	plain := testApproval(t, testID(90), "claude", `{"command":"ls"}`)
	w, h := a.approvalCardSize(plain)
	if w != 360 || h != 158 {
		t.Fatalf("plain card %0.fx%0.f, want 360x158", w, h)
	}
	risky := testApproval(t, testID(91), "claude", `{"command":"rm -rf build"}`)
	if _, h := a.approvalCardSize(risky); h != 184 {
		t.Fatalf("risky card height %0.f, want 184", h)
	}
	long := testApproval(t, testID(92), "claude", `{"command":"ls","description":"`+strings.Repeat("长", 100)+`"}`)
	if _, h := a.approvalCardSize(long); h != 176 {
		t.Fatalf("two-line card height %0.f, want 176", h)
	}
	a.island = true
	if _, h := a.approvalCardSize(plain); h != 132 {
		t.Fatalf("island card height %0.f, want 132", h)
	}
}

func TestApprovalDuplicateReplacesOld(t *testing.T) {
	_, f := approvalTestShelf(t)
	first := testApproval(t, testID(70), "claude", `{"command":"ls"}`)
	second := testApproval(t, testID(71), "claude", `{"command":"ls"}`)
	second.at = first.at.Add(500 * time.Millisecond)
	f.enqueue(first)
	f.enqueue(second)
	if d := <-first.done; d.Decision != agentnotify.DecisionPass {
		t.Fatalf("the duplicate was not passed back: %+v", d)
	}
	if n := f.queueCount(second.id); n != 0 {
		t.Fatalf("queue count %d after dedupe", n)
	}
}
