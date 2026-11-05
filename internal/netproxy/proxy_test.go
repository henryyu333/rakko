package netproxy

// The proxy rules: which source decides a request, which proxy a scheme
// takes, what the system bypass list does, and that a changed system setting
// is read again instead of being cached for the process's life. No test here
// needs the network: the live routing is proxy_live_test.go.

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

// swapSystem replaces the platform read for the duration of a test and
// starts it from a cold cache.
func swapSystem(fn func() (System, error)) func() {
	old := readSystem
	readSystem = fn
	resetSystem()
	return func() {
		readSystem = old
		resetSystem()
	}
}

// swapTTL changes how long a system read is cached.
func swapTTL(d time.Duration) func() {
	old := systemTTL
	systemTTL = d
	resetSystem()
	return func() {
		systemTTL = old
		resetSystem()
	}
}

func resetSystem() {
	systemMu.Lock()
	systemAt, systemValue = time.Time{}, System{}
	systemMu.Unlock()
}

// clearProxyEnv removes every proxy variable the rules read.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"http_proxy", "HTTP_PROXY", "https_proxy", "HTTPS_PROXY",
		"no_proxy", "NO_PROXY", "REQUEST_METHOD",
	} {
		t.Setenv(k, "")
	}
}

// resolve asks the resolver for one URL's proxy, the way a transport does.
func resolve(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	req := &http.Request{URL: u, Method: http.MethodGet}
	proxy, err := ProxyFor(req)
	if err != nil {
		t.Fatalf("proxy for %q: %v", raw, err)
	}
	return proxy
}

func wantProxy(t *testing.T, raw, want string) {
	t.Helper()
	proxy := resolve(t, raw)
	if want == "" {
		if proxy != nil {
			t.Fatalf("%s: proxy %q, want a direct connection", raw, proxy)
		}
		return
	}
	if proxy == nil {
		t.Fatalf("%s: direct, want %s", raw, want)
	}
	if proxy.String() != want {
		t.Fatalf("%s: proxy %q, want %q", raw, proxy, want)
	}
}

// The environment decides alone when it configures anything: no system
// setting is consulted, and a scheme the environment leaves unset is direct.
func TestEnvironmentDecidesAlone(t *testing.T) {
	clearProxyEnv(t)
	defer swapSystem(func() (System, error) {
		return System{HTTP: "10.0.0.9:3128", HTTPS: "10.0.0.9:3129", SOCKS: "10.0.0.9:1080"}, nil
	})()

	t.Setenv("http_proxy", "http://env-proxy.test:8080")
	wantProxy(t, "http://example.test/", "http://env-proxy.test:8080")
	wantProxy(t, "https://example.test/", "")
}

// HTTPS_PROXY covers https, and HTTP falls back to nothing.
func TestEnvironmentHTTPS(t *testing.T) {
	clearProxyEnv(t)
	defer swapSystem(func() (System, error) {
		return System{HTTP: "10.0.0.9:3128"}, nil
	})()
	t.Setenv("HTTPS_PROXY", "http://secure-proxy.test:8443")
	wantProxy(t, "https://example.test/", "http://secure-proxy.test:8443")
	wantProxy(t, "http://example.test/", "")
}

// An environment that only says "do not proxy these" is a bypass decision,
// not a missing configuration: the system proxy must not take over.
func TestNoProxyAloneNeverFallsBack(t *testing.T) {
	clearProxyEnv(t)
	defer swapSystem(func() (System, error) {
		return System{HTTP: "10.0.0.9:3128", HTTPS: "10.0.0.9:3129"}, nil
	})()
	t.Setenv("NO_PROXY", "example.test")
	wantProxy(t, "http://example.test/", "")
	wantProxy(t, "http://other.test/", "")
}

// NO_PROXY applies to the environment's proxy only.
func TestEnvironmentBypass(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://env-proxy.test:8443")
	t.Setenv("NO_PROXY", "bypassed.test,.internal.test,10.1.0.0/16,192.168.1.4,127.0.0.1")
	for _, raw := range []string{
		"https://bypassed.test/",
		"https://api.bypassed.test/",
		"https://host.internal.test/",
		"https://10.1.2.3/",
		"https://192.168.1.4/",
		"https://127.0.0.1/",
		"https://localhost/",
	} {
		wantProxy(t, raw, "")
	}
	wantProxy(t, "https://internal.test/", "http://env-proxy.test:8443") // a leading dot is subdomains only
	wantProxy(t, "https://elsewhere.test/", "http://env-proxy.test:8443")
}

// Without a proxy in the environment the system settings decide each scheme.
func TestSystemProxyPerScheme(t *testing.T) {
	clearProxyEnv(t)

	defer swapSystem(func() (System, error) {
		return System{HTTP: "10.0.0.9:3128", HTTPS: "10.0.0.9:3129"}, nil
	})()
	wantProxy(t, "http://example.test/", "http://10.0.0.9:3128")
	wantProxy(t, "https://example.test/", "http://10.0.0.9:3129")

	// No HTTPS proxy: HTTPS goes through the HTTP proxy as a CONNECT.
	defer swapSystem(func() (System, error) {
		return System{HTTP: "10.0.0.9:3128"}, nil
	})()
	wantProxy(t, "https://example.test/", "http://10.0.0.9:3128")

	// SOCKS covers what the HTTP proxies do not.
	defer swapSystem(func() (System, error) {
		return System{SOCKS: "10.0.0.9:1080"}, nil
	})()
	wantProxy(t, "https://example.test/", "socks5://10.0.0.9:1080")
	wantProxy(t, "http://example.test/", "socks5://10.0.0.9:1080")

	// The HTTP proxy wins over SOCKS for both schemes when no HTTPS proxy
	// is set: HTTPS is a CONNECT through it.
	defer swapSystem(func() (System, error) {
		return System{HTTP: "10.0.0.9:3128", SOCKS: "10.0.0.9:1080"}, nil
	})()
	wantProxy(t, "http://example.test/", "http://10.0.0.9:3128")
	wantProxy(t, "https://example.test/", "http://10.0.0.9:3128")
}

// The system exception list bypasses the system proxy, and loopback is
// always direct.
func TestSystemBypass(t *testing.T) {
	clearProxyEnv(t)
	defer swapSystem(func() (System, error) {
		return System{
			HTTP: "10.0.0.9:3128", HTTPS: "10.0.0.9:3128",
			Bypass: "*.local,169.254/16,intranet.example.com,192.168.0.0/16",
		}, nil
	})()
	for _, raw := range []string{
		"https://printer.local/",
		"http://nas.local/",
		"https://169.254.10.2/",
		"https://intranet.example.com/",
		"https://files.intranet.example.com/",
		"https://192.168.3.4/",
		"https://127.0.0.1:8443/",
		"https://localhost:8443/",
	} {
		wantProxy(t, raw, "")
	}
	wantProxy(t, "https://example.com/", "http://10.0.0.9:3128")
	wantProxy(t, "https://intranet.example.com.evil.test/", "http://10.0.0.9:3128")
}

// A changed system setting is read again: a cached read is never the
// process's last word.
func TestSystemSettingChangeApplies(t *testing.T) {
	clearProxyEnv(t)
	settings := System{HTTP: "10.0.0.9:3128"}
	defer swapSystem(func() (System, error) { return settings, nil })()
	defer swapTTL(0)() // every resolution reads again

	wantProxy(t, "http://example.test/", "http://10.0.0.9:3128")
	settings = System{HTTP: "10.0.0.20:8888"}
	wantProxy(t, "http://example.test/", "http://10.0.0.20:8888")
	settings = System{}
	wantProxy(t, "http://example.test/", "")
}

// Within the cache window the read is not repeated; past it, it is.
func TestSystemReadIsCachedForTheWindow(t *testing.T) {
	clearProxyEnv(t)
	reads := 0
	settings := System{HTTP: "10.0.0.9:3128"}
	defer swapSystem(func() (System, error) {
		reads++
		return settings, nil
	})()
	defer swapTTL(time.Hour)()

	wantProxy(t, "http://example.test/", "http://10.0.0.9:3128")
	settings = System{HTTP: "10.0.0.20:8888"}
	wantProxy(t, "http://example.test/", "http://10.0.0.9:3128") // still the cached read
	if reads != 1 {
		t.Fatalf("the settings were read %d times inside the window, want 1", reads)
	}
	resetSystem()
	wantProxy(t, "http://example.test/", "http://10.0.0.20:8888")
}

// A failed platform read is a direct connection, not an error the request
// path has to carry.
func TestSystemReadFailureIsDirect(t *testing.T) {
	clearProxyEnv(t)
	defer swapSystem(func() (System, error) { return System{}, errTestRead })()
	wantProxy(t, "http://example.test/", "")
}

func TestAutoConfigIsNotEvaluated(t *testing.T) {
	clearProxyEnv(t)
	defer swapSystem(func() (System, error) {
		return System{AutoConfig: true}, nil
	})()
	defer func() {
		pacNoteMu.Lock()
		pacNoted = false
		pacNoteMu.Unlock()
	}()
	wantProxy(t, "https://example.test/", "")
	pacNoteMu.Lock()
	noted := pacNoted
	pacNoteMu.Unlock()
	if !noted {
		t.Fatal("a PAC configuration was ignored without a word")
	}
}

// The transport hands net/http the resolver and shares one connection pool.
func TestSharedTransport(t *testing.T) {
	first, second := Shared(), Shared()
	if first != second {
		t.Fatal("Shared returned two transports")
	}
	if first.Proxy == nil {
		t.Fatal("the shared transport has no proxy resolver")
	}
	if c := NewClient(3 * time.Second); c.Transport != first || c.Timeout != 3*time.Second {
		t.Fatal("NewClient did not use the shared transport and its timeout")
	}
	if first.TLSClientConfig != nil && first.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("the transport skips TLS verification")
	}
}

func TestBypassWords(t *testing.T) {
	got := bypassWords([]string{" 169.254/16 ", "", "*.local", "192.168.0.0/16", "10/8", "weird/thing"})
	want := "169.254.0.0/16,*.local,192.168.0.0/16,10.0.0.0/8,weird/thing"
	if got != want {
		t.Fatalf("bypass words %q, want %q", got, want)
	}
}

// The mygo updater's client is http.DefaultClient; installing ours routes it
// through the same rules and leaves the request deadline to the caller.
func TestInstallDefaultClient(t *testing.T) {
	old := http.DefaultClient
	defer func() { http.DefaultClient = old }()
	InstallDefaultClient()
	if http.DefaultClient.Transport != Shared() {
		t.Fatal("http.DefaultClient does not use the shared transport")
	}
	if http.DefaultClient.Timeout != 0 {
		t.Fatal("http.DefaultClient gained a timeout: the updater bounds its own requests")
	}
}

var errTestRead = errFailedRead{}

type errFailedRead struct{}

func (errFailedRead) Error() string { return "no settings" }
