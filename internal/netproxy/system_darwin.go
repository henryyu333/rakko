//go:build darwin

package netproxy

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/ebitengine/purego"
)

// SystemConfiguration's proxy dictionary, read with the same purego
// convention as the rest of the app's macOS calls: no cgo, no subprocess.
const (
	cfUTF8        = 0x08000100 // kCFStringEncodingUTF8
	cfNumberInt   = 9          // kCFNumberIntType
	cfTypeString  = "CFString"
	cfTypeNumber  = "CFNumber"
	cfTypeBoolean = "CFBoolean"
	cfTypeArray   = "CFArray"
)

var (
	systemOnce    sync.Once
	systemLoadErr error

	cfStringCreate   func(alloc uintptr, str *byte, enc uint32) uintptr
	cfDictGetValue   func(dict, key uintptr) uintptr
	cfNumberGetValue func(num uintptr, typ int32, out *int32) bool
	cfStringGetCStr  func(str uintptr, buf *byte, size int64, enc uint32) bool
	cfArrayGetCount  func(arr uintptr) int64
	cfArrayGetValue  func(arr uintptr, idx int64) uintptr
	cfGetTypeID      func(cf uintptr) uintptr
	cfTypeID         map[string]func() uintptr
	cfBoolGetValue   func(b uintptr) bool
	cfReleaseSys     func(cf uintptr)
	scStoreCreate    func(alloc, name, callback, ctx uintptr) uintptr
	scCopyProxies    func(store uintptr) uintptr
)

// loadSystem resolves CoreFoundation and SystemConfiguration once.
func loadSystem() error {
	systemOnce.Do(func() {
		defer func() {
			if r := recover(); r != nil {
				systemLoadErr = fmt.Errorf("netproxy: system proxy setup: %v", r)
			}
		}()
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			systemLoadErr = err
			return
		}
		sc, err := purego.Dlopen("/System/Library/Frameworks/SystemConfiguration.framework/SystemConfiguration",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			systemLoadErr = err
			return
		}
		purego.RegisterLibFunc(&cfStringCreate, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfDictGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")
		purego.RegisterLibFunc(&cfStringGetCStr, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&cfArrayGetCount, cf, "CFArrayGetCount")
		purego.RegisterLibFunc(&cfArrayGetValue, cf, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&cfGetTypeID, cf, "CFGetTypeID")
		purego.RegisterLibFunc(&cfBoolGetValue, cf, "CFBooleanGetValue")
		purego.RegisterLibFunc(&cfReleaseSys, cf, "CFRelease")
		purego.RegisterLibFunc(&scStoreCreate, sc, "SCDynamicStoreCreate")
		purego.RegisterLibFunc(&scCopyProxies, sc, "SCDynamicStoreCopyProxies")
		cfTypeID = map[string]func() uintptr{}
		for name, key := range map[string]string{
			cfTypeString:  "CFStringGetTypeID",
			cfTypeNumber:  "CFNumberGetTypeID",
			cfTypeBoolean: "CFBooleanGetTypeID",
			cfTypeArray:   "CFArrayGetTypeID",
		} {
			var fn func() uintptr
			purego.RegisterLibFunc(&fn, cf, key)
			cfTypeID[name] = fn
		}
	})
	return systemLoadErr
}

// readSystemProxies reads System Settings ▸ Network ▸ Proxies through
// SystemConfiguration. It runs on whichever goroutine asks, at most once per
// systemTTL: it starts no process and opens no connection, so it cannot stall
// the UI.
func readSystemProxies() (System, error) {
	if err := loadSystem(); err != nil {
		return System{}, err
	}
	store := proxyStore()
	if store == 0 {
		return System{}, errors.New("netproxy: no dynamic store")
	}
	dict := scCopyProxies(store)
	if dict == 0 {
		return System{}, errors.New("netproxy: no proxy settings")
	}
	defer cfReleaseSys(dict)
	return parseProxies(dict), nil
}

// proxyStore is the process's one SystemConfiguration session, created on
// first use: opening it per read would talk to configd every systemTTL for no
// gain, since reads of it are thread-safe.
var (
	storeOnce sync.Once
	storeRef  uintptr
)

func proxyStore() uintptr {
	storeOnce.Do(func() {
		storeRef = scStoreCreate(0, cfString("local.y3.rakko.proxy"), 0, 0)
	})
	return storeRef
}

// parseProxies reads one SystemConfiguration proxy dictionary into System.
// Split out so a test can drive the parsing with the same CoreFoundation
// objects the platform hands over.
func parseProxies(dict uintptr) System {
	var s System
	s.HTTP = proxyAt(dict, "HTTPEnable", "HTTPProxy", "HTTPPort")
	s.HTTPS = proxyAt(dict, "HTTPSEnable", "HTTPSProxy", "HTTPSPort")
	s.SOCKS = proxyAt(dict, "SOCKSEnable", "SOCKSProxy", "SOCKSPort")
	s.Bypass = strings.Join(exceptionList(dict), ",")
	s.AutoConfig = dictBool(dict, "ProxyAutoConfigEnable") && dictString(dict, "ProxyAutoConfigURLString") != ""
	s.AutoDiscover = dictBool(dict, "ProxyAutoDiscoveryEnable")
	return s
}

// proxyAt is one enabled proxy as "host:port"; an entry that is switched off,
// has no host or no port uses no proxy.
func proxyAt(dict uintptr, enable, host, port string) string {
	if !dictBool(dict, enable) {
		return ""
	}
	h := dictString(dict, host)
	p := dictInt(dict, port)
	if h == "" || p <= 0 {
		return ""
	}
	return net.JoinHostPort(h, strconv.Itoa(p))
}

// exceptionList reads the proxy bypass entries as written.
func exceptionList(dict uintptr) []string {
	v := dictValue(dict, "ExceptionsList")
	if !isType(v, cfTypeArray) {
		return nil
	}
	var out []string
	for i := range cfArrayGetCount(v) {
		if e := strings.TrimSpace(valueString(cfArrayGetValue(v, i))); e != "" {
			out = append(out, e)
		}
	}
	return out
}

func dictValue(dict uintptr, key string) uintptr {
	k := cfString(key)
	if k == 0 {
		return 0
	}
	defer cfReleaseSys(k)
	return cfDictGetValue(dict, k)
}

func dictString(dict uintptr, key string) string { return valueString(dictValue(dict, key)) }

func dictInt(dict uintptr, key string) int { return valueInt(dictValue(dict, key)) }

func dictBool(dict uintptr, key string) bool {
	return valueBool(dictValue(dict, key))
}

func isType(v uintptr, name string) bool {
	fn := cfTypeID[name]
	return v != 0 && fn != nil && cfGetTypeID(v) == fn()
}

func valueString(v uintptr) string {
	if !isType(v, cfTypeString) {
		return ""
	}
	buf := make([]byte, 512)
	if !cfStringGetCStr(v, &buf[0], int64(len(buf)), cfUTF8) {
		return ""
	}
	if i := indexNUL(buf); i >= 0 {
		return string(buf[:i])
	}
	return string(buf)
}

// indexNUL returns where the C string ends, or -1.
func indexNUL(b []byte) int {
	for i := range b {
		if b[i] == 0 {
			return i
		}
	}
	return -1
}

func valueInt(v uintptr) int {
	if !isType(v, cfTypeNumber) {
		return 0
	}
	var n int32
	if !cfNumberGetValue(v, cfNumberInt, &n) {
		return 0
	}
	return int(n)
}

func valueBool(v uintptr) bool {
	if !isType(v, cfTypeBoolean) {
		return false
	}
	return cfBoolGetValue(v)
}

// cfString makes a CoreFoundation string from s; the caller releases it.
func cfString(s string) uintptr {
	buf := append([]byte(s), 0)
	return cfStringCreate(0, &buf[0], cfUTF8)
}
