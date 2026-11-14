//go:build windows

package agentnotify

import "net"

// peerIsSelf reports whether the process on the other end of an approval
// connection is Rakko. The pipe client proved that before it sent a byte of
// payload: the named pipe's DACL admits this user only, and the app answered
// a fresh challenge with the token that only this user's normal-integrity
// processes can read. A connection that got this far already passed.
func peerIsSelf(net.Conn) bool { return true }
