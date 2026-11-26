//go:build windows

package note

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestReportsChange: a batch that overflowed the buffer completes either as
// ERROR_NOTIFY_ENUM_DIR or successfully with no bytes; both mean something
// changed, and dropping the second would lose the save.
func TestReportsChange(t *testing.T) {
	cases := []struct {
		name string
		n    uint32
		err  error
		want bool
	}{
		{"records", 64, nil, true},
		{"no bytes", 0, nil, true},
		{"overflow", 0, windows.ERROR_NOTIFY_ENUM_DIR, true},
		{"aborted", 0, windows.ERROR_OPERATION_ABORTED, false},
	}
	for _, c := range cases {
		if got := reportsChange(c.n, c.err); got != c.want {
			t.Errorf("%s: reportsChange(%d, %v) = %v, want %v", c.name, c.n, c.err, got, c.want)
		}
	}
}

// TestWatchFileNotifiesImmediatelyAfterReturn: the first read is pending
// before WatchFile returns, so a save made right after it is still seen.
func TestWatchFileNotifiesImmediatelyAfterReturn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	var hits atomic.Int32
	w, err := WatchFile(path, func() { hits.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if err := os.WriteFile(path, []byte("right away\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("a save made right after WatchFile returned was not reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestWatchFileReportsARegistrationFailure: a watch whose first read cannot
// be armed is an error, not a silent watch, and it leaves nothing open.
func TestWatchFileReportsARegistrationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	old := watchArm
	var got *Watch
	watchArm = func(w *Watch) error {
		got = w
		return errors.New("arm failed")
	}
	t.Cleanup(func() { watchArm = old })

	if _, err := WatchFile(path, func() {}); err == nil {
		t.Fatal("a failed registration must be an error")
	}
	if got == nil {
		t.Fatal("the test never saw the watch")
	}
	got.mu.Lock()
	defer got.mu.Unlock()
	if !got.released || got.dir != 0 || got.stop != 0 || got.io != 0 || got.buf != nil {
		t.Fatalf("the failed watch left something open: released %v dir %v stop %v io %v buf %d",
			got.released, got.dir, got.stop, got.io, len(got.buf))
	}
}

// TestCloseWaitsForTheReadInFlight: Close returns only after the pending
// read completed and its buffer was released, and no callback runs after
// it; a second close does nothing.
// TestCloseDoesNotWaitForABlockedNotify: Close runs on the window loop,
// and a caller's notify hops to that loop with RunOnMain. A notify waiting
// for the loop must not hold Close up, or the two wait on each other and
// the app stops answering hotkeys.
func TestCloseDoesNotWaitForABlockedNotify(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	started := make(chan struct{})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	defer unblock()
	var once sync.Once
	w, err := WatchFile(path, func() {
		once.Do(func() { close(started) })
		<-releaseCh
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("blocked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the save was not reported")
	}
	closed := make(chan struct{})
	go func() {
		w.Close()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited for the notify callback")
	}
	unblock()
	w.Close() // already closed: a no-op
}

func TestCloseWaitsForTheReadInFlight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.md")
	var hits atomic.Int32
	w, err := WatchFile(path, func() { hits.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	// One save completes a read; the next read is pending when Close runs,
	// which is the state the buffer must survive.
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for hits.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the save was not reported")
		}
		time.Sleep(10 * time.Millisecond)
	}

	w.Close()
	if w.buf != nil {
		t.Fatal("Close returned with the read's buffer still held")
	}
	w.mu.Lock()
	released := w.released
	w.mu.Unlock()
	if !released {
		t.Fatal("Close returned with the watch's handles still open")
	}
	w.Close() // already closed: a no-op

	got := hits.Load()
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if hits.Load() != got {
		t.Fatal("a callback ran after Close returned")
	}
}
