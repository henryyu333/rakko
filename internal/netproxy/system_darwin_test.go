//go:build darwin

package netproxy

// The platform read: the shape of what it reports on this machine, and the
// parsing of a proxy dictionary driven with the same CoreFoundation objects
// SystemConfiguration hands over. The latter needs no system change (it never
// touches the machine's settings): it builds a dictionary in this process and
// parses it through the production path.

import (
	"net/url"
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

// A dictionary like the one System Settings writes, parsed by the production
// code path.
func TestParseProxies(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatalf("system setup: %v", err)
	}
	dict := cfDict(t, map[string]any{
		"HTTPEnable":     true,
		"HTTPProxy":      "127.0.0.1",
		"HTTPPort":       7890,
		"HTTPSEnable":    true,
		"HTTPSProxy":     "127.0.0.1",
		"HTTPSPort":      7891,
		"SOCKSEnable":    true,
		"SOCKSProxy":     "127.0.0.1",
		"SOCKSPort":      7892,
		"ExceptionsList": []string{"*.local", "169.254/16", " intranet.example.com "},
	})
	defer cfReleaseSys(dict)
	got := parseProxies(dict)
	want := System{
		HTTP: "127.0.0.1:7890", HTTPS: "127.0.0.1:7891", SOCKS: "127.0.0.1:7892",
		Bypass: "*.local,169.254/16,intranet.example.com",
	}
	if got != want {
		t.Fatalf("parsed %+v, want %+v", got, want)
	}
}

// A switched-off proxy is off no matter what host it names; a wrong-typed or
// incomplete entry is off too, never "host:0".
func TestParseProxiesIgnoresIncompleteEntries(t *testing.T) {
	if err := loadSystem(); err != nil {
		t.Fatalf("system setup: %v", err)
	}
	dict := cfDict(t, map[string]any{
		"HTTPEnable":  false,
		"HTTPProxy":   "127.0.0.1",
		"HTTPPort":    7890,
		"HTTPSEnable": true,
		"HTTPSProxy":  "127.0.0.1",
		"HTTPSPort":   0,
		"SOCKSEnable": "yes", // a string where a boolean belongs
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
		"ProxyAutoConfigEnable":    true,
		"ProxyAutoConfigURLString": "http://127.0.0.1:1/proxy.pac",
	})
	defer cfReleaseSys(on)
	if got := parseProxies(on); !got.AutoConfig || got.AutoDiscover {
		t.Fatalf("parsed %+v, want auto-configuration only", got)
	}
	noScript := cfDict(t, map[string]any{"ProxyAutoConfigEnable": true})
	defer cfReleaseSys(noScript)
	if got := parseProxies(noScript); got.AutoConfig {
		t.Fatalf("parsed %+v: auto-configuration without a script is off", got)
	}
	discover := cfDict(t, map[string]any{"ProxyAutoDiscoveryEnable": true})
	defer cfReleaseSys(discover)
	if got := parseProxies(discover); !got.AutoDiscover || got.AutoConfig {
		t.Fatalf("parsed %+v, want auto-discovery only", got)
	}
}
