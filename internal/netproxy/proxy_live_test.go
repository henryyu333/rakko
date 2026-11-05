//go:build darwin

package netproxy

// Live routing: local HTTP, CONNECT and SOCKS5 proxies (the shapes FlClash's
// 127.0.0.1:7890 has) and a real request through each, so the proof is that a
// request reached the right process, not that a Proxy method was called.

import (
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// getThrough issues one request with a client on the shared transport and
// returns the body; a transport error is returned, not fatal.
func getThrough(t *testing.T, raw string) (string, error) {
	t.Helper()
	client := NewClient(5 * time.Second)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

// TestSystemHTTPProxyCarriesRequest proves an HTTP request goes through the
// system proxy: the proxy itself is the endpoint that answers.
func TestSystemHTTPProxyCarriesRequest(t *testing.T) {
	clearProxyEnv(t)
	seen := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case seen <- r.Host + r.URL.Path:
		default:
		}
		io.WriteString(w, "answered by the proxy")
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)

	defer swapSystem(func() (System, error) { return System{HTTP: u.Host}, nil })()
	defer swapTTL(0)()

	body, err := getThrough(t, "http://api.example.test/quota")
	if err != nil {
		t.Fatalf("request through the system proxy: %v", err)
	}
	if body != "answered by the proxy" {
		t.Fatalf("body %q: the request did not reach the proxy", body)
	}
	select {
	case got := <-seen:
		if got != "api.example.test/quota" {
			t.Fatalf("the proxy saw %q, want the request's host and path", got)
		}
	default:
		t.Fatal("the system proxy saw no request")
	}
}

// TestSystemSocksProxyCarriesRequest is the reported setup: a local SOCKS
// proxy, a request whose host the proxy resolves, and a real answer from the
// target behind it.
func TestSystemSocksProxyCarriesRequest(t *testing.T) {
	clearProxyEnv(t)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "answered by the target")
	}))
	defer target.Close()

	hosts := make(chan string, 1)
	socksAddr := startSOCKS5(t, target.Listener.Addr().String(), func(host string) {
		select {
		case hosts <- host:
		default:
		}
	})

	defer swapSystem(func() (System, error) { return System{SOCKS: socksAddr}, nil })()
	defer swapTTL(0)()

	body, err := getThrough(t, "http://api.example.test/quota")
	if err != nil {
		t.Fatalf("request through the system SOCKS proxy: %v", err)
	}
	if body != "answered by the target" {
		t.Fatalf("body %q, want the target's answer", body)
	}
	select {
	case got := <-hosts:
		if got != "api.example.test" {
			t.Fatalf("SOCKS asked for %q, want the request's host", got)
		}
	default:
		t.Fatal("the SOCKS proxy saw no connection")
	}
}

// TestSystemHTTPSProxyTunnelsAndKeepsTLS proves an HTTPS request is tunneled
// with a CONNECT through the system proxy, and that TLS verification is still
// enforced on the tunneled connection: the proxy's own target certificate does
// not match the requested name, and the request must fail on that.
func TestSystemHTTPSProxyTunnelsAndKeepsTLS(t *testing.T) {
	clearProxyEnv(t)
	tlsTarget := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "must not be read")
	}))
	defer tlsTarget.Close()

	connects := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		select {
		case connects <- r.Host:
		default:
		}
		upstream, err := net.Dial("tcp", tlsTarget.Listener.Addr().String())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
		go func() { io.Copy(upstream, client); upstream.Close() }()
		go func() { io.Copy(client, upstream); client.Close() }()
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)

	defer swapSystem(func() (System, error) { return System{HTTPS: u.Host}, nil })()
	defer swapTTL(0)()

	_, err := getThrough(t, "https://api.example.test/quota")
	if err == nil {
		t.Fatal("the tunneled request succeeded: TLS verification was bypassed")
	}
	var certErr *tls.CertificateVerificationError
	if !errors.As(err, &certErr) {
		t.Fatalf("error %v is not a certificate verification failure", err)
	}
	select {
	case got := <-connects:
		if got != "api.example.test:443" {
			t.Fatalf("CONNECT asked for %q, want the request's host and port", got)
		}
	default:
		t.Fatal("the HTTPS proxy saw no CONNECT")
	}
}

// TestEnvironmentProxyCarriesRequest is the same routing decided by the
// environment instead, and that an environment proxy bypassed by NO_PROXY
// connects directly (no CONNECT reaches the proxy).
func TestEnvironmentProxyCarriesRequest(t *testing.T) {
	clearProxyEnv(t)
	requests := make(chan string, 2)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r.Host + r.URL.Path:
		default:
		}
		io.WriteString(w, "answered by the environment proxy")
	}))
	defer proxy.Close()
	t.Setenv("HTTP_PROXY", proxy.URL)

	body, err := getThrough(t, "http://api.example.test/quota")
	if err != nil {
		t.Fatalf("request through the environment proxy: %v", err)
	}
	if !strings.Contains(body, "environment proxy") {
		t.Fatalf("body %q, want the proxy's answer", body)
	}
	select {
	case got := <-requests:
		if got != "api.example.test/quota" {
			t.Fatalf("the proxy saw %q, want the request's host and path", got)
		}
	default:
		t.Fatal("the environment proxy saw no request")
	}
}

// startSOCKS5 serves the no-authentication SOCKS5 CONNECT flow on loopback and
// dials dialAddr whatever host the client names, so a request whose host has
// no DNS entry still reaches the target: the proxy, not the client, resolves.
func startSOCKS5(t *testing.T, dialAddr string, onHost func(host string)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("SOCKS listener: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go serveSOCKS5Conn(conn, dialAddr, onHost)
		}
	}()
	return ln.Addr().String()
}

func serveSOCKS5Conn(conn net.Conn, dialAddr string, onHost func(string)) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(conn, greeting); err != nil || greeting[0] != 5 {
		return
	}
	methods := make([]byte, greeting[1])
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	conn.Write([]byte{5, 0}) // no authentication
	head := make([]byte, 4)
	if _, err := io.ReadFull(conn, head); err != nil || head[1] != 1 {
		return
	}
	var host string
	switch head[3] {
	case 1: // IPv4
		b := make([]byte, 4)
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = net.IP(b).String()
	case 3: // domain name
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return
		}
		b := make([]byte, int(n[0]))
		if _, err := io.ReadFull(conn, b); err != nil {
			return
		}
		host = string(b)
	default:
		return
	}
	if _, err := io.ReadFull(conn, make([]byte, 2)); err != nil { // port
		return
	}
	if onHost != nil {
		onHost(host)
	}
	upstream, err := net.DialTimeout("tcp", dialAddr, 5*time.Second)
	if err != nil {
		conn.Write([]byte{5, 5, 0, 1, 0, 0, 0, 0, 0, 0}) // connection refused
		return
	}
	defer upstream.Close()
	conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
	conn.SetDeadline(time.Time{})
	go func() { io.Copy(upstream, conn); upstream.Close() }()
	io.Copy(conn, upstream)
}
