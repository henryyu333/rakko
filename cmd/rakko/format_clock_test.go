package main

import "testing"

func TestFormatClock(t *testing.T) {
	cases := []struct {
		sec  float64
		want string
	}{
		{0, "0:00"},
		{9.4, "0:09"},
		{59.5, "1:00"},
		{60, "1:00"},
		{61, "1:01"},
		{599, "9:59"},
		{3599, "59:59"},
		{3600, "1:00:00"},
		{3661, "1:01:01"},
		{-3, "0:00"},
	}
	for _, c := range cases {
		if got := formatClock(c.sec); got != c.want {
			t.Errorf("formatClock(%v) = %q, want %q", c.sec, got, c.want)
		}
	}
}
