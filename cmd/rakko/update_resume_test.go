package main

// The install download's resume body (update_resume.go), driven by a
// loopback httptest server the way mygo drives it: plain io.ReadAll. A
// connection that breaks mid-download must finish the stream through a
// Range request without the caller noticing; a connection that keeps
// breaking must fail with a reason instead of hanging; a slow-but-alive
// stream must never be dropped; and a server that cannot resume must be
// refused a splice rather than served a corrupt archive.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// updateTestSource is the source with its URLs left to the test's server.
func updateTestSource(client *http.Client) *rakkoUpdateSource {
	return &rakkoUpdateSource{
		primary:  "unused",
		fallback: "unused",
		client:   client,
		version:  func() string { return "test" },
	}
}

// updateTestPayload is n deterministic bytes.
func updateTestPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestNewUpdateSourceClientSetsHeaderDeadline(t *testing.T) {
	c := newUpdateSourceClient()
	transport, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport = %T, want *http.Transport", c.Transport)
	}
	if transport.ResponseHeaderTimeout != updateSourceHeaderTimeout {
		t.Fatalf("ResponseHeaderTimeout = %v, want %v", transport.ResponseHeaderTimeout, updateSourceHeaderTimeout)
	}
	if netproxy := transport.Proxy; netproxy == nil {
		t.Fatal("the cloned transport dropped the shared proxy rules")
	}
}

// A connection that delivers less than it promised is reconnected with a
// Range request from the byte where it stopped, and the caller reads one
// unbroken stream: the bytes match the file exactly.
func TestUpdateOpenResumesAfterADisconnect(t *testing.T) {
	payload := updateTestPayload(64 * 1024)
	cut := 30000

	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		if n == 1 {
			if got := r.Header.Get("Range"); got != "" {
				t.Errorf("first request carried Range %q, want none", got)
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.(io.Writer).Write(payload[:cut]) // then the connection ends early
			return
		}
		if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-", cut) {
			t.Errorf("resume Range = %q, want bytes=%d-", got, cut)
		}
		if got := r.Header.Get("If-Range"); got != `"v1"` {
			t.Errorf("resume If-Range = %q, want the first response's ETag", got)
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[cut:])
	}))
	defer srv.Close()

	body, err := updateTestSource(srv.Client()).Open(context.Background(), srv.URL+"/archive")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("resumed stream = %d bytes %s…, want the untouched %d-byte file", len(got), preview(got), len(payload))
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 2 {
		t.Fatalf("requests = %d, want the broken one and one resume", requests)
	}
}

// A connection that stalls mid-download is dropped by the watchdog, and
// one that keeps stalling past the retry budget fails with the reason
// instead of hanging on an empty progress bar.
func TestUpdateOpenPersistentStallsFailWithAReason(t *testing.T) {
	origStall, origTries, origBackoff := updateStallTimeout, updateResumeTries, updateResumeBackoff
	updateStallTimeout, updateResumeTries, updateResumeBackoff = 150*time.Millisecond, 3, time.Millisecond
	t.Cleanup(func() {
		updateStallTimeout, updateResumeTries, updateResumeBackoff = origStall, origTries, origBackoff
	})

	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		if offset := r.Header.Get("Range"); offset != "" {
			// The resume is honoured to the letter — and stalls again a
			// few bytes in, as a connection that keeps breaking does.
			var from int64
			if _, err := fmt.Sscanf(offset, "bytes=%d-", &from); err != nil || from < 0 || from > 4096 {
				t.Errorf("resume Range = %q, want bytes=<offset>-", offset)
				return
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-4095/4096", from))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(updateTestPayload(16))
			// The headers and the first bytes leave now: the stall happens
			// mid-body, where the watchdog lives.
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Length", "4096")
		w.Write(updateTestPayload(16))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Then nothing, until the watchdog drops the connection.
		<-r.Context().Done()
	}))
	defer srv.Close()

	body, err := updateTestSource(srv.Client()).Open(context.Background(), srv.URL+"/archive")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer body.Close()
	start := time.Now()
	_, err = io.ReadAll(body)
	if err == nil {
		t.Fatal("a permanently stalled download read to its end")
	}
	if !strings.Contains(err.Error(), "broke") || !strings.Contains(err.Error(), "sent nothing") {
		t.Fatalf("stall error = %v, want the stall named", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != updateResumeTries+1 {
		t.Fatalf("requests = %d, want %d (the first plus every retry)", requests, updateResumeTries+1)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("giving up took %v, want a bounded stop", d)
	}
}

// A server that answers a Range request with the whole file cannot be
// spliced onto the half already read: without a validator it says so as
// an unsupported resume, with one it says the update changed.
func TestUpdateOpenRefusesSplicesWhenTheServerCannotResume(t *testing.T) {
	payload := updateTestPayload(4096)
	for _, tc := range []struct {
		name      string
		validator string
		want      string
	}{
		{"no validator", "", "cannot resume a partial download"},
		{"changed behind a validator", `"v1"`, "changed on the server"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests++
				n := requests
				mu.Unlock()
				if tc.validator != "" {
					w.Header().Set("ETag", tc.validator)
				}
				if n == 1 {
					w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
					w.Write(payload[:100])
					return
				}
				// A 200 where a resume was asked for: the server ignored
				// the Range (and the If-Range).
				w.Write(payload)
			}))
			defer srv.Close()

			body, err := updateTestSource(srv.Client()).Open(context.Background(), srv.URL+"/archive")
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer body.Close()
			got, err := io.ReadAll(body)
			if err == nil {
				t.Fatalf("a %s splice read %d bytes to its end", tc.name, len(got))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("splice error = %v, want %q", err, tc.want)
			}
		})
	}
}

// A slow stream that keeps delivering is never dropped, however slow: the
// watchdog only ends connections that send nothing.
func TestUpdateOpenSlowStreamIsNotKilled(t *testing.T) {
	origStall := updateStallTimeout
	updateStallTimeout = 400 * time.Millisecond
	t.Cleanup(func() { updateStallTimeout = origStall })

	payload := updateTestPayload(8 * 1024)
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		for i := 0; i < len(payload); i += 1024 {
			w.Write(payload[i : i+1024])
			// Slower than a fast link, far alive-er than the watchdog.
			time.Sleep(80 * time.Millisecond)
		}
	}))
	defer srv.Close()

	body, err := updateTestSource(srv.Client()).Open(context.Background(), srv.URL+"/archive")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("a slow download failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("a slow download delivered the wrong bytes")
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Fatalf("requests = %d, want the one slow connection", requests)
	}
}

// The 206's Content-Range must pick up exactly where the stream stopped;
// anything else is refused rather than spliced.
func TestUpdateOpenRefusesAMisalignedResume(t *testing.T) {
	payload := updateTestPayload(4096)
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		w.Header().Set("ETag", `"v1"`)
		if n == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Write(payload[:100])
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", 50, len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[50:])
	}))
	defer srv.Close()

	body, err := updateTestSource(srv.Client()).Open(context.Background(), srv.URL+"/archive")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer body.Close()
	_, err = io.ReadAll(body)
	if err == nil {
		t.Fatal("a misaligned resume read to its end")
	}
	if !strings.Contains(err.Error(), "resumed at byte 50, want 100") {
		t.Fatalf("misaligned resume error = %v", err)
	}
}

func TestParseContentRange(t *testing.T) {
	for _, tc := range []struct {
		v     string
		start int64
		total int64
		ok    bool
	}{
		{"bytes 100-199/1234", 100, 1234, true},
		{"bytes 0-99/*", 0, -1, true},
		{"bytes 7-8/9", 7, 9, true},
		{"bytes 100-199", 0, -1, false},
		{"bytes x-199/1234", 0, -1, false},
		{"octets 100-199/1234", 0, -1, false},
		{"", 0, -1, false},
	} {
		start, total, ok := parseContentRange(tc.v)
		if ok != tc.ok || (ok && (start != tc.start || total != tc.total)) {
			t.Errorf("parseContentRange(%q) = %d, %d, %v; want %d, %d, %v", tc.v, start, total, ok, tc.start, tc.total, tc.ok)
		}
	}
}

// preview is the first few bytes of a mismatched stream, for the failure.
func preview(b []byte) string {
	if len(b) > 8 {
		b = b[:8]
	}
	return fmt.Sprintf("%x", b)
}
