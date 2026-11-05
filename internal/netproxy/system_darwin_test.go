//go:build darwin

package netproxy

// The platform read: the shape of what it reports on this machine, and the
// parsing of a proxy dictionary driven with the same CoreFoundation objects
// SystemConfiguration hands over. The latter needs no system change (it never
// touches the machine's settings): it builds a dictionary in this process and
// parses it through the production path.

import (
	"net/url"
	"strconv"
	"sync"
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"
)

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
	t.Logf("system proxies: HTTP=%q HTTPS=%q SOCKS=%q bypass=%q pac=%v auto=%v",
		s.HTTP, s.HTTPS, s.SOCKS, s.Bypass, s.AutoConfig, s.AutoDiscover)
}

// CoreFoundation constructors the dictionary test needs. Registering them
// here keeps the platform layer free of code only a test calls.
var (
	cfTestOnce sync.Once
	cfTestErr  error

	cfDictCreate   func(alloc, keys, values uintptr, n int64, keyCB, valueCB uintptr) uintptr
	cfArrayCreate  func(alloc, values uintptr, n int64, cbs uintptr) uintptr
	cfNumberCreate func(alloc uintptr, typ int32, v *int32) uintptr
	cfKeyCB        uintptr
	cfValueCB      uintptr
	cfArrayCB      uintptr
	cfTrue         uintptr
	cfFalse        uintptr
)

func loadCFTest() error {
	cfTestOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cfTestErr = err
			return
		}
		purego.RegisterLibFunc(&cfDictCreate, lib, "CFDictionaryCreate")
		purego.RegisterLibFunc(&cfArrayCreate, lib, "CFArrayCreate")
		purego.RegisterLibFunc(&cfNumberCreate, lib, "CFNumberCreate")
		for name, dst := range map[string]*uintptr{
			"kCFTypeDictionaryKeyCallBacks":   &cfKeyCB,
			"kCFTypeDictionaryValueCallBacks": &cfValueCB,
			"kCFTypeArrayCallBacks":           &cfArrayCB,
		} {
			addr, err := purego.Dlsym(lib, name)
			if err != nil {
				cfTestErr = err
				return
			}
			*dst = addr // the symbol is the callbacks struct itself
		}
		var copySymbol func(dst *uintptr, src, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		for name, dst := range map[string]*uintptr{"kCFBooleanTrue": &cfTrue, "kCFBooleanFalse": &cfFalse} {
			addr, err := purego.Dlsym(lib, name)
			if err != nil {
				cfTestErr = err
				return
			}
			copySymbol(dst, addr, unsafe.Sizeof(*dst)) // the symbol points at the ref
		}
	})
	return cfTestErr
}

// cfDict builds a CFDictionary from a plain map, releasing the objects it
// owns once the dictionary holds them.
func cfDict(t *testing.T, entries map[string]any) uintptr {
	t.Helper()
	if err := loadCFTest(); err != nil {
		t.Fatalf("CoreFoundation test setup: %v", err)
	}
	keys := make([]uintptr, 0, len(entries))
	values := make([]uintptr, 0, len(entries))
	for k, v := range entries {
		keys = append(keys, cfString(k))
		values = append(values, cfObject(t, v))
	}
	dict := cfDictCreate(0, uintptr(unsafe.Pointer(&keys[0])), uintptr(unsafe.Pointer(&values[0])),
		int64(len(keys)), cfKeyCB, cfValueCB)
	for _, v := range append(keys, values...) {
		cfReleaseSys(v)
	}
	return dict
}

func cfObject(t *testing.T, v any) uintptr {
	t.Helper()
	switch x := v.(type) {
	case string:
		return cfString(x)
	case int:
		n := int32(x)
		return cfNumberCreate(0, cfNumberInt, &n)
	case bool:
		if x {
			return cfTrue
		}
		return cfFalse
	case []string:
		values := make([]uintptr, 0, len(x))
		for _, s := range x {
			values = append(values, cfString(s))
		}
		arr := cfArrayCreate(0, uintptr(unsafe.Pointer(&values[0])), int64(len(values)), cfArrayCB)
		for _, v := range values {
			cfReleaseSys(v)
		}
		return arr
	}
	t.Fatalf("cfObject: unsupported %T", v)
	return 0
}

// A dictionary shaped the way SCSchemaDefinitions documents it — the switches
// are CFNumber 0/1, and so is ExcludeSimpleHostnames — parsed by the
// production code path.
func TestParseProxies(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatalf("system setup: %v", err)
	}
	dict := cfDict(t, map[string]any{
		"HTTPEnable":             1,
		"HTTPProxy":              "127.0.0.1",
		"HTTPPort":               7890,
		"HTTPSEnable":            1,
		"HTTPSProxy":             "127.0.0.1",
		"HTTPSPort":              7891,
		"SOCKSEnable":            1,
		"SOCKSProxy":             "127.0.0.1",
		"SOCKSPort":              7892,
		"ExceptionsList":         []string{"*.local", "169.254/16", " intranet.example.com "},
		"ExcludeSimpleHostnames": 1,
	})
	defer cfReleaseSys(dict)
	got := parseProxies(dict)
	want := System{
		HTTP: "127.0.0.1:7890", HTTPS: "127.0.0.1:7891", SOCKS: "127.0.0.1:7892",
		Bypass: "*.local,169.254/16,intranet.example.com", ExcludeSimple: true,
	}
	if got != want {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
}

// A switched-off proxy (CFNumber 0) is off no matter what host it names; a
// wrong-typed or incomplete entry is off too, never "host:0".
func TestParseProxiesIgnoresIncompleteEntries(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatalf("system setup: %v", err)
	}
	dict := cfDict(t, map[string]any{
		"HTTPEnable":  0,
		"HTTPProxy":   "127.0.0.1",
		"HTTPPort":    7890,
		"HTTPSEnable": 1,
		"HTTPSProxy":  "127.0.0.1",
		"HTTPSPort":   0,
		"SOCKSEnable": "yes", // a string where a switch belongs
		"SOCKSProxy":  "127.0.0.1",
		"SOCKSPort":   7892,
	})
	defer cfReleaseSys(dict)
	if got := parseProxies(dict); got != (System{}) {
		t.Fatalf("parsed %+v, want no proxy", got)
	}
}

// Auto-configuration is reported when it is on and names a script; discovery
// on its own.
func TestParseProxiesAutoConfig(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatalf("system setup: %v", err)
	}
	on := cfDict(t, map[string]any{
		"ProxyAutoConfigEnable":    1,
		"ProxyAutoConfigURLString": "http://127.0.0.1:1/proxy.pac",
	})
	defer cfReleaseSys(on)
	if got := parseProxies(on); !got.AutoConfig || got.AutoDiscover {
		t.Fatalf("parsed %+v, want auto-configuration only", got)
	}
	noScript := cfDict(t, map[string]any{"ProxyAutoConfigEnable": 1})
	defer cfReleaseSys(noScript)
	if got := parseProxies(noScript); got.AutoConfig {
		t.Fatalf("parsed %+v: auto-configuration without a script is off", got)
	}
	discover := cfDict(t, map[string]any{"ProxyAutoDiscoveryEnable": 1})
	defer cfReleaseSys(discover)
	if got := parseProxies(discover); !got.AutoDiscover || got.AutoConfig {
		t.Fatalf("parsed %+v, want auto-discovery only", got)
	}
}

// A CFBoolean in the same slot is honored too, because CFNetwork itself
// accepts one, but nothing else is guessed at.
func TestParseProxiesFlagTypes(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatalf("system setup: %v", err)
	}
	boolDict := cfDict(t, map[string]any{
		"HTTPEnable": true, "HTTPProxy": "127.0.0.1", "HTTPPort": 7890,
	})
	defer cfReleaseSys(boolDict)
	if got := parseProxies(boolDict); got.HTTP != "127.0.0.1:7890" {
		t.Fatalf("CFBoolean enable parsed as %+v", got)
	}
	for _, bad := range []any{"1", []string{"1"}} {
		dict := cfDict(t, map[string]any{
			"HTTPEnable": bad, "HTTPProxy": "127.0.0.1", "HTTPPort": 7890,
		})
		if got := parseProxies(dict); got.HTTP != "" {
			t.Fatalf("%T enable parsed as %+v", bad, got)
		}
		cfReleaseSys(dict)
	}
}

// CFNetwork is the authority for the system policy: this test asks
// CFNetworkCopyProxiesForURL the same question the resolver answers and
// compares the answer. The dictionaries are built in this process, so no
// system setting is read or changed and no request is sent.
func TestMatchesCFNetwork(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatalf("system setup: %v", err)
	}
	if err := loadCFNetworkTest(); err != nil {
		t.Skipf("CFNetwork unavailable: %v", err)
	}
	cases := []struct {
		name string
		dict map[string]any
		urls []string
	}{
		{
			name: "HTTP only",
			dict: map[string]any{"HTTPEnable": 1, "HTTPProxy": "127.0.0.1", "HTTPPort": 7890},
			urls: []string{"http://example.test/p", "https://example.test/p", "http://printer/p",
				"http://127.0.0.1:8080/p", "http://localhost:8080/p"},
		},
		{
			name: "HTTPS only",
			dict: map[string]any{"HTTPSEnable": 1, "HTTPSProxy": "10.0.0.9", "HTTPSPort": 3129},
			urls: []string{"http://example.test/p", "https://example.test/p"},
		},
		{
			name: "SOCKS only",
			dict: map[string]any{"SOCKSEnable": 1, "SOCKSProxy": "127.0.0.1", "SOCKSPort": 7892},
			urls: []string{"http://example.test/p", "https://example.test/p"},
		},
		{
			name: "HTTP and SOCKS",
			dict: map[string]any{
				"HTTPEnable": 1, "HTTPProxy": "10.0.0.9", "HTTPPort": 3128,
				"SOCKSEnable": 1, "SOCKSProxy": "127.0.0.1", "SOCKSPort": 7892,
			},
			urls: []string{"http://example.test/p", "https://example.test/p"},
		},
		{
			name: "HTTPS and SOCKS",
			dict: map[string]any{
				"HTTPSEnable": 1, "HTTPSProxy": "10.0.0.9", "HTTPSPort": 3129,
				"SOCKSEnable": 1, "SOCKSProxy": "127.0.0.1", "SOCKSPort": 7892,
			},
			urls: []string{"http://example.test/p", "https://example.test/p"},
		},
		{
			name: "exceptions",
			dict: map[string]any{
				"HTTPEnable": 1, "HTTPProxy": "127.0.0.1", "HTTPPort": 7890,
				"ExceptionsList": []string{"*.example.test", "intranet.test"},
			},
			urls: []string{"http://api.example.test/p", "http://intranet.test/p", "http://example.test/p"},
		},
		{
			name: "simple hostnames excluded",
			dict: map[string]any{
				"HTTPEnable": 1, "HTTPProxy": "127.0.0.1", "HTTPPort": 7890,
				"ExcludeSimpleHostnames": 1,
			},
			urls: []string{"http://printer/p", "http://has.dot.test/p"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dict := cfDict(t, tc.dict)
			defer cfReleaseSys(dict)
			s := parseProxies(dict)
			for _, raw := range tc.urls {
				typ, host, port := cfNetworkProxy(t, dict, raw)
				u, err := url.Parse(raw)
				if err != nil {
					t.Fatalf("parse %q: %v", raw, err)
				}
				ours, err := s.proxyFor(u)
				if err != nil {
					t.Fatalf("%s: %v", raw, err)
				}
				switch {
				case ours == nil:
					if typ != "kCFProxyTypeNone" {
						t.Fatalf("%s: CFNetwork says %s %s:%d, we connect direct", raw, typ, host, port)
					}
				default:
					wantType := proxyTypeOf(s, ours)
					if typ != wantType || host != ours.Hostname() || port != portOf(t, ours) {
						t.Fatalf("%s: CFNetwork says %s %s:%d, we chose %s %s:%d",
							raw, typ, host, port, wantType, ours.Hostname(), portOf(t, ours))
					}
				}
			}
		})
	}
}

// proxyTypeOf names the CFNetwork proxy type for the entry the resolver chose.
func proxyTypeOf(s System, u *url.URL) string {
	switch {
	case s.HTTPS != "" && u.Host == s.HTTPS:
		return "kCFProxyTypeHTTPS"
	case s.HTTP != "" && u.Host == s.HTTP:
		return "kCFProxyTypeHTTP"
	case s.SOCKS != "" && u.Host == s.SOCKS:
		return "kCFProxyTypeSOCKS"
	}
	return "unknown"
}

func portOf(t *testing.T, u *url.URL) int {
	t.Helper()
	p := u.Port()
	if p == "" {
		return 0
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("port %q: %v", p, err)
	}
	return n
}

// CFNetwork's own proxy answer for one URL and dictionary: the first entry's
// type, host and port (0 for kCFProxyTypeNone).
var (
	cfNetOnce      sync.Once
	cfNetErr       error
	cfNetCopy      func(url, proxies uintptr) uintptr
	cfURLCreateStr func(alloc, str, base uintptr) uintptr
	cfProxyTypeKey uintptr
	cfProxyHostKey uintptr
	cfProxyPortKey uintptr
)

func loadCFNetworkTest() error {
	cfNetOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/CFNetwork.framework/CFNetwork",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cfNetErr = err
			return
		}
		cfLib, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cfNetErr = err
			return
		}
		purego.RegisterLibFunc(&cfNetCopy, lib, "CFNetworkCopyProxiesForURL")
		purego.RegisterLibFunc(&cfURLCreateStr, cfLib, "CFURLCreateWithString")
		var copySymbol func(dst *uintptr, src, size uintptr) uintptr
		purego.RegisterLibFunc(&copySymbol, purego.RTLD_DEFAULT, "memcpy")
		for name, dst := range map[string]*uintptr{
			"kCFProxyTypeKey": &cfProxyTypeKey, "kCFProxyHostNameKey": &cfProxyHostKey,
			"kCFProxyPortNumberKey": &cfProxyPortKey,
		} {
			addr, err := purego.Dlsym(lib, name)
			if err != nil {
				cfNetErr = err
				return
			}
			copySymbol(dst, addr, unsafe.Sizeof(*dst)) // the symbol holds the ref
		}
	})
	return cfNetErr
}

// cfNetworkProxy asks CFNetwork for one URL's proxy.
func cfNetworkProxy(t *testing.T, dict uintptr, rawURL string) (typ, host string, port int) {
	t.Helper()
	url := cfURLCreateStr(0, cfString(rawURL), 0)
	if url == 0 {
		t.Fatalf("CFURL for %q", rawURL)
	}
	defer cfReleaseSys(url)
	arr := cfNetCopy(url, dict)
	if arr == 0 || cfArrayGetCount(arr) < 1 {
		return "kCFProxyTypeNone", "", 0
	}
	defer cfReleaseSys(arr)
	first := cfArrayGetValue(arr, 0)
	typ = valueString(cfDictGetValue(first, cfProxyTypeKey))
	host = valueString(cfDictGetValue(first, cfProxyHostKey))
	port = valueInt(cfDictGetValue(first, cfProxyPortKey))
	if typ == "" {
		typ = "kCFProxyTypeNone"
	}
	return typ, host, port
}
