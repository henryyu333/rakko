//go:build darwin

package agentnotify

import (
	"net"

	"rakko/internal/mac"
)

// peerUID is the uid of the process on the other end of an accepted unix
// connection. Only this user may talk to the approval socket.
func peerUID(conn net.Conn) (uint32, error) { return mac.PeerUID(conn) }

// peerIsSelf reports whether the process on the other end of an approval
// connection is this same Rakko binary.
func peerIsSelf(conn net.Conn) bool { return mac.PeerIsSelf(conn) == nil }
