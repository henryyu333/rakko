//go:build windows

package agentnotify

// A named-pipe net.Listener and net.Conn. The standard library has no
// named-pipe transport, so this file supplies the small amount the protocol
// needs: overlapped I/O with working deadlines, and a Close that waits for
// in-flight reads and writes to finish.
//
// Wire detail: byte mode, duplex, one instance per connection, and the
// client opens with SECURITY_IDENTIFICATION so the server can never act as
// the client.
//
// Concurrency: one read and one write may be in flight at a time, each with
// its own completion event; a second same-direction call waits its turn, so
// a completion is never delivered to the wrong caller and a large write
// never interleaves. I/O is submitted while holding the mutex Close uses to
// mark the connection closed, so a request registered but not yet submitted
// cannot slip past the single CancelIoEx that Close issues. A deadline
// change wakes a waiting caller through its own event, so shrinking,
// extending or clearing a deadline takes effect on the operation already
// waiting.

import (
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pipeBuffer is both pipe buffers' size. A frame is one short line; a
// card's decision fits with room to spare.
const pipeBuffer = 8 << 10

// pipeAddr names one end of a pipe connection.
type pipeAddr struct{ name string }

func (a pipeAddr) Network() string { return "pipe" }
func (a pipeAddr) String() string  { return a.name }

// beforeSubmit is a test seam: it runs inside the critical section just
// before an operation is handed to the kernel, so a test can hold open the
// window in which Close must not miss the request. Production leaves it nil.
var beforeSubmit func()

// pipeListener accepts named-pipe clients. Every accepted connection gets
// its own pipe instance; the first instance is created with
// FILE_FLAG_FIRST_PIPE_INSTANCE, so a name another program owns fails here
// instead of being silently shared.
type pipeListener struct {
	name string
	sd   *windows.SECURITY_DESCRIPTOR

	mu      sync.Mutex
	cond    *sync.Cond
	pending windows.Handle // instance waiting for a client
	waiting map[windows.Handle]struct{} // instances Accept is blocked on
	active  int                         // Accept calls in progress
	closed  bool
}

// listenPipe creates the first instance of the pipe, with sd as its security
// descriptor.
func listenPipe(name string, sd *windows.SECURITY_DESCRIPTOR) (*pipeListener, error) {
	h, err := createPipeInstance(name, sd, true)
	if err != nil {
		return nil, err
	}
	l := &pipeListener{name: name, sd: sd, pending: h, waiting: map[windows.Handle]struct{}{}}
	l.cond = sync.NewCond(&l.mu)
	return l, nil
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	l.active++
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.active--
		if l.active == 0 {
			l.cond.Broadcast()
		}
		l.mu.Unlock()
	}()

	for {
		h, err := l.nextInstance()
		if err != nil {
			return nil, err
		}
		ev, err := windows.CreateEvent(nil, 0, 0, nil)
		if err != nil {
			windows.CloseHandle(h)
			return nil, err
		}
		// Register the instance before submitting, so Close cancels every
		// accept that is under way — net.Listener allows concurrent Accepts,
		// and one field could only remember the last of them.
		l.mu.Lock()
		if l.closed {
			l.mu.Unlock()
			windows.CloseHandle(h)
			windows.CloseHandle(ev)
			return nil, net.ErrClosed
		}
		l.waiting[h] = struct{}{}
		l.mu.Unlock()

		ov := &windows.Overlapped{HEvent: ev}
		err = windows.ConnectNamedPipe(h, ov)
		connected := err == nil || errors.Is(err, windows.ERROR_PIPE_CONNECTED)
		if !connected && errors.Is(err, windows.ERROR_IO_PENDING) {
			// Close may have cancelled between the registration and the
			// submission, which cancels nothing yet; the flag is read again
			// once the request really is pending.
			l.mu.Lock()
			closed := l.closed
			l.mu.Unlock()
			if closed {
				_ = windows.CancelIoEx(h, ov)
			}
			status, werr := windows.WaitForSingleObject(ev, windows.INFINITE)
			connected = werr == nil && status == windows.WAIT_OBJECT_0
			if connected {
				var done uint32
				connected = windows.GetOverlappedResult(h, ov, &done, false) == nil
			}
		}
		l.mu.Lock()
		delete(l.waiting, h)
		closed := l.closed
		l.mu.Unlock()

		if closed {
			_ = windows.DisconnectNamedPipe(h)
			windows.CloseHandle(h)
			windows.CloseHandle(ev)
			return nil, net.ErrClosed
		}
		if connected {
			windows.CloseHandle(ev)
			// newPipeConn owns the pipe handle from here and closes it if it
			// cannot set up the connection's events.
			c, cerr := newPipeConn(h, l.name)
			if cerr != nil {
				return nil, cerr
			}
			return c, nil
		}
		// A client that disappeared before Accept got to it, or a transient
		// failure: drop this instance and take the next client.
		_ = windows.DisconnectNamedPipe(h)
		windows.CloseHandle(h)
		windows.CloseHandle(ev)
	}
}

// Close stops accepting and does not return until the accept in flight has
// released its pipe instance, so a stop followed at once by a start can
// create the name again.
func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	pending := l.pending
	l.pending = 0
	waiting := make([]windows.Handle, 0, len(l.waiting))
	for h := range l.waiting {
		waiting = append(waiting, h)
	}
	l.mu.Unlock()

	if pending != 0 {
		windows.CloseHandle(pending)
	}
	// Every blocked ConnectNamedPipe completes with an error; the accepting
	// goroutine owns the handle and closes it before it drops out of
	// l.active.
	for _, h := range waiting {
		_ = windows.CancelIoEx(h, nil)
	}
	l.mu.Lock()
	for l.active > 0 {
		l.cond.Wait()
	}
	l.mu.Unlock()
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{name: l.name} }

// nextInstance hands out the waiting instance, or creates a fresh one.
func (l *pipeListener) nextInstance() (windows.Handle, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return 0, net.ErrClosed
	}
	h := l.pending
	l.pending = 0
	l.mu.Unlock()
	if h != 0 {
		return h, nil
	}
	return createPipeInstance(l.name, l.sd, false)
}

func createPipeInstance(name string, sd *windows.SECURITY_DESCRIPTOR, first bool) (windows.Handle, error) {
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	open := uint32(windows.PIPE_ACCESS_DUPLEX | windows.FILE_FLAG_OVERLAPPED)
	if first {
		open |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	mode := uint32(windows.PIPE_TYPE_BYTE | windows.PIPE_READMODE_BYTE |
		windows.PIPE_WAIT | windows.PIPE_REJECT_REMOTE_CLIENTS)
	sa := &windows.SecurityAttributes{
		Length:             uint32(unsafeSizeofSecurityAttributes()),
		SecurityDescriptor: sd,
		InheritHandle:      0,
	}
	return windows.CreateNamedPipe(namep, open, mode,
		windows.PIPE_UNLIMITED_INSTANCES, pipeBuffer, pipeBuffer, 0, sa)
}

// dialPipe opens the pipe named name, giving up after timeout. A pipe with
// every instance busy is retried until the deadline.
func dialPipe(name string, timeout time.Duration) (net.Conn, error) {
	namep, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		h, err := windows.CreateFile(namep,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, nil, windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION,
			0)
		if err == nil {
			return newPipeConn(h, name)
		}
		if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
			return nil, err
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return nil, err
		}
		ms := uint32(remain.Milliseconds())
		if ms == 0 {
			ms = 1
		}
		if werr := waitNamedPipe(namep, ms); werr != nil {
			return nil, werr
		}
	}
}

// pipeConn is one connection on a named pipe. One read and one write may be
// in flight at a time; each direction has a completion event and an event
// that wakes it when its deadline changes.
type pipeConn struct {
	h    windows.Handle
	name string

	rlock, wlock   sync.Mutex // serializes reads / whole writes
	rcompletion    windows.Handle
	wcompletion    windows.Handle
	rdeadlineEvent windows.Handle
	wdeadlineEvent windows.Handle

	mu     sync.Mutex
	cond   *sync.Cond
	rdl    time.Time
	wdl    time.Time
	rop    *windows.Overlapped
	wop    *windows.Overlapped
	ops    int
	closed bool
	once   sync.Once
}

func newPipeConn(h windows.Handle, name string) (*pipeConn, error) {
	var events [4]windows.Handle
	for i := range events {
		// The deadline events are manual-reset so a change made before the
		// I/O starts is not lost; the completion events are auto-reset.
		manual := uint32(0)
		if i >= 2 {
			manual = 1
		}
		ev, err := windows.CreateEvent(nil, manual, 0, nil)
		if err != nil {
			for _, done := range events[:i] {
				windows.CloseHandle(done)
			}
			windows.CloseHandle(h)
			return nil, err
		}
		events[i] = ev
	}
	c := &pipeConn{
		h: h, name: name,
		rcompletion: events[0], wcompletion: events[1],
		rdeadlineEvent: events[2], wdeadlineEvent: events[3],
	}
	c.cond = sync.NewCond(&c.mu)
	return c, nil
}

func (c *pipeConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	c.rlock.Lock()
	defer c.rlock.Unlock()
	return c.transfer(b, true)
}

func (c *pipeConn) Write(b []byte) (int, error) {
	c.wlock.Lock()
	defer c.wlock.Unlock()
	total := 0
	for total < len(b) {
		n, err := c.transfer(b[total:], false)
		total += n
		if err != nil {
			return total, err
		}
		if n == 0 {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

// completionEvents is the direction's completion and deadline events.
func (c *pipeConn) completionEvents(read bool) (done, changed windows.Handle) {
	if read {
		return c.rcompletion, c.rdeadlineEvent
	}
	return c.wcompletion, c.wdeadlineEvent
}

func (c *pipeConn) deadline(read bool) time.Time {
	if read {
		return c.rdl
	}
	return c.wdl
}

// transfer submits one overlapped read or write and waits for it.
func (c *pipeConn) transfer(b []byte, read bool) (int, error) {
	doneEv, _ := c.completionEvents(read)
	ov := &windows.Overlapped{HEvent: doneEv}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	if dl := c.deadline(read); !dl.IsZero() && !time.Now().Before(dl) {
		c.mu.Unlock()
		return 0, timeoutError{}
	}
	if read {
		c.rop = ov
	} else {
		c.wop = ov
	}
	c.ops++
	// The request is submitted before the mutex is released. Close marks the
	// connection closed under the same mutex and only then cancels; a
	// request that got in first is therefore always covered by that cancel,
	// and a request that comes later never submits at all.
	if beforeSubmit != nil {
		beforeSubmit()
	}
	var done uint32
	var err error
	if read {
		err = windows.ReadFile(c.h, b, &done, ov)
	} else {
		err = windows.WriteFile(c.h, b, &done, ov)
	}
	if err != nil && !errors.Is(err, windows.ERROR_IO_PENDING) {
		c.clearOpLocked(read)
		c.mu.Unlock()
		return 0, pipeIOError(err, read)
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.clearOpLocked(read)
		c.mu.Unlock()
	}()
	if err == nil {
		return int(done), nil
	}
	return c.await(ov, read)
}

func (c *pipeConn) clearOpLocked(read bool) {
	if read {
		c.rop = nil
	} else {
		c.wop = nil
	}
	c.ops--
	if c.ops == 0 {
		c.cond.Broadcast()
	}
}

// await waits for one submitted operation, recomputing the wait whenever the
// deadline changes.
func (c *pipeConn) await(ov *windows.Overlapped, read bool) (int, error) {
	doneEv, changed := c.completionEvents(read)
	for {
		c.mu.Lock()
		closed := c.closed
		dl := c.deadline(read)
		c.mu.Unlock()

		if closed {
			c.abandon(ov)
			return 0, io.ErrClosedPipe
		}
		wait := uint32(windows.INFINITE)
		if !dl.IsZero() {
			remain := time.Until(dl)
			if remain <= 0 {
				c.abandon(ov)
				return 0, timeoutError{}
			}
			ms := uint32(remain.Milliseconds())
			if ms == 0 {
				ms = 1
			}
			wait = ms
		}
		status, werr := windows.WaitForMultipleObjects([]windows.Handle{doneEv, changed}, false, wait)
		if werr != nil {
			c.abandon(ov)
			return 0, werr
		}
		switch uint32(status) {
		case uint32(windows.WAIT_OBJECT_0):
			var done uint32
			err := windows.GetOverlappedResult(c.h, ov, &done, false)
			if err == nil {
				return int(done), nil
			}
			if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				// Cancelled by Close or by an expired deadline; the loop
				// re-reads both and reports the right one.
				continue
			}
			return 0, pipeIOError(err, read)
		case uint32(windows.WAIT_OBJECT_0) + 1:
			_ = windows.ResetEvent(changed)
			continue
		case uint32(windows.WAIT_TIMEOUT):
			c.abandon(ov)
			return 0, timeoutError{}
		default:
			c.abandon(ov)
			return 0, errors.New("agentnotify: unexpected pipe wait result")
		}
	}
}

// abandon cancels one operation and waits for the kernel to let go of its
// buffer.
func (c *pipeConn) abandon(ov *windows.Overlapped) {
	_ = windows.CancelIoEx(c.h, ov)
	var done uint32
	_ = windows.GetOverlappedResult(c.h, ov, &done, true)
}

// pipeIOError maps the pipe's end-of-file to io.EOF, which bufio and
// io.LimitReader both understand.
func pipeIOError(err error, read bool) error {
	if read && (errors.Is(err, windows.ERROR_BROKEN_PIPE) || errors.Is(err, windows.ERROR_PIPE_NOT_CONNECTED)) {
		return io.EOF
	}
	return err
}

func (c *pipeConn) Close() error {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.mu.Unlock()
		_ = windows.CancelIoEx(c.h, nil)
		c.mu.Lock()
		for c.ops > 0 {
			c.cond.Wait()
		}
		c.mu.Unlock()
		windows.CloseHandle(c.h)
		for _, ev := range []windows.Handle{
			c.rcompletion, c.wcompletion, c.rdeadlineEvent, c.wdeadlineEvent,
		} {
			windows.CloseHandle(ev)
		}
	})
	return nil
}

func (c *pipeConn) LocalAddr() net.Addr  { return pipeAddr{name: c.name} }
func (c *pipeConn) RemoteAddr() net.Addr { return pipeAddr{name: c.name} }

func (c *pipeConn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

func (c *pipeConn) SetReadDeadline(t time.Time) error {
	c.setDeadline(t, true)
	return nil
}

func (c *pipeConn) SetWriteDeadline(t time.Time) error {
	c.setDeadline(t, false)
	return nil
}

// setDeadline records t and wakes the direction's waiter, so a deadline
// moved earlier, later or away takes effect on the operation already
// waiting. The waiter itself cancels the request if the new deadline is
// already past.
func (c *pipeConn) setDeadline(t time.Time, read bool) {
	c.mu.Lock()
	if read {
		c.rdl = t
	} else {
		c.wdl = t
	}
	var ev windows.Handle
	if read {
		ev = c.rdeadlineEvent
	} else {
		ev = c.wdeadlineEvent
	}
	closed := c.closed
	c.mu.Unlock()
	if ev != 0 && !closed {
		_ = windows.SetEvent(ev)
	}
}

// timeoutError is net.Conn's deadline error: it reports Timeout and matches
// os.ErrDeadlineExceeded.
type timeoutError struct{}

func (timeoutError) Error() string   { return "agentnotify: i/o deadline exceeded" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }
func (timeoutError) Is(target error) bool {
	return target == os.ErrDeadlineExceeded
}

// unsafeSizeofSecurityAttributes is SECURITY_ATTRIBUTES.nLength.
func unsafeSizeofSecurityAttributes() uintptr { return unsafe.Sizeof(windows.SecurityAttributes{}) }

// procWaitNamedPipeW waits for an instance of a named pipe to become
// available; x/sys wrap it only internally.
var procWaitNamedPipeW = windows.NewLazySystemDLL("kernel32.dll").NewProc("WaitNamedPipeW")

func waitNamedPipe(name *uint16, milliseconds uint32) error {
	r1, _, err := procWaitNamedPipeW.Call(uintptr(unsafe.Pointer(name)), uintptr(milliseconds))
	if r1 == 0 {
		return err
	}
	return nil
}
