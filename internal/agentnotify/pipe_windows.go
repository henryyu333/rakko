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

// pipeListener accepts named-pipe clients. Every accepted connection gets
// its own pipe instance; the first instance is created with
// FILE_FLAG_FIRST_PIPE_INSTANCE, so a name another program owns fails here
// instead of being silently shared.
type pipeListener struct {
	name    string
	sd      *windows.SECURITY_DESCRIPTOR
	mu      sync.Mutex
	pending windows.Handle // instance waiting for a client
	waiting windows.Handle // instance Accept is blocked on
	closed  bool
}

// listenPipe creates the first instance of the pipe, with sd as its security
// descriptor.
func listenPipe(name string, sd *windows.SECURITY_DESCRIPTOR) (*pipeListener, error) {
	h, err := createPipeInstance(name, sd, true)
	if err != nil {
		return nil, err
	}
	return &pipeListener{name: name, sd: sd, pending: h}, nil
}

func (l *pipeListener) Accept() (net.Conn, error) {
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
		ov := &windows.Overlapped{HEvent: ev}
		err = windows.ConnectNamedPipe(h, ov)
		connected := err == nil || errors.Is(err, windows.ERROR_PIPE_CONNECTED)
		if !connected && errors.Is(err, windows.ERROR_IO_PENDING) {
			l.mu.Lock()
			l.waiting = h
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
			l.mu.Lock()
			l.waiting = 0
			closed = l.closed
			l.mu.Unlock()
			if closed {
				_ = windows.DisconnectNamedPipe(h)
				windows.CloseHandle(h)
				windows.CloseHandle(ev)
				return nil, net.ErrClosed
			}
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

func (l *pipeListener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	pending, waiting := l.pending, l.waiting
	l.pending = 0
	l.mu.Unlock()
	if pending != 0 {
		windows.CloseHandle(pending)
	}
	if waiting != 0 {
		// The blocked ConnectNamedPipe completes with an error, which wakes
		// Accept; Accept owns the handle and closes it.
		_ = windows.CancelIoEx(waiting, nil)
	}
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

// pipeConn is one connection on a named pipe.
type pipeConn struct {
	h    windows.Handle
	name string
	rev  windows.Handle
	wev  windows.Handle

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
	rev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		return nil, err
	}
	wev, err := windows.CreateEvent(nil, 0, 0, nil)
	if err != nil {
		windows.CloseHandle(h)
		windows.CloseHandle(rev)
		return nil, err
	}
	c := &pipeConn{h: h, name: name, rev: rev, wev: wev}
	c.cond = sync.NewCond(&c.mu)
	return c, nil
}

func (c *pipeConn) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	return c.xfer(b, true)
}

func (c *pipeConn) Write(b []byte) (int, error) {
	total := 0
	for total < len(b) {
		n, err := c.xfer(b[total:], false)
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

// xfer runs one overlapped read or write, honoring the direction's deadline.
func (c *pipeConn) xfer(b []byte, read bool) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, io.ErrClosedPipe
	}
	dl, ev := c.rdl, c.rev
	if !read {
		dl, ev = c.wdl, c.wev
	}
	if !dl.IsZero() && !time.Now().Before(dl) {
		c.mu.Unlock()
		return 0, timeoutError{}
	}
	ov := &windows.Overlapped{HEvent: ev}
	if read {
		c.rop = ov
	} else {
		c.wop = ov
	}
	c.ops++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if read {
			c.rop = nil
		} else {
			c.wop = nil
		}
		c.ops--
		if c.ops == 0 {
			c.cond.Broadcast()
		}
		c.mu.Unlock()
	}()

	var done uint32
	var err error
	if read {
		err = windows.ReadFile(c.h, b, &done, ov)
	} else {
		err = windows.WriteFile(c.h, b, &done, ov)
	}
	if err == nil {
		return int(done), nil
	}
	if !errors.Is(err, windows.ERROR_IO_PENDING) {
		return 0, pipeIOError(err, read)
	}
	wait := uint32(windows.INFINITE)
	if !dl.IsZero() {
		remain := time.Until(dl)
		if remain <= 0 {
			wait = 0
		} else if ms := remain.Milliseconds(); ms <= 0 {
			wait = 1
		} else {
			wait = uint32(ms)
		}
	}
	status, werr := windows.WaitForSingleObject(ev, wait)
	if werr != nil {
		_ = windows.CancelIoEx(c.h, ov)
		_ = windows.GetOverlappedResult(c.h, ov, &done, true)
		return 0, werr
	}
	if status == uint32(windows.WAIT_TIMEOUT) {
		_ = windows.CancelIoEx(c.h, ov)
		_ = windows.GetOverlappedResult(c.h, ov, &done, true)
		return 0, timeoutError{}
	}
	if gerr := windows.GetOverlappedResult(c.h, ov, &done, false); gerr != nil {
		if errors.Is(gerr, windows.ERROR_OPERATION_ABORTED) {
			c.mu.Lock()
			closed := c.closed
			expired := !c.rdl.IsZero() && !time.Now().Before(c.rdl)
			if !read {
				expired = !c.wdl.IsZero() && !time.Now().Before(c.wdl)
			}
			c.mu.Unlock()
			switch {
			case closed:
				return 0, io.ErrClosedPipe
			case expired:
				return 0, timeoutError{}
			}
		}
		return 0, pipeIOError(gerr, read)
	}
	return int(done), nil
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
		windows.CloseHandle(c.rev)
		windows.CloseHandle(c.wev)
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

func (c *pipeConn) setDeadline(t time.Time, read bool) {
	c.mu.Lock()
	if read {
		c.rdl = t
	} else {
		c.wdl = t
	}
	ov := c.rop
	if !read {
		ov = c.wop
	}
	closed := c.closed
	c.mu.Unlock()
	// An operation already running is waiting with the old timeout; when
	// the new deadline is already past, cancelling it is what makes the
	// deadline take effect.
	if ov != nil && !closed && !t.IsZero() && !time.Now().Before(t) {
		_ = windows.CancelIoEx(c.h, ov)
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
