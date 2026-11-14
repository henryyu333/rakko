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

	mu     sync.Mutex
	closed bool
}

// WatchFile starts watching path. notify runs on a goroutine other than
// the caller's, once per batch of file system events.
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
	w := &Watch{dir: dir, stop: stop, io: ioEvent, finished: make(chan struct{})}
	go w.run(notify)
	return w, nil
}

// Close stops the watcher. notify is not called again after it returns.
func (w *Watch) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	w.mu.Unlock()
	windows.SetEvent(w.stop)
	// Abandon a read still pending on the folder, then let the goroutine
	// leave before its handles go.
	_ = windows.CancelIoEx(w.dir, nil)
	<-w.finished
	windows.CloseHandle(w.io)
	windows.CloseHandle(w.stop)
	windows.CloseHandle(w.dir)
}

// run reads one batch of changes at a time until Close.
func (w *Watch) run(notify func()) {
	defer close(w.finished)
	buf := make([]byte, 64<<10)
	for {
		var ol windows.Overlapped
		ol.HEvent = w.io
		_ = windows.ResetEvent(w.io)
		if err := windows.ReadDirectoryChanges(w.dir, &buf[0], uint32(len(buf)), false,
			notifyFilter, nil, &ol, 0); err != nil {
			return
		}
		event, err := windows.WaitForMultipleObjects([]windows.Handle{w.stop, w.io}, false, windows.INFINITE)
		if err != nil {
			return
		}
		if event == windows.WAIT_OBJECT_0 {
			return // Close
		}
		if event != windows.WAIT_OBJECT_0+1 {
			return
		}
		var n uint32
		if err := windows.GetOverlappedResult(w.dir, &ol, &n, false); err != nil {
			if err == windows.ERROR_NOTIFY_ENUM_DIR {
				// The buffer overflowed and changes were dropped; telling
				// the caller is the only correct answer left.
				n = 1
			} else {
				return
			}
		}
		w.mu.Lock()
		if w.closed {
			w.mu.Unlock()
			return
		}
		if n > 0 {
			notify()
		}
		w.mu.Unlock()
	}
}
