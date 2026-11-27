package main

// The install download's tolerance for slow and broken connections: the
// source's Open hands mygo one continuous io.ReadCloser, and that body
// watches itself. A connection that stalls or drops mid-download is
// reconnected with an HTTP Range request from the byte where it stopped,
// so a slow link (a direct GitHub download half the world away) and a
// broken one both finish without the caller — mygo's io.Copy, its
// SHA-256 and its progress counter — ever noticing: the bytes it reads
// are one unbroken stream. What the body cannot repair it reports
// clearly, after a bounded number of tries, instead of parking the
// update window on an empty progress bar forever.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

var (
	// updateSourceHeaderTimeout bounds the wait for a response's headers on
	// the update source's own client: the process's shared transport has no
	// deadline, so without one a stuck response head would sit on a check
	// or a download forever. Mirrors mygo's updater. A variable so tests
	// can shrink it.
	updateSourceHeaderTimeout = 30 * time.Second
	// updateStallTimeout is how long a download body may deliver nothing
	// before the connection is judged broken and reconnected. A slow
	// stream is fine, however long it takes; one that stopped is not. A
	// variable so tests can shrink it.
	updateStallTimeout = 45 * time.Second
	// updateResumeTries bounds the broken-connection reconnects of one
	// download, stalls and refused reconnects alike: past it the download
	// fails with the reason instead of retrying all day.
	updateResumeTries = 6
	// updateResumeBackoff waits before the first reconnect and doubles per
	// try, capped by updateResumeBackoffMax.
	updateResumeBackoff    = time.Second
	updateResumeBackoffMax = 15 * time.Second
)

// attemptWatchdog drops the connection it guards when nothing arrives for
// the stall timeout: a read that never returns is a download parked on an
// empty progress bar. One per connection; its state is goroutine-safe,
// and only the reading goroutine arms and stops it.
type attemptWatchdog struct {
	cancel context.CancelFunc
	last   atomic.Int64 // Unix nanoseconds of the last delivered byte
	fired  atomic.Bool  // the stall timeout ended the connection
	armed  atomic.Bool
	timer  *time.Timer
}

// armAttemptWatchdog starts the watchdog of one connection, whose cancel
// ends that connection's request.
func armAttemptWatchdog(cancel context.CancelFunc) *attemptWatchdog {
	w := &attemptWatchdog{cancel: cancel}
	w.last.Store(time.Now().UnixNano())
	w.armed.Store(true)
	w.timer = time.AfterFunc(updateStallTimeout/4, w.check)
	return w
}

// check cancels the connection when nothing arrived for the stall timeout,
// and looks again a quarter of that later otherwise.
func (w *attemptWatchdog) check() {
	if !w.armed.Load() {
		return
	}
	if time.Since(time.Unix(0, w.last.Load())) >= updateStallTimeout {
		w.fired.Store(true)
		w.armed.Store(false)
		w.cancel()
		return
	}
	if w.armed.Load() {
		w.timer.Reset(updateStallTimeout / 4)
	}
}

func (w *attemptWatchdog) touched() { w.last.Store(time.Now().UnixNano()) }

func (w *attemptWatchdog) stop() {
	w.armed.Store(false)
	w.timer.Stop()
}

// resumeBody is the body the update source's Open returns: the response
// of one GET, reconnected with a Range request whenever the connection
// stalls or drops before the promised end. Not safe for concurrent use;
// mygo reads a download from one goroutine, and so does every test.
type resumeBody struct {
	parent  context.Context // the install's context; its cancel ends all retries
	client  *http.Client
	url     string
	version string // Rakko/<version> is the User-Agent

	// offset counts the bytes already handed out; total is the first
	// response's Content-Length, -1 when the server kept it to itself;
	// validator is the ETag (or Last-Modified) that makes a Range request
	// safe against a resource that changed mid-download.
	offset     int64
	total      int64
	validator  string
	body       io.ReadCloser
	cancel     context.CancelFunc
	watch      *attemptWatchdog
	tries      int   // reconnects spent
	connectErr error // why the last connection ended, for the final error
	err        error
}

// connect opens one attempt from where the stream stopped: a plain GET at
// the beginning, a Range request (with If-Range when a validator was
// seen) after a break. The returned error is retryErr when another
// attempt may succeed, and finalErr for everything the caller can only
// report.
func (r *resumeBody) connect() error {
	ctx, cancel := context.WithCancel(r.parent)
	defer func() {
		if r.body == nil {
			cancel()
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return finalErr{err}
	}
	req.Header.Set("User-Agent", "Rakko/"+r.version)
	if r.offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", r.offset))
		if r.validator != "" {
			req.Header.Set("If-Range", r.validator)
		}
	}
	resp, err := r.client.Do(req)
	if err != nil {
		if r.parent.Err() != nil {
			return finalErr{r.parent.Err()}
		}
		return retryErr{err}
	}
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != r.offset {
			resp.Body.Close()
			return finalErr{fmt.Errorf("GET %s: resumed at byte %d, want %d", r.url, start, r.offset)}
		}
		if total >= 0 && r.total >= 0 && total != r.total {
			// The two halves of the stream describe different sizes: the
			// resource changed under a validator-less resume. Splicing the
			// pieces would build a corrupt archive.
			resp.Body.Close()
			return finalErr{errors.New("the update changed on the server while it was downloading")}
		}
	case resp.StatusCode == http.StatusOK && r.offset == 0:
		// The first, plain GET.
	case resp.StatusCode == http.StatusOK:
		// A 200 where a resume was asked for means the server sent the
		// whole resource again: either it changed (If-Range) or it cannot
		// resume. Either way the halves cannot be spliced.
		resp.Body.Close()
		if r.validator != "" {
			return finalErr{errors.New("the update changed on the server while it was downloading")}
		}
		return finalErr{errors.New("the server cannot resume a partial download")}
	default:
		status := resp.Status
		transient := transientUpdateStatus(resp.StatusCode)
		resp.Body.Close()
		err := fmt.Errorf("GET %s: %s", r.url, status)
		if !transient {
			return finalErr{err}
		}
		return retryErr{err}
	}
	if r.offset == 0 {
		r.total = -1
		if cl := resp.Header.Get("Content-Length"); cl != "" {
			if n, err := strconv.ParseInt(cl, 10, 64); err == nil {
				r.total = n
			}
		}
		// A validator makes the next Range request refuse a resource that
		// changed: the ETag is exact, Last-Modified is the date fallback.
		r.validator = firstNonEmptyHeader(resp.Header, "ETag", "Last-Modified")
	}
	r.body, r.cancel = resp.Body, cancel
	r.watch = armAttemptWatchdog(cancel)
	return nil
}

// closeAttempt ends the current connection and stops its watchdog.
func (r *resumeBody) closeAttempt() {
	if r.body == nil {
		return
	}
	r.watch.stop()
	r.body.Close()
	r.cancel()
	r.body, r.cancel, r.watch = nil, nil, nil
}

// Read hands out the download's bytes, reconnecting with a Range request
// from where it stopped whenever the connection breaks before the
// promised end.
func (r *resumeBody) Read(p []byte) (int, error) {
	for {
		if r.err != nil {
			return 0, r.err
		}
		if r.body == nil {
			if !r.reconnect() {
				return 0, r.err
			}
		}
		n, err := r.body.Read(p)
		if n > 0 {
			r.offset += int64(n)
			r.watch.touched()
			return n, nil
		}
		if err == nil {
			continue
		}
		if r.watch.fired.Load() {
			r.connectErr = errors.New("the connection sent nothing for " + updateStallTimeout.String())
		} else {
			r.connectErr = err
		}
		r.closeAttempt()
		if r.parent.Err() != nil {
			// The install's own context died: the user's cancel, or the
			// overall deadline. Never resumed past that.
			r.err = r.parent.Err()
			return 0, r.err
		}
		if errors.Is(err, io.EOF) && (r.total < 0 || r.offset == r.total) {
			r.err = io.EOF
			return 0, io.EOF
		}
		// Everything else is a connection that broke mid-download — a
		// watchdog-dropped stall, a reset, a shorter-than-promised body —
		// and the stream resumes from where it stopped.
		if !r.reconnect() {
			return 0, r.err
		}
	}
}

// reconnect spends tries of the budget on a new connection, waiting out
// the backoff first; false when the budget is spent or the parent context
// is done, with r.err saying why. The reconnect itself may be refused a
// few times without a fresh byte; those consume the same budget.
func (r *resumeBody) reconnect() bool {
	if r.tries >= updateResumeTries {
		r.err = fmt.Errorf("the download broke %d times: %w", r.tries, r.connectErr)
		return false
	}
	for {
		wait := min(updateResumeBackoff<<r.tries, updateResumeBackoffMax)
		r.tries++
		select {
		case <-r.parent.Done():
			r.err = r.parent.Err()
			return false
		case <-time.After(wait):
		}
		err := r.connect()
		if err == nil {
			return true
		}
		var retry retryErr
		if !errors.As(err, &retry) || r.parent.Err() != nil {
			var final finalErr
			if errors.As(err, &final) {
				r.err = final.detail(r.tries, r.connectErr)
			} else {
				r.err = err
			}
			return false
		}
		r.connectErr = retry.err
		if r.tries >= updateResumeTries {
			r.err = fmt.Errorf("the download broke %d times: %w", r.tries, r.connectErr)
			return false
		}
	}
}

// Close drops the connection; the download's caller ends it this way
// whether the stream ran out or gave up. A later Read answers closed,
// not the bytes of a dead connection.
func (r *resumeBody) Close() error {
	r.closeAttempt()
	if r.err == nil {
		r.err = errors.New("the download body is closed")
	}
	return nil
}

// retryErr marks a connect failure another attempt may repair: a refused
// or reset connection, or a status the server itself calls temporary.
type retryErr struct{ err error }

func (e retryErr) Error() string { return e.err.Error() }
func (e retryErr) Unwrap() error { return e.err }

// finalErr marks a failure no retry can repair: a bad answer to a Range
// request, a changed resource, a status that is not temporary.
type finalErr struct{ err error }

func (e finalErr) Error() string { return e.err.Error() }
func (e finalErr) Unwrap() error { return e.err }

// detail is the final error of a download that had already broken before
// it hit a failure no retry repairs, so the report carries both.
func (e finalErr) detail(tries int, last error) error {
	if last == nil {
		return e.err
	}
	return fmt.Errorf("%w (after the download broke %d times: %v)", e.err, tries, last)
}

// transientUpdateStatus reports whether a response of the code may
// succeed when asked again, as mygo's updater judges its own feed.
func transientUpdateStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// parseContentRange reads the offset and total a 206's Content-Range
// promises: "bytes <start>-<end>/<total>", with * for an unknown total.
func parseContentRange(v string) (start, total int64, ok bool) {
	rest, found := strings.CutPrefix(v, "bytes ")
	if !found {
		return 0, -1, false
	}
	before, after, found := strings.Cut(rest, "/")
	if !found {
		return 0, -1, false
	}
	startEnd := strings.Split(before, "-")
	if len(startEnd) != 2 {
		return 0, -1, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(startEnd[0]), 10, 64)
	if err != nil || start < 0 {
		return 0, -1, false
	}
	if after == "*" {
		return start, -1, true
	}
	total, err = strconv.ParseInt(strings.TrimSpace(after), 10, 64)
	if err != nil || total < 0 {
		return 0, -1, false
	}
	return start, total, true
}

// firstNonEmptyHeader is the first of the named headers the response set.
func firstNonEmptyHeader(h http.Header, names ...string) string {
	for _, name := range names {
		if v := h.Get(name); v != "" {
			return v
		}
	}
	return ""
}
