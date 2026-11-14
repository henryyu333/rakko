//go:build darwin

package platform

import (
	"time"

	"golang.org/x/sys/unix"
)

// CreationTime is the filesystem's birth time for path. A filesystem that
// keeps none, or a path that is gone, reports false.
func CreationTime(path string) (time.Time, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return time.Time{}, false
	}
	ts := st.Btim
	if ts.Sec == 0 && ts.Nsec == 0 {
		return time.Time{}, false
	}
	return time.Unix(ts.Sec, ts.Nsec), true
}
