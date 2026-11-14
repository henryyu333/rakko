//go:build darwin

package platform

import "testing"

// TestPowerAssertion exercises the real IOKit path: create a named
// assertion of each kind, read the name back from powerd and release it.
// A non-zero id, no error and the exact name prove the dylib, signature
// and CFString bridging all work.
func TestPowerAssertion(t *testing.T) {
	for _, kind := range []PowerAssertionKind{PowerAssertSystemSleep, PowerAssertDisplaySleep} {
		id, err := NewPowerAssertion(kind, "ShelfU Stay Awake")
		if err != nil {
			t.Fatalf("create kind %d: %v", kind, err)
		}
		if id == 0 {
			t.Fatalf("create kind %d returned id 0", kind)
		}
		if name := PowerAssertionName(id); name != "ShelfU Stay Awake" {
			t.Errorf("AssertName = %q, want %q", name, "ShelfU Stay Awake")
		}
		ReleasePowerAssertion(id)
		if name := PowerAssertionName(id); name != "" {
			t.Errorf("AssertName after release = %q, want the hold gone", name)
		}
	}
	ReleasePowerAssertion(0) // no-op, must not crash
	if _, err := NewPowerAssertion(PowerAssertionKind(99), "x"); err == nil {
		t.Error("unknown kind accepted")
	}
}
