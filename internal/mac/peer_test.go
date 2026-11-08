//go:build darwin

package mac

// The approval client trusts a decision only from this same binary: the
// same executable path, the same inode, and, when both sides carry one, the
// same code signature. Two ends of one socket pair in this test process are
// exactly that, so the check must pass and the uid/pid helpers must name
// this process.

import (
	"bytes"
	"errors"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func socketPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	f0 := os.NewFile(uintptr(fds[0]), "peer-a")
	f1 := os.NewFile(uintptr(fds[1]), "peer-b")
	c0, err := net.FileConn(f0)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := net.FileConn(f1)
	if err != nil {
		t.Fatal(err)
	}
	f0.Close()
	f1.Close()
	t.Cleanup(func() { c0.Close(); c1.Close() })
	return c0, c1
}

func TestPeerIsSelfOnSocketPair(t *testing.T) {
	a, b := socketPair(t)
	if err := PeerIsSelf(a); err != nil {
		t.Fatalf("the same process was refused: %v", err)
	}
	if err := PeerIsSelf(b); err != nil {
		t.Fatalf("the other end of the same process was refused: %v", err)
	}
}

func TestPeerUIDAndPID(t *testing.T) {
	a, _ := socketPair(t)
	uid, err := PeerUID(a)
	if err != nil {
		t.Fatal(err)
	}
	if uid != uint32(os.Getuid()) {
		t.Fatalf("uid %d, want %d", uid, os.Getuid())
	}
	pid, err := PeerPID(a)
	if err != nil {
		t.Fatal(err)
	}
	if pid != os.Getpid() {
		t.Fatalf("pid %d, want %d", pid, os.Getpid())
	}
	if path, err := PeerExePath(pid); err != nil || path == "" {
		t.Fatalf("PeerExePath(%d) = %q, %v", pid, path, err)
	}
}

// The code-signature path must work in the test binary too: the arm64
// linker ad-hoc signs it, so both ends of a socket pair carry a cdhash and
// the security framework calls must succeed.
func TestCodeHashSelf(t *testing.T) {
	loadSecurity()
	if secErr != nil {
		t.Fatalf("loadSecurity: %v", secErr)
	}
	self, err := codeHash(0)
	if err != nil {
		t.Fatalf("codeHash(self): %v", err)
	}
	if len(self) == 0 {
		t.Fatal("codeHash(self) returned an empty hash")
	}
	peer, err := codeHash(os.Getpid())
	if err != nil {
		t.Fatalf("codeHash(pid): %v", err)
	}
	if len(peer) == 0 {
		t.Fatal("codeHash(pid) returned an empty hash")
	}
	if !bytes.Equal(self, peer) {
		t.Fatalf("self %x != peer %x", self, peer)
	}
}

func TestSignatureDecision(t *testing.T) {
	errHash := errors.New("no hash")
	cases := []struct {
		name    string
		self    []byte
		selfErr error
		peer    []byte
		peerErr error
		wantErr bool
	}{
		{"same hash", []byte{1, 2, 3}, nil, []byte{1, 2, 3}, nil, false},
		{"different hash", []byte{1, 2, 3}, nil, []byte{1, 2, 4}, nil, true},
		{"self signed, peer unsigned", []byte{1, 2, 3}, nil, nil, errHash, true},
		{"self signed, peer empty", []byte{1, 2, 3}, nil, nil, nil, true},
		{"self empty", nil, nil, []byte{1, 2, 3}, nil, true},
		{"unsigned self falls back", nil, errHash, nil, errHash, false},
		{"unsigned self with peer hash", nil, errHash, []byte{1, 2, 3}, nil, false},
	}
	for _, tc := range cases {
		err := signatureDecision(tc.self, tc.selfErr, tc.peer, tc.peerErr)
		if (err != nil) != tc.wantErr {
			t.Errorf("%s: signatureDecision = %v, want error %v", tc.name, err, tc.wantErr)
		}
	}
}

// A peer that cannot produce a hash must be refused once this binary has
// one; the path and inode checks pass for a socket pair in this process.
func TestPeerIsSelfRejectsUnsignedPeer(t *testing.T) {
	oldSelf, oldPeer := codeHashSelf, codeHashPeer
	codeHashSelf = func() ([]byte, error) { return []byte{1, 2, 3}, nil }
	codeHashPeer = func(int) ([]byte, error) { return nil, errors.New("no hash") }
	t.Cleanup(func() { codeHashSelf, codeHashPeer = oldSelf, oldPeer })

	a, _ := socketPair(t)
	if err := PeerIsSelf(a); err == nil {
		t.Fatal("an unsigned peer was accepted while self is signed")
	}
}

// A development build without its own cdhash keeps the path+inode rule.
func TestPeerIsSelfFallsBackWhenSelfUnsigned(t *testing.T) {
	oldSelf, oldPeer := codeHashSelf, codeHashPeer
	codeHashSelf = func() ([]byte, error) { return nil, errors.New("no hash") }
	codeHashPeer = func(int) ([]byte, error) { return nil, errors.New("no hash") }
	t.Cleanup(func() { codeHashSelf, codeHashPeer = oldSelf, oldPeer })

	a, _ := socketPair(t)
	if err := PeerIsSelf(a); err != nil {
		t.Fatalf("an unsigned build refused its own peer: %v", err)
	}
}
