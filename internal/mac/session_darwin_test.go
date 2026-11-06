//go:build darwin

package mac

import "testing"

func TestSessionEvidenceUsable(t *testing.T) {
	cases := []struct {
		name string
		e    sessionEvidence
		want bool
	}{
		{
			name: "logged-in console with no lock key is the unlocked shape",
			e:    sessionEvidence{onConsole: true, onConsoleValid: true},
			want: true,
		},
		{
			name: "explicit unlocked",
			e: sessionEvidence{
				onConsole: true, onConsoleValid: true,
				locked: false, lockPresent: true, lockValid: true,
			},
			want: true,
		},
		{
			name: "explicit locked",
			e: sessionEvidence{
				onConsole: true, onConsoleValid: true,
				locked: true, lockPresent: true, lockValid: true,
			},
			want: false,
		},
		{
			name: "lock key present but unreadable is unknown",
			e: sessionEvidence{
				onConsole: true, onConsoleValid: true,
				lockPresent: true, lockValid: false,
			},
			want: false,
		},
		{
			name: "away from the console",
			e: sessionEvidence{
				onConsole: false, onConsoleValid: true,
				lockPresent: true, lockValid: true,
			},
			want: false,
		},
		{
			name: "console key missing",
			e:    sessionEvidence{},
			want: false,
		},
		{
			name: "console key present but unreadable",
			e:    sessionEvidence{onConsole: true, onConsoleValid: false, lockPresent: false},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.e.usable(); got != c.want {
				t.Fatalf("usable() = %v, want %v", got, c.want)
			}
		})
	}
}
