package main

import "fmt"

// formatClock renders seconds as m:ss, or h:mm:ss past an hour.
func formatClock(sec float64) string {
	if sec < 0 || sec != sec { // negative or NaN
		sec = 0
	}
	t := int(sec + 0.5)
	h, m, s := t/3600, (t%3600)/60, t%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
