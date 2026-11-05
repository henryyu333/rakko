// Package netproxy builds the HTTP transport for the app's outbound
// requests. A process started from Finder inherits no proxy environment
// variables, so the macOS system proxy settings — what System Settings ▸
// Network ▸ Proxies holds and what tools such as FlClash write — decide
// where a request goes when the environment is silent.
//
// Precedence, per request:
//
//  1. HTTP_PROXY, HTTPS_PROXY or NO_PROXY (lowercase wins, as in Go) decide
//     alone, with the semantics of http.ProxyFromEnvironment: NO_PROXY
//     bypass, the loopback rule, the CGI guard. An environment that only
//     carries NO_PROXY still counts — it is a bypass decision, not a missing
//     configuration, and never falls through to the system settings.
//  2. Otherwise the system proxy settings apply exactly as CFNetwork reads
//     them: HTTP for http, HTTPS for https (a CONNECT through the HTTPS
//     proxy), SOCKS after either — never the other scheme's proxy — minus the
//     system's own bypass rules (ExceptionsList and ExcludeSimpleHostnames).
//  3. Proxy auto-configuration (PAC) and auto-discovery are not evaluated:
//     while only a PAC script is configured the app connects directly, and
//     says so once in the log rather than pretending the setting works.
//
// The system settings are read through SystemConfiguration and cached for
// systemTTL: changing them takes effect within that window without a
// relaunch, and no process is started per request. Nothing here weakens TLS
// verification or touches a request's headers; a proxy password in an
// environment variable stays in the request's URL and is never logged.
package netproxy

import (
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http/httpproxy"
)

// System is one read of the macOS proxy settings that decide where outbound
// requests go.
type System struct {
	// HTTP, HTTPS and SOCKS are the enabled proxies as "host:port"; an
	// empty one is switched off.
	HTTP, HTTPS, SOCKS string
	// Bypass is the system exception list as written ("*.local,
	// 169.254/16").
	Bypass string
	// ExcludeSimple is ExcludeSimpleHostnames: a host name with no dot (an
	// intranet name, not an address) bypasses the proxy.
	ExcludeSimple bool
	// AutoConfig and AutoDiscover report a proxy auto-configuration (PAC)
	// script or auto-discovery; neither is evaluated (package doc).
	AutoConfig, AutoDiscover bool
}

const (
	// defaultSystemTTL bounds how stale a cached system read may get.
	defaultSystemTTL = 2 * time.Second
)

var (
	// readSystem reads the platform's proxy settings; tests replace it.
	readSystem = readSystemProxies
	// systemTTL is a variable so a test can force a fresh read.
	systemTTL = defaultSystemTTL

	systemMu    sync.Mutex
	systemAt    time.Time
	systemValue System

	sharedOnce sync.Once
	shared     *http.Transport

	pacNoteMu sync.Mutex
	pacNoted  bool
)

// ProxyFor is the transport's Proxy hook: the environment's proxying when it
// configures any, else the macOS system proxy settings.
func ProxyFor(req *http.Request) (*url.URL, error) {
	if cfg := envProxy(); cfg != nil {
		return cfg.ProxyFunc()(req.URL)
	}
	return systemNow().proxyFor(req.URL)
}

// envProxy returns the environment's proxy configuration, or nil when the
// environment says nothing about proxying at all.
func envProxy() *httpproxy.Config {
	cfg := httpproxy.FromEnvironment()
	if cfg.HTTPProxy == "" && cfg.HTTPSProxy == "" && cfg.NoProxy == "" {
		return nil
	}
	return cfg
}

// systemNow returns the system settings, reading them again once the cache
// is older than systemTTL. A failed read is a direct connection, the same
// as no configuration, and is retried after the same window.
func systemNow() System {
	systemMu.Lock()
	defer systemMu.Unlock()
	if !systemAt.IsZero() && time.Since(systemAt) < systemTTL {
		return systemValue
	}
	v, _ := readSystem()
	systemAt, systemValue = time.Now(), v
	return systemValue
}

// proxyFor picks the system proxy for one request the way CFNetwork answers
// CFNetworkCopyProxiesForURL for the same dictionary: the scheme's own proxy,
// else SOCKS, and the system's own bypass rules. No proxy is invented across
// schemes — a Mac with only an HTTP proxy configured does not proxy HTTPS.
func (s System) proxyFor(reqURL *url.URL) (*url.URL, error) {
	addr := s.address(reqURL.Scheme)
	if addr == "" {
		s.noteAutoConfig()
		return nil, nil
	}
	if s.bypassed(reqURL.Hostname()) {
		return nil, nil
	}
	return url.Parse(addr)
}

// address is the proxy the scheme goes through, in the order CFNetwork
// returns it: HTTP for http, HTTPS for https (a CONNECT through the HTTPS
// proxy), and SOCKS after either — for both schemes. The other scheme's HTTP
// proxy is never a fallback.
func (s System) address(scheme string) string {
	switch scheme {
	case "https":
		return firstNonEmpty(httpURL(s.HTTPS), socksURL(s.SOCKS))
	case "http":
		return firstNonEmpty(httpURL(s.HTTP), socksURL(s.SOCKS))
	}
	return socksURL(s.SOCKS)
}

// bypassed reports whether the system sends this request direct:
// ExcludeSimpleHostnames for a name with no dot, else an ExceptionsList entry.
// The rules match CFNetwork's (an entry matches the host and its subdomains; a
// leading "*." or "." matches subdomains only; an address, CIDR or "*" as
// written). Unlike Go's environment rule there is no implicit loopback
// bypass: CFNetwork proxies 127.0.0.1 and localhost when the system proxy is
// on, and this follows it.
func (s System) bypassed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	if s.ExcludeSimple && net.ParseIP(host) == nil && !strings.Contains(host, ".") {
		return true
	}
	for _, rule := range strings.Split(s.Bypass, ",") {
		if bypassMatches(rule, host) {
			return true
		}
	}
	return false
}

// bypassMatches reports whether one ExceptionsList entry covers host.
func bypassMatches(rule, host string) bool {
	rule = strings.ToLower(strings.TrimSpace(rule))
	if rule == "" {
		return false
	}
	if rule == "*" {
		return true
	}
	if strings.Contains(rule, "/") {
		_, network, err := net.ParseCIDR(expandPrefix(rule))
		ip := net.ParseIP(host)
		return err == nil && ip != nil && network.Contains(ip)
	}
	if ip := net.ParseIP(rule); ip != nil {
		other := net.ParseIP(host)
		return other != nil && ip.Equal(other)
	}
	if h, _, err := net.SplitHostPort(rule); err == nil {
		rule = h
	} else if i := strings.LastIndexByte(rule, ':'); i > 0 && allDigits(rule[i+1:]) {
		rule = rule[:i] // "host:port" without brackets
	}
	switch {
	case strings.HasPrefix(rule, "*."):
		return strings.HasSuffix(host, rule[1:]) // ".example.test"
	case strings.HasPrefix(rule, "."):
		return strings.HasSuffix(host, rule)
	}
	return host == rule || strings.HasSuffix(host, "."+rule)
}

func httpURL(addr string) string {
	if addr == "" {
		return ""
	}
	return "http://" + addr
}

func socksURL(addr string) string {
	if addr == "" {
		return ""
	}
	return "socks5://" + addr
}

// noteAutoConfig says once, in the log, that an auto-configuration is on and
// is not evaluated.
func (s System) noteAutoConfig() {
	if !s.AutoConfig && !s.AutoDiscover {
		return
	}
	pacNoteMu.Lock()
	noted := pacNoted
	pacNoted = true
	pacNoteMu.Unlock()
	if !noted {
		log.Printf("netproxy: macOS proxy auto-configuration (PAC) is on; Rakko does not evaluate PAC scripts and connects directly")
	}
}

// expandPrefix fills the missing octets of an IPv4 prefix ("169.254/16" →
// "169.254.0.0/16"), the shorthand Apple's ExceptionsList uses. Anything else
// is returned unchanged.
func expandPrefix(entry string) string {
	addr, bits, ok := strings.Cut(entry, "/")
	if !ok {
		return entry
	}
	if !allDigits(bits) {
		return entry
	}
	octets := strings.Split(addr, ".")
	if len(octets) >= 4 {
		return entry
	}
	for len(octets) < 4 {
		octets = append(octets, "0")
	}
	for _, o := range octets {
		if !allDigits(o) {
			return entry // a hostname with a slash: leave it to the matcher
		}
	}
	return strings.Join(octets, ".") + "/" + bits
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// Shared returns the process's one transport: every client that goes through
// the proxy rules shares its connection pools. Safe on any goroutine.
func Shared() *http.Transport {
	sharedOnce.Do(func() {
		base, ok := http.DefaultTransport.(*http.Transport)
		t := base
		if ok {
			t = base.Clone()
		} else {
			t = &http.Transport{}
		}
		t.Proxy = ProxyFor
		shared = t
	})
	return shared
}

// NewClient returns a client over Shared with the given overall timeout. A
// zero timeout is net/http's own (none): the caller bounds its requests with
// a context.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: Shared()}
}

// InstallDefaultClient routes http.DefaultClient through the same rules. The
// mygo updater is the one outbound path with no client to inject: it calls
// http.DefaultClient directly, so that is the hook. Call it once from main
// before anything can issue a request. The client keeps a zero timeout; the
// updater bounds its own requests with a context.
func InstallDefaultClient() {
	http.DefaultClient = &http.Client{Transport: Shared()}
}
