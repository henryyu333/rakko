//go:build windows

package agentnotify

// The Windows transport: one named pipe in the machine-wide pipe namespace,
// named after this user, with a DACL that admits only this account. The pipe
// rejects remote clients and its first instance is created with
// FILE_FLAG_FIRST_PIPE_INSTANCE, so another account's process that grabbed
// the name first makes the feature unavailable instead of being trusted.
//
// Access is decided by the token handshake in auth_windows.go, not by the
// connection's process id or image path; those are diagnostics only.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"time"

	"golang.org/x/sys/windows"
)

// pipePrefix namespaces Rakko's pipes. The pipe namespace is shared by every
// account on the machine, so the name carries a hash of the user's SID.
const pipePrefix = `\\.\pipe\rakko-agent-`

// pipeName is the name of this user's pipe. Tests replace it before they
// start a server.
var pipeName = defaultPipeName

func defaultPipeName() (string, error) {
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(sid))
	return pipePrefix + hex.EncodeToString(sum[:8]), nil
}

// pipeSDDL admits this user alone. The mandatory label keeps a low-integrity
// process from adding an instance (Windows' default no-write-up already does;
// the label states it).
func pipeSDDL(sid string) string {
	return "D:P(A;;GA;;;" + sid + ")S:(ML;;NW;;;ME)"
}

// listenRakkoPipe creates this user's pipe. A SACL needs a privilege this
// process may not hold, in which case the DACL alone is used: a new object
// gets the creator's integrity label by default, which denies the same
// write-up.
func listenRakkoPipe(name, sid string) (net.Listener, error) {
	full, err := windows.SecurityDescriptorFromString(pipeSDDL(sid))
	if err != nil {
		return nil, err
	}
	ln, err := listenPipe(name, full)
	if err == nil || !errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) {
		return ln, err
	}
	dacl, derr := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + sid + ")")
	if derr != nil {
		return nil, err
	}
	return listenPipe(name, dacl)
}

// openTransport writes this run's token and starts listening.
func openTransport(dataDir string) (*transport, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	token, err := writeToken(dataDir)
	if err != nil {
		return nil, err
	}
	name, err := pipeName()
	if err != nil {
		return nil, err
	}
	ln, err := listenRakkoPipe(name, sid)
	if err != nil {
		return nil, err
	}
	return &transport{
		ln: ln,
		authorize: func(conn net.Conn, deadline time.Time) bool {
			return serverHandshake(conn, token, deadline)
		},
	}, nil
}

// dialTransport opens this user's pipe and proves who is on the other end
// before the caller writes a single byte of payload.
func dialTransport(dataDir string, timeout time.Duration) (net.Conn, error) {
	token, err := readToken(dataDir)
	if err != nil {
		return nil, err
	}
	name, err := pipeName()
	if err != nil {
		return nil, err
	}
	conn, err := dialPipe(name, timeout)
	if err != nil {
		return nil, err
	}
	if err := clientHandshake(conn, token, timeout); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}
