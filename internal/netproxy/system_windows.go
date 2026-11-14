//go:build windows

package netproxy

import (
	"strings"

	"golang.org/x/sys/windows/registry"
)

// internetSettings is the per-user Internet Settings key WinINET, the WinHTTP
// fallback and every browser that follows the system proxy read.
const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// readSystemProxies reads the current user's LAN proxy settings. A missing
// key or value means "not configured": Windows ships the key, so an error
// opening it is reported and the caller falls back to a direct connection.
func readSystemProxies() (System, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE)
	if err != nil {
		return System{}, err
	}
	defer k.Close()

	enabled, _, _ := k.GetIntegerValue("ProxyEnable")
	server, _, _ := k.GetStringValue("ProxyServer")
	override, _, _ := k.GetStringValue("ProxyOverride")
	autoConfig, _, _ := k.GetStringValue("AutoConfigURL")
	autoDetect, _, _ := k.GetIntegerValue("AutoDetect")
	return systemFromSettings(enabled != 0, server, override, autoConfig, autoDetect != 0), nil
}

// systemFromSettings maps the registry values onto the System model. It is
// pure so the value formats can be tested without touching a machine.
func systemFromSettings(enabled bool, server, override, autoConfigURL string, autoDetect bool) System {
	var s System
	if enabled {
		s.HTTP, s.HTTPS, s.SOCKS = parseProxyServer(server)
	}
	s.Bypass, s.ExcludeSimple = parseProxyOverride(override)
	s.AutoConfig = autoConfigURL != ""
	s.AutoDiscover = autoDetect
	return s
}

// parseProxyServer decodes ProxyServer. WinINET writes either one
// "host:port" for every protocol or a "scheme=host:port;…" list, which may
// also put one bare entry first to serve the rest. Unknown schemes are
// ignored, and a scheme Windows lists with an empty address is off.
func parseProxyServer(v string) (httpAddr, httpsAddr, socksAddr string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", "", ""
	}
	if !strings.Contains(v, "=") {
		return v, v, v
	}
	var fallback string
	for _, entry := range strings.Split(v, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		scheme, addr, ok := strings.Cut(entry, "=")
		if !ok {
			// A bare entry is the proxy for every protocol not named.
			fallback = entry
			continue
		}
		addr = strings.TrimSpace(addr)
		switch strings.ToLower(strings.TrimSpace(scheme)) {
		case "http":
			httpAddr = addr
		case "https":
			httpsAddr = addr
		case "socks", "socks5":
			socksAddr = addr
		}
	}
	if fallback != "" {
		if httpAddr == "" {
			httpAddr = fallback
		}
		if httpsAddr == "" {
			httpsAddr = fallback
		}
		if socksAddr == "" {
			socksAddr = fallback
		}
	}
	return httpAddr, httpsAddr, socksAddr
}

// parseProxyOverride decodes ProxyOverride, a semicolon-separated exception
// list. "<local>" is the one special entry: it names every host without a
// dot — ExcludeSimpleHostnames in the System model — and is not a bypass
// pattern itself.
func parseProxyOverride(v string) (bypass string, excludeSimple bool) {
	var rules []string
	for _, entry := range strings.Split(v, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.EqualFold(entry, "<local>") {
			excludeSimple = true
			continue
		}
		rules = append(rules, entry)
	}
	return strings.Join(rules, ","), excludeSimple
}

// systemName is how the platform's settings are named in the log.
const systemName = "Windows"
