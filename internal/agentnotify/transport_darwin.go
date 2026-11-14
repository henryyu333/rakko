//go:build darwin

package agentnotify

// The macOS transport: a unix socket inside the data directory, mode 0600,
// in a directory Rakko creates 0700. Only this user can open either, so the
// server admits a connection by its peer's uid and the approval client
// insists the process on the other end is this same Rakko.

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"time"
)

// openTransport creates the listening socket for this data directory.
func openTransport(dataDir string) (*transport, error) {
	path := filepath.Join(dataDir, SocketName)
	// A leftover socket from a crashed run would block Listen.
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("agentnotify: " + path + " exists and is not a socket")
		}
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return &transport{
		ln: ln,
		authorize: func(conn net.Conn) bool {
			uid, err := peerUID(conn)
			return err == nil && uid == uint32(os.Getuid())
		},
		cleanup: func() { _ = os.Remove(path) },
	}, nil
}

// dialTransport connects to the app that owns dataDir.
func dialTransport(dataDir string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", filepath.Join(dataDir, SocketName), timeout)
}
