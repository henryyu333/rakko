//go:build !darwin

package agentnotify

import "net"

// forwardPeerIsRakko is the macOS check, which needs a unix peer. Rakko's
// Windows transport authenticates both directions with a token before the
// first byte (see token_windows.go), so a connection that got this far is
// already Rakko's; the remaining platforms run no Rakko.
func forwardPeerIsRakko(net.Conn) bool { return true }
