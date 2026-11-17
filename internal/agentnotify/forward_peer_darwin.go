//go:build darwin

package agentnotify

import "net"

// forwardPeerIsRakko reports whether the listener on the other end of a
// completion connection is this same Rakko. The event carries the prompt
// text, so the forwarder asks what the approval client asks: the same
// executable path and inode, then the same code signature. A socket left
// behind by a crashed app, or one another process took first, fails the
// check and gets nothing.
func forwardPeerIsRakko(conn net.Conn) bool { return peerIsSelf(conn) }
