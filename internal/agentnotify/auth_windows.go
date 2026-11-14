//go:build windows

package agentnotify

// The mutual challenge that runs before either side sends a payload. Each
// side sends 32 random bytes and answers the other's with
// HMAC-SHA256(token, direction || peer nonce || own nonce), compared in
// constant time. Neither the client's process id nor its image path takes
// part: a pid can be reused and a handle can move, but a token only this
// user's normal-integrity processes can read cannot be guessed.
//
// Admission happens in both directions. The server refuses a client that
// cannot answer, so no event or approval request reaches the app; the client
// refuses a server that cannot answer, so a process that grabbed the pipe
// name first never receives a prompt or a tool_input byte and never gets to
// answer with a forged decision.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"io"
	"net"
	"time"
)

const (
	// authNonceSize is each side's random challenge.
	authNonceSize = 32
	// authTagSize is the HMAC's length.
	authTagSize = 32
	// authClientTag and authServerTag separate the two directions, so a
	// message reflected back at its sender cannot verify.
	authClientTag = "rakko-agent/1 client"
	authServerTag = "rakko-agent/1 server"
	// authTimeout bounds the whole handshake. It is inside the server's
	// two-second framing budget and inside each client's dial budget.
	authTimeout = 2 * time.Second
)

// authMAC is one side's answer: the token, the direction, the peer's nonce
// and its own, in that order.
func authMAC(token []byte, tag string, peer, own []byte) []byte {
	m := hmac.New(sha256.New, token)
	m.Write([]byte(tag))
	m.Write([]byte{0})
	m.Write(peer)
	m.Write(own)
	return m.Sum(nil)
}

// serverHandshake admits one client. It writes only its nonce until the
// client has proved it holds the token.
func serverHandshake(conn net.Conn, token []byte) bool {
	_ = conn.SetDeadline(time.Now().Add(authTimeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	nonce := make([]byte, authNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return false
	}
	if _, err := conn.Write(nonce); err != nil {
		return false
	}
	peer := make([]byte, authNonceSize)
	if _, err := io.ReadFull(conn, peer); err != nil {
		return false
	}
	answer := make([]byte, authTagSize)
	if _, err := io.ReadFull(conn, answer); err != nil {
		return false
	}
	want := authMAC(token, authClientTag, nonce, peer)
	if subtle.ConstantTimeCompare(answer, want) != 1 {
		return false
	}
	_, err := conn.Write(authMAC(token, authServerTag, peer, nonce))
	return err == nil
}

// clientHandshake proves this client to the app and refuses a server that
// cannot prove itself first. Only the nonce goes out before the app answers.
func clientHandshake(conn net.Conn, token []byte, timeout time.Duration) error {
	if timeout <= 0 || timeout > authTimeout {
		timeout = authTimeout
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	nonce := make([]byte, authNonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if _, err := conn.Write(nonce); err != nil {
		return err
	}
	peer := make([]byte, authNonceSize)
	if _, err := io.ReadFull(conn, peer); err != nil {
		return err
	}
	// The answer goes out before the app's arrives: the app verifies it
	// before it answers, and waiting first would deadlock both ends. An
	// HMAC proves nothing to a thief, and no payload follows until the
	// app's own answer has been checked.
	if _, err := conn.Write(authMAC(token, authClientTag, peer, nonce)); err != nil {
		return err
	}
	answer := make([]byte, authTagSize)
	if _, err := io.ReadFull(conn, answer); err != nil {
		return err
	}
	want := authMAC(token, authServerTag, nonce, peer)
	if subtle.ConstantTimeCompare(answer, want) != 1 {
		return errors.New("agentnotify: the process on the other end is not this Rakko")
	}
	return nil
}
