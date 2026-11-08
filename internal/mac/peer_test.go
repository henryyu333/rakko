//go:build darwin

package mac

// The approval client trusts a decision only from this same binary: the
// same executable path, the same inode, and, when both sides carry one, the
// same code signature. Two ends of one socket pair in this test process are
// exactly that, so the check must pass and the uid/pid helpers must name
// this process.

import (
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
