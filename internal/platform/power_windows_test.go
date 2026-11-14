//go:build windows

package platform

// Power and session calls: headless checks that the assertions the awake
// module creates are named and released, and that the machine queries
// answer without a desktop.

import "testing"

func TestPowerAssertions(t *testing.T) {
	id, err := NewPowerAssertion(PowerAssertDisplaySleep, "rakko-test")
	if err != nil || id == 0 {
		t.Fatalf("NewPowerAssertion = %v, %v", id, err)
	}
	if got := PowerAssertionName(id); got != "rakko-test" {
		t.Fatalf("PowerAssertionName = %q, want rakko-test", got)
	}
	ReleasePowerAssertion(id)
	if got := PowerAssertionName(id); got != "" {
		t.Fatalf("a released assertion still answers %q", got)
	}
	ReleasePowerAssertion(0) // a failed create must be a no-op
}

func TestPowerAndSessionQueries(t *testing.T) {
	if _, ok := Battery(); ok {
		// A desktop machine reports no battery; a laptop answers. Either
		// way the call must not panic.
	}
	if idle, err := IdleSeconds(); err != nil || idle < 0 {
		t.Fatalf("IdleSeconds = %v, %v", idle, err)
	}
	_ = SessionActive()
	_ = MainDisplayAsleep()
	_ = ReduceTransparency()
}

func TestTextHelpers(t *testing.T) {
	if got, ok := DecodeGB18030([]byte{0xD6, 0xD0, 0xCE, 0xC4}); !ok || got != "中文" {
		t.Fatalf("DecodeGB18030 = %q, %v; want 中文", got, ok)
	}
	if _, ok := DecodeGB18030([]byte{0xFF, 0xFF}); ok {
		t.Fatal("invalid GB18030 reported success")
	}
	if got := NormalizeText("ﬁ"); got != "fi" {
		t.Fatalf("NormalizeText(ﬁ) = %q, want fi", got)
	}
}
