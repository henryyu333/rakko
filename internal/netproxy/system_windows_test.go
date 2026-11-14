//go:build windows

package netproxy

import (
	"net/url"
	"testing"
)

func TestParseProxyServer(t *testing.T) {
	for _, tc := range []struct {
		in                 string
		http, https, socks string
		name               string
	}{
		{"127.0.0.1:7890", "127.0.0.1:7890", "127.0.0.1:7890", "127.0.0.1:7890", "one address for every protocol"},
		{"http=10.0.0.1:8080;https=10.0.0.2:8443;socks=10.0.0.3:1080", "10.0.0.1:8080", "10.0.0.2:8443", "10.0.0.3:1080", "per-protocol list"},
		{"HTTP=10.0.0.1:8080; HTTPS = 10.0.0.1:8443 ", "10.0.0.1:8080", "10.0.0.1:8443", "", "scheme names are case- and space-insensitive"},
		{"proxy.corp:3128;ftp=ftp.corp:21", "proxy.corp:3128", "proxy.corp:3128", "proxy.corp:3128", "bare entry serves the unnamed protocols"},
		{"socks=127.0.0.1:1080", "", "", "127.0.0.1:1080", "SOCKS only"},
		{"http=;https=10.0.0.2:8443", "", "10.0.0.2:8443", "", "empty scheme address is off"},
		{"", "", "", "", "not set"},
	} {
		h, hs, s := parseProxyServer(tc.in)
		if h != tc.http || hs != tc.https || s != tc.socks {
			t.Errorf("%s: %q → %q, %q, %q; want %q, %q, %q", tc.name, tc.in, h, hs, s, tc.http, tc.https, tc.socks)
		}
	}
}

func TestParseProxyOverride(t *testing.T) {
	for _, tc := range []struct {
		in            string
		bypass        string
		excludeSimple bool
	}{
		{"", "", false},
		{"*.local;10.*;192.168.*", "*.local,10.*,192.168.*", false},
		{"*.local;<local>", "*.local", true},
		{"<LOCAL>;intranet.example.com;", "intranet.example.com", true},
	} {
		bypass, exclude := parseProxyOverride(tc.in)
		if bypass != tc.bypass || exclude != tc.excludeSimple {
			t.Errorf("%q → %q, %v; want %q, %v", tc.in, bypass, exclude, tc.bypass, tc.excludeSimple)
		}
	}
}

// The registry values map onto the System model exactly as WinINET reads
// them, and a disabled proxy ignores a leftover server entry.
func TestSystemFromSettings(t *testing.T) {
	s := systemFromSettings(true, "http=10.0.0.1:8080;https=10.0.0.1:8443;socks=10.0.0.1:1080",
		"*.local;<local>", "http://wpad.example/proxy.pac", true)
	if s.HTTP != "10.0.0.1:8080" || s.HTTPS != "10.0.0.1:8443" || s.SOCKS != "10.0.0.1:1080" {
		t.Fatalf("addresses = %q, %q, %q", s.HTTP, s.HTTPS, s.SOCKS)
	}
	if s.Bypass != "*.local" || !s.ExcludeSimple || !s.AutoConfig || !s.AutoDiscover {
		t.Fatalf("bypass/PAC = %q, %v, %v, %v", s.Bypass, s.ExcludeSimple, s.AutoConfig, s.AutoDiscover)
	}
	u, err := url.Parse("https://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := s.proxyFor(u)
	if err != nil || proxy == nil || proxy.Host != "10.0.0.1:8443" {
		t.Fatalf("https through the HTTPS proxy = %v, %v", proxy, err)
	}
	local, err := url.Parse("https://printer/")
	if err != nil {
		t.Fatal(err)
	}
	if proxy, _ := s.proxyFor(local); proxy != nil {
		t.Fatalf("a dotless name went through %v despite <local>", proxy)
	}

	off := systemFromSettings(false, "10.0.0.1:8080", "", "", false)
	if off.HTTP != "" || off.HTTPS != "" || off.SOCKS != "" {
		t.Fatalf("disabled proxy still reported %+v", off)
	}
}

// TestReadSystemProxies: the production read returns well-formed addresses
// from this machine's settings; it only reads them.
func TestReadSystemProxies(t *testing.T) {
	if testing.Short() {
		t.Skip("reads the machine's network configuration")
	}
	s, err := readSystemProxies()
	if err != nil {
		t.Fatalf("reading the system proxy settings: %v", err)
	}
	for _, probe := range []struct{ name, addr string }{
		{"HTTP", s.HTTP}, {"HTTPS", s.HTTPS}, {"SOCKS", s.SOCKS},
	} {
		if probe.addr == "" {
			continue
		}
		u, err := url.Parse("http://" + probe.addr)
		if err != nil || u.Host != probe.addr {
			t.Fatalf("%s proxy %q is not host:port: %v", probe.name, probe.addr, err)
		}
	}
	t.Logf("system proxies: HTTP=%q HTTPS=%q SOCKS=%q bypass=%q local=%v pac=%v auto=%v",
		s.HTTP, s.HTTPS, s.SOCKS, s.Bypass, s.ExcludeSimple, s.AutoConfig, s.AutoDiscover)
}
