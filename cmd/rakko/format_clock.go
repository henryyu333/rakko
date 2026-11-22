package main

// formatClock renders seconds as m:ss, or h:mm:ss past an hour.
func formatClock(sec float64) string {
	if sec < 0 || sec != sec { // negative or NaN
		sec = 0
	}
	return clockSeconds(int(sec + 0.5))
}
