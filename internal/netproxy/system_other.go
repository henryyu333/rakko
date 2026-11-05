//go:build !darwin

package netproxy

// readSystemProxies reports no system proxy settings: only macOS has the
// per-network proxy configuration this package reads. The environment's
// rules still apply on every platform.
func readSystemProxies() (System, error) { return System{}, nil }
