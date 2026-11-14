//go:build !darwin && !windows

package agentnotify

import "net"

// peerIsSelf is unavailable away from the platforms Rakko runs on; an
// approval client there always falls back to its tool's own flow.
func peerIsSelf(net.Conn) bool { return false }
