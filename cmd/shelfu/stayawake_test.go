//go:build darwin

package main

import (
	"errors"
	"testing"
	"time"

	"shelf/internal/mac"
)

// fakePower replaces the IOKit calls: it hands out fresh ids, records the
// released ones and can be told to fail creation.
type fakePower struct {
	live  map[mac.PowerAssertionID]bool
	next  mac.PowerAssertionID
	fail  error // create returns this while set
	kinds []mac.PowerAssertionKind
}

func newFakePower() *fakePower {
	return &fakePower{live: map[mac.PowerAssertionID]bool{}, next: 1}
}

func (f *fakePower) create(kind mac.PowerAssertionKind) (mac.PowerAssertionID, error) {
	if f.fail != nil {
		return 0, f.fail
	}
	f.kinds = append(f.kinds, kind)
	f.next++
	f.live[f.next] = true
	return f.next, nil
}

func (f *fakePower) release(id mac.PowerAssertionID) {
	delete(f.live, id)
}

func (f *fakePower) held() int { return len(f.live) }

func newAwakeStub(p *fakePower) *awakeModule {
	return &awakeModule{
		host:    &shelf{},
		create:  p.create,
		release: p.release,
	}
}

func TestAwakeTimedRelease(t *testing.T) {
	p := newFakePower()
	m := newAwakeStub(p)
	now := time.Now()
	m.start(now, 30*time.Minute)
	if !m.active() || p.held() != 1 {
		t.Fatalf("started: active=%v held=%d", m.active(), p.held())
	}
	m.tick(now.Add(29 * time.Minute))
	if !m.active() {
		t.Fatal("released before the deadline")
	}
	m.tick(now.Add(30 * time.Minute))
	if m.active() || p.held() != 0 {
		t.Fatalf("deadline passed: active=%v held=%d", m.active(), p.held())
	}
}

func TestAwakeRepickRearms(t *testing.T) {
	p := newFakePower()
	m := newAwakeStub(p)
	now := time.Now()
	m.start(now, 30*time.Minute)
	m.start(now.Add(10*time.Minute), time.Hour)
	// The old hold must still be the only one (same type: no re-create),
	// and the deadline moved to now+70min.
	if p.held() != 1 || len(p.kinds) != 1 {
		t.Fatalf("repick: held=%d creates=%d", p.held(), len(p.kinds))
	}
	m.tick(now.Add(10*time.Minute + 30*time.Minute))
	if !m.active() {
		t.Fatal("expired at the old deadline")
	}
	m.tick(now.Add(10*time.Minute + time.Hour))
	if m.active() || p.held() != 0 {
		t.Fatalf("new deadline: active=%v held=%d", m.active(), p.held())
	}
}

func TestAwakeStopAndNeverExpire(t *testing.T) {
	p := newFakePower()
	m := newAwakeStub(p)
	now := time.Now()
	m.start(now, 0) // 一直
	m.tick(now.Add(100 * time.Hour))
	if !m.active() {
		t.Fatal("一直 expired")
	}
	m.stop(now)
	if m.active() || p.held() != 0 {
		t.Fatalf("stop: active=%v held=%d", m.active(), p.held())
	}
	if m.remainText(now) != "" {
		t.Fatalf("stopped module reports %q", m.remainText(now))
	}
}

func TestAwakeDisplaySwitch(t *testing.T) {
	p := newFakePower()
	m := newAwakeStub(p)
	now := time.Now()
	m.start(now, time.Hour)
	m.setDisplay(now, true)
	if p.held() != 1 || p.kinds[1] != mac.PowerAssertDisplaySleep {
		t.Fatalf("display on: held=%d kinds=%v", p.held(), p.kinds)
	}
	m.setDisplay(now, false)
	if p.held() != 1 || p.kinds[2] != mac.PowerAssertSystemSleep {
		t.Fatalf("display off: held=%d kinds=%v", p.held(), p.kinds)
	}
	// Same value is a no-op: no third assertion.
	m.setDisplay(now, false)
	if len(p.kinds) != 3 {
		t.Fatalf("no-op toggle created %d assertions", len(p.kinds))
	}
	// Toggled while off: the next start uses the display type.
	m.stop(now)
	m.setDisplay(now, true)
	m.start(now, time.Hour)
	if p.kinds[3] != mac.PowerAssertDisplaySleep {
		t.Fatalf("started with kind %v, want display", p.kinds[3])
	}
}

func TestAwakeDisplaySwitchFailure(t *testing.T) {
	p := newFakePower()
	m := newAwakeStub(p)
	now := time.Now()
	m.start(now, time.Hour)
	old := m.id
	p.fail = errors.New("denied")
	m.setDisplay(now, true)
	if m.display || m.id != old || p.held() != 1 {
		t.Fatalf("failed switch: display=%v id=%d held=%d", m.display, m.id, p.held())
	}
}

func TestAwakeCreateFailure(t *testing.T) {
	p := newFakePower()
	p.fail = errors.New("denied")
	m := newAwakeStub(p)
	m.start(time.Now(), time.Hour)
	if m.active() || p.held() != 0 {
		t.Fatalf("failed create: active=%v held=%d", m.active(), p.held())
	}
	if m.host.status == "" {
		t.Fatal("failed create reported no toast")
	}
}

// Assertion failures from the tray are visible without forcing the panel open.
func TestAwakeFailureUsesTheCapsule(t *testing.T) {
	p := newFakePower()
	p.fail = errors.New("denied")
	m := newAwakeStub(p)
	if m.host.visible {
		t.Fatal("panel started visible")
	}
	m.start(time.Now(), 30*time.Minute)
	if m.host.visible || m.host.fb.text == "" || m.host.fb.color() != fbRed {
		t.Fatal("the assertion failure did not reach the closed notch's error capsule")
	}
	if m.host.status == "" || m.host.statusKind != statusErr {
		t.Fatalf("toast = %q err=%v, want the error notice", m.host.status, m.host.statusKind == statusErr)
	}
	if left := time.Until(m.host.statusUntil); left < 3*time.Second {
		t.Fatalf("notice expires in %v, want the full 4s from opening", left)
	}
}

// pmset prints an empty `named:` for non-ASCII names, so the hold's name
// must be plain ASCII to stay visible in `pmset -g assertions`.
func TestAwakeAssertionNameIsASCII(t *testing.T) {
	if awakeAssertionName != "Rakko Stay Awake" {
		t.Fatalf("assertion name = %q, want %q", awakeAssertionName, "Rakko Stay Awake")
	}
	for i, r := range awakeAssertionName {
		if r > 127 {
			t.Fatalf("assertion name has non-ASCII %q at byte %d", r, i)
		}
	}
}

func TestAwakeRemainText(t *testing.T) {
	p := newFakePower()
	m := newAwakeStub(p)
	now := time.Now()
	m.start(now, 90*time.Second)
	if got := m.remainText(now); got != "剩余 1:30" {
		t.Fatalf("remain %q", got)
	}
	m.start(now, 0)
	if got := m.remainText(now); got != "直到关闭" {
		t.Fatalf("一直 remain %q", got)
	}
}
