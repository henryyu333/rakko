//go:build darwin

package note

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Watch follows the note file without polling. kqueue watches the note's
// folder — our atomic save renames a temporary file into it, and so do
// most editors' saves — and the note file itself, for an editor that
// rewrites the file in place. Both reports arrive as one notify call on
// the watcher's own goroutine; a caller that touches the UI hops to the
// main thread itself.
//
// A note that is replaced (rename over it) loses its file watch: the
// goroutine re-opens the path and watches it again, so the next save is
// still seen. Deleting the note keeps the folder watch, which sees the
// note come back.
type Watch struct {
	mu   sync.Mutex
	kq   int
	dir  *os.File
	f    *os.File
	quit *os.File // the read end of the stop pipe
	stop *os.File // …and its write end
	path string
	done bool
}

// WatchFile starts watching path. notify runs on a goroutine other than
// the caller's, once per batch of file system events.
func WatchFile(path string, notify func()) (*Watch, error) {
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	quit, stop, err := os.Pipe()
	if err != nil {
		dir.Close()
		return nil, err
	}
	w := &Watch{dir: dir, quit: quit, stop: stop, path: path, kq: -1}
	if w.kq, err = syscall.Kqueue(); err != nil {
		dir.Close()
		quit.Close()
		stop.Close()
		return nil, err
	}
	if err := w.arm(uint64(dir.Fd()), syscall.NOTE_WRITE|syscall.NOTE_DELETE|syscall.NOTE_RENAME); err != nil {
		w.Close()
		return nil, err
	}
	// The stop pipe's read end tells run when Close happened.
	ev := syscall.Kevent_t{Ident: uint64(quit.Fd()), Filter: syscall.EVFILT_READ, Flags: syscall.EV_ADD | syscall.EV_CLEAR}
	if _, err := syscall.Kevent(w.kq, []syscall.Kevent_t{ev}, nil, nil); err != nil {
		w.Close()
		return nil, err
	}
	w.rearm()
	go w.run(notify)
	return w, nil
}

// Close stops the watcher. notify is not called again after it returns.
func (w *Watch) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return
	}
	w.done = true
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	if w.stop != nil {
		// Wake the goroutine out of kevent and let it see done.
		w.stop.Write([]byte{1})
		w.stop.Close()
		w.stop = nil
	}
}

// closeFDs releases the kqueue and the folder; the goroutine owns them
// until it sees done.
func (w *Watch) closeFDs() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.quit != nil {
		w.quit.Close()
		w.quit = nil
	}
	if w.dir != nil {
		w.dir.Close()
		w.dir = nil
	}
	if w.kq >= 0 {
		syscall.Close(w.kq)
		w.kq = -1
	}
}

// arm adds one ident to the kqueue with NOTE_CLEAR semantics.
func (w *Watch) arm(ident uint64, fflags uint32) error {
	ev := syscall.Kevent_t{Ident: ident, Filter: syscall.EVFILT_VNODE, Flags: syscall.EV_ADD | syscall.EV_CLEAR}
	ev.Fflags = fflags
	_, err := syscall.Kevent(w.kq, []syscall.Kevent_t{ev}, nil, nil)
	return err
}

// rearm re-opens the note after a replacement and watches it again. A
// note that is not there reads as no watch until the folder reports it
// back. The caller holds the watch from Close's side by checking done
// first.
func (w *Watch) rearm() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done || w.kq < 0 {
		return
	}
	if w.f != nil {
		w.f.Close()
		w.f = nil
	}
	f, err := os.Open(w.path)
	if err != nil {
		return
	}
	ev := syscall.Kevent_t{
		Ident: uint64(f.Fd()), Filter: syscall.EVFILT_VNODE, Flags: syscall.EV_ADD | syscall.EV_CLEAR,
		Fflags: syscall.NOTE_WRITE | syscall.NOTE_EXTEND | syscall.NOTE_DELETE | syscall.NOTE_RENAME,
	}
	if _, err := syscall.Kevent(w.kq, []syscall.Kevent_t{ev}, nil, nil); err != nil {
		f.Close()
		return
	}
	w.f = f
}

// run waits for events until Close.
func (w *Watch) run(notify func()) {
	defer w.closeFDs()
	evs := make([]syscall.Kevent_t, 8)
	for {
		n, err := syscall.Kevent(w.kq, nil, evs, nil)
		if err == syscall.EINTR {
			// A signal interrupted the wait; the watch itself is fine.
			continue
		}
		if err != nil {
			return
		}
		stopped := false
		for _, ev := range evs[:n] {
			if ev.Filter == syscall.EVFILT_READ {
				stopped = true
			}
		}
		w.mu.Lock()
		done := w.done
		w.mu.Unlock()
		if done || stopped {
			return
		}
		if n == 0 {
			continue
		}
		w.rearm()
		notify()
	}
}
