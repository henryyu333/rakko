//go:build !darwin

package agentnotify

import (
	"errors"
	"net"
)

// peerUID is unavailable away from Darwin, where Rakko does not run.
func peerUID(net.Conn) (uint32, error) { return 0, errors.New("agentnotify: no peer credentials") }

func peerIsSelf(net.Conn) bool { return false }
