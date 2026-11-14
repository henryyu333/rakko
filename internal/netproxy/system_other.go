//go:build !darwin && !windows

package netproxy

// readSystemProxies reports no system proxy settings: this package reads
// them only on macOS and Windows. The environment's rules still apply.
func readSystemProxies() (System, error) { return System{}, nil }

// systemName is how the platform's settings are named in the log.
const systemName = "system"
