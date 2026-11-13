//go:build darwin

package main

import (
	"time"

	"golang.org/x/sys/unix"
)

// dataDirBorn is the data folder's birth time, the first day an existing
// install without a companion file counts from. A filesystem that keeps no
// birth time, or a folder that is gone, reports false.
func dataDirBorn(dir string) (time.Time, bool) {
	var st unix.Stat_t
	if err := unix.Stat(dir, &st); err != nil {
		return time.Time{}, false
	}
	ts := st.Btim
	if ts.Sec == 0 && ts.Nsec == 0 {
		return time.Time{}, false
	}
	return time.Unix(ts.Sec, ts.Nsec), true
}
