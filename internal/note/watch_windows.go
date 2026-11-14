//go:build windows

package note

import (
	"path/filepath"
	"sync"

	"golang.org/x/sys/windows"
)

// notifyFilter is what a save can change: a new name (the atomic save's
// rename, or a note created after being deleted), a rewritten file (the
// size and the write time of an editor that saves in place).
const notifyFilter = windows.FILE_NOTIFY_CHANGE_FILE_NAME |
	windows.FILE_NOTIFY_CHANGE_SIZE |
	windows.FILE_NOTIFY_CHANGE_LAST_WRITE

// watchArm is Watch.arm as run calls it; a test replaces it to take the
// registration failure path, which a working file system will not produce.
var watchArm = (*Watch).arm

// Watch follows the note's folder without polling. ReadDirectoryChangesW
// reports our atomic save (a temporary file renamed into the folder), most
// editors' saves and an editor that rewrites the file in place, all as one
// notify call on the watcher's own goroutine; a caller that touches the UI
// hops to the main thread itself.
//
// A note that is replaced or deleted keeps its watch: the folder is what is
// watched, so the note coming back is seen too.
type Watch struct {
	dir      windows.Handle
	stop     windows.Handle
	io       windows.Handle
	finished chan struct{}

	// buf and ol belong to the one read in flight: the kernel writes into
	// both until that read completes, so neither is reused or released
	// before then.
	buf []byte
	ol  windows.Overlapped

	mu       sync.Mutex
	closed   bool
	released bool
}

// WatchFile starts watching path. notify runs on a goroutine other than
// the caller's, once per batch of file system events.
//
// It returns only once the first read is pending: a caller that saves the
// note right after the call must still be seen. A folder that cannot be
// watched is an error, not a watch that silently reports nothing.
func WatchFile(path string, notify func()) (*Watch, error) {
	dirName, err := windows.UTF16PtrFromString(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	dir, err := windows.CreateFile(dirName,
		windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1 /* manual reset */, 0, nil)
	if err != nil {
		windows.CloseHandle(dir)
		return nil, err
	}
	ioEvent, err := windows.CreateEvent(nil, 1 /* manual reset */, 0, nil)
	if err != nil {
		windows.CloseHandle(stop)
		windows.CloseHandle(dir)
		return nil, err
	}
	w := &Watch{
		dir:      dir,
		stop:     stop,
		io:       ioEvent,
		finished: make(chan struct{}),
		buf:      make([]byte, 64<<10),
	}
	ready := make(chan error, 1)
	go w.run(notify, ready)
	if err := <-ready; err != nil {
		<-w.finished
		return nil, err
	}
	return w, nil
}

// Close stops the watcher. notify is not called again after it returns,
// and the pending read has completed by then, so nothing the kernel owns
// outlives the watch.
func (w *Watch) Close() {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		if !w.released {
			windows.SetEvent(w.stop)
		}
	}
	w.mu.Unlock()
	<-w.finished
}

// run arms one read, waits for it, and reports the changes; the read loop
// is the only thing that submits or cancels a request, so Close can neither
// race a submission nor close a buffer the kernel still owns.
func (w *Watch) run(notify func(), ready chan<- error) {
	defer w.release()
	if err := watchArm(w); err != nil {
		ready <- err
		return
	}
	ready <- nil
	for {
		event, err := windows.WaitForMultipleObjects([]windows.Handle{w.stop, w.io}, false, windows.INFINITE)
		if err != nil {
			w.abandon()
			return
		}
		if event == windows.WAIT_OBJECT_0 {
			w.abandon() // Close
			return
		}
		if event != windows.WAIT_OBJECT_0+1 {
			w.abandon()
			return
		}
		var n uint32
		err = windows.GetOverlappedResult(w.dir, &w.ol, &n, false)
		w.mu.Lock()
		closed := w.closed
		w.mu.Unlock()
		if closed {
			// Close is waiting for this goroutine: the request has
			// completed, so the buffer is already safe to drop.
			return
		}
		if !reportsChange(n, err) {
			// The read failed for a reason other than a dropped batch —
			// the folder went away, most likely. The request is complete,
			// so there is nothing to cancel.
			return
		}
		notify()
		if err := watchArm(w); err != nil {
			return
		}
	}
}

// arm submits one read into the watcher's buffer. A request left pending is
// the normal answer, so only a request that could not be started is an
// error.
func (w *Watch) arm() error {
	w.ol = windows.Overlapped{HEvent: w.io}
	if err := windows.ResetEvent(w.io); err != nil {
		return err
	}
	err := windows.ReadDirectoryChanges(w.dir, &w.buf[0], uint32(len(w.buf)), false,
		notifyFilter, nil, &w.ol, 0)
	if err != nil && err != windows.ERROR_IO_PENDING {
		return err
	}
	return nil
}

// abandon ends the read in flight and waits for the kernel to finish with
// the overlapped structure and the buffer. CancelIoEx only asks; the
// completion is what makes the buffer safe to release.
func (w *Watch) abandon() {
	if err := windows.CancelIoEx(w.dir, &w.ol); err != nil && err != windows.ERROR_NOT_FOUND {
		// ERROR_NOT_FOUND is the only benign answer: no request is
		// pending, so the read has already completed and nothing can
		// still write into the buffer.
	}
	var n uint32
	_ = windows.GetOverlappedResult(w.dir, &w.ol, &n, true)
}

// release lets go of everything the read loop owned. It runs only after
// the read in flight has completed, so the kernel no longer holds the
// buffer, and Close returns when it is done.
func (w *Watch) release() {
	w.mu.Lock()
	w.released = true
	windows.CloseHandle(w.io)
	windows.CloseHandle(w.stop)
	windows.CloseHandle(w.dir)
	w.io, w.stop, w.dir = 0, 0, 0
	w.buf = nil
	w.mu.Unlock()
	close(w.finished)
}

// reportsChange says whether a finished read reports a change worth telling
// the caller about. A buffer overflow can complete in two ways: as
// ERROR_NOTIFY_ENUM_DIR, or as a successful read of no bytes. Both mean the
// record of what changed was dropped, so both are answered with a notify.
func reportsChange(n uint32, err error) bool {
	switch {
	case err == windows.ERROR_NOTIFY_ENUM_DIR:
		return true
	case err != nil:
		return false
	default:
		return true
	}
}
