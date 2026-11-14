//go:build !darwin && !windows

package agentnotify

// Rakko runs on macOS and Windows. Elsewhere the socket has no transport:
// the app never starts and every hook client falls back to its tool's own
// flow.

import (
	"errors"
	"net"
	"time"
)

var errNoTransport = errors.New("agentnotify: no socket transport on this platform")

func openTransport(string) (*transport, error) { return nil, errNoTransport }

func dialTransport(string, time.Duration) (net.Conn, error) { return nil, errNoTransport }
