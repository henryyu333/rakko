//go:build darwin

package mac

// Who is on the other end of the approval socket. The hook client asks
// three questions before it accepts a decision: is the peer's executable
// the same file as mine, is it the same inode (so the real app was not
// moved aside and replaced at the same path), and, when both sides carry a
// code signature, is the cdhash the same. The server only accepts sockets
// from its own uid.

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// localPeerPID is Darwin's LOCAL_PEERPID, with SOL_LOCAL as its level.
const localPeerPID = 0x2

// PeerUID is the uid of the process on the other end of conn.
func PeerUID(conn net.Conn) (uint32, error) {
	fd, err := connFD(conn)
	if err != nil {
		return 0, err
	}
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}

// PeerPID is the pid of the process on the other end of conn.
func PeerPID(conn net.Conn) (int, error) {
	fd, err := connFD(conn)
	if err != nil {
		return 0, err
	}
	pid, err := syscall.GetsockoptInt(fd, unix.SOL_LOCAL, localPeerPID)
	if err != nil {
		return 0, err
	}
	if pid <= 0 {
		return 0, errors.New("mac: peer has no pid")
	}
	return pid, nil
}

func connFD(conn net.Conn) (int, error) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0, errors.New("mac: not a unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var fd int
	if err := raw.Control(func(got uintptr) { fd = int(got) }); err != nil {
		return 0, err
	}
	return fd, nil
}

// procPath is libproc's proc_pidpath.
var procPath struct {
	once sync.Once
	fn   func(pid int32, buf unsafe.Pointer, size uint32) int32
	err  error
}

const procPathMax = 4096

// PeerExePath is the path of pid's executable, through libproc.
func PeerExePath(pid int) (string, error) {
	procPath.once.Do(func() {
		lib, err := purego.Dlopen("/usr/lib/libproc.dylib", purego.RTLD_LAZY)
		if err != nil {
			procPath.err = err
			return
		}
		purego.RegisterLibFunc(&procPath.fn, lib, "proc_pidpath")
	})
	if procPath.err != nil {
		return "", procPath.err
	}
	buf := make([]byte, procPathMax)
	n := procPath.fn(int32(pid), unsafe.Pointer(&buf[0]), uint32(len(buf)))
	if n <= 0 {
		return "", fmt.Errorf("mac: proc_pidpath(%d) failed", pid)
	}
	if int(n) > len(buf) {
		n = int32(len(buf))
	}
	return string(buf[:n]), nil
}

// PeerIsSelf reports whether the process on the other end of conn is this
// same Rakko binary: same path and inode, then the code signature. If this
// binary carries a cdhash the peer must carry the same one; only an unsigned
// build (no cdhash of its own) falls back to path and inode alone.
func PeerIsSelf(conn net.Conn) error {
	pid, err := PeerPID(conn)
	if err != nil {
		return err
	}
	peer, err := PeerExePath(pid)
	if err != nil {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	selfReal, err := filepath.EvalSymlinks(self)
	if err != nil {
		return err
	}
	peerReal, err := filepath.EvalSymlinks(peer)
	if err != nil {
		return err
	}
	if selfReal != peerReal {
		return fmt.Errorf("mac: peer runs %s, not %s", peerReal, selfReal)
	}
	selfInfo, err := os.Stat(selfReal)
	if err != nil {
		return err
	}
	peerInfo, err := os.Stat(peerReal)
	if err != nil {
		return err
	}
	si, sok := selfInfo.Sys().(*syscall.Stat_t)
	pi, pok := peerInfo.Sys().(*syscall.Stat_t)
	if !sok || !pok || si.Dev != pi.Dev || si.Ino != pi.Ino {
		return errors.New("mac: peer executable is not the same file")
	}
	selfHash, selfErr := codeHashSelf()
	var peerHash []byte
	var peerErr error
	if selfErr == nil {
		peerHash, peerErr = codeHashPeer(pid)
	}
	return signatureDecision(selfHash, selfErr, peerHash, peerErr)
}

// codeHashSelf and codeHashPeer are seams for the signature decision tests;
// production reads the real framework.
var (
	codeHashSelf = func() ([]byte, error) { return codeHash(0) }
	codeHashPeer = func(pid int) ([]byte, error) { return codeHash(pid) }
)

// signatureDecision is the rule PeerIsSelf applies after path and inode
// match. An unsigned development build has no cdhash of its own and may
// rely on path and inode; once this binary has a hash, the peer must have
// the same one. Anything less is refused.
func signatureDecision(self []byte, selfErr error, peer []byte, peerErr error) error {
	if selfErr != nil {
		debugf("own signature unavailable, using path and inode: %v", selfErr)
		return nil
	}
	if len(self) == 0 {
		return errors.New("mac: own code signature is empty")
	}
	if peerErr != nil {
		return fmt.Errorf("mac: peer code signature unavailable: %w", peerErr)
	}
	if len(peer) == 0 {
		return errors.New("mac: peer code signature is empty")
	}
	if !bytes.Equal(self, peer) {
		return errors.New("mac: peer binary has a different code signature")
	}
	return nil
}

// pointerAnchor gives unsafePointerAt a real address to do arithmetic
// from, so the conversion stays in the form that is safe in Go's rules.
var pointerAnchor byte

// unsafePointerAt names the address dlsym returned as a Go pointer. The
// address is a live symbol in a loaded framework, so it does not move.
func unsafePointerAt(addr uintptr) unsafe.Pointer {
	base := unsafe.Pointer(&pointerAnchor)
	return unsafe.Add(base, addr-uintptr(base))
}

func debugf(format string, args ...any) {
	if os.Getenv("RAKKO_AGENT_DEBUG") == "1" {
		fmt.Fprintf(os.Stderr, "rakko: "+format+"\n", args...)
	}
}

// --- code signature ---

var (
	secOnce sync.Once
	secErr  error

	secCodeCopySelf                func(uint32, *uintptr) int32
	secCodeCopyGuestWithAttributes func(uintptr, uintptr, uint32, *uintptr) int32
	secCodeCopySigningInformation  func(uintptr, uint32, *uintptr) int32
	peerCFNumberCreate             func(uintptr, int32, unsafe.Pointer) uintptr
	peerCFDictionaryCreate         func(uintptr, *uintptr, *uintptr, int64, uintptr, uintptr) uintptr
	peerCFDictionaryGetValue       func(uintptr, uintptr) uintptr
	peerCFDataGetBytePtr           func(uintptr) unsafe.Pointer
	peerCFDataGetLength            func(uintptr) int64
	peerCFRelease                  func(uintptr)

	kSecGuestAttributePid uintptr
	kSecCodeInfoUnique    uintptr
	peerKeyCallbacks,
	peerValueCallbacks uintptr
)

const (
	kCFNumberSInt32Type      = 3
	kSecCSSigningInformation = 2
)

func loadSecurity() {
	secErr = nil
	lib, err := purego.Dlopen("/System/Library/Frameworks/Security.framework/Security",
		purego.RTLD_LAZY)
	if err != nil {
		secErr = err
		return
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
		purego.RTLD_LAZY)
	if err != nil {
		secErr = err
		return
	}
	purego.RegisterLibFunc(&secCodeCopySelf, lib, "SecCodeCopySelf")
	purego.RegisterLibFunc(&secCodeCopyGuestWithAttributes, lib, "SecCodeCopyGuestWithAttributes")
	purego.RegisterLibFunc(&secCodeCopySigningInformation, lib, "SecCodeCopySigningInformation")
	purego.RegisterLibFunc(&peerCFNumberCreate, cf, "CFNumberCreate")
	purego.RegisterLibFunc(&peerCFDictionaryCreate, cf, "CFDictionaryCreate")
	purego.RegisterLibFunc(&peerCFDictionaryGetValue, cf, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&peerCFDataGetBytePtr, cf, "CFDataGetBytePtr")
	purego.RegisterLibFunc(&peerCFDataGetLength, cf, "CFDataGetLength")
	purego.RegisterLibFunc(&peerCFRelease, cf, "CFRelease")
	var symErr error
	global := func(lib uintptr, name string) uintptr {
		addr, err := purego.Dlsym(lib, name)
		if err != nil {
			symErr = err
			return 0
		}
		return *(*uintptr)(unsafePointerAt(addr))
	}
	kSecGuestAttributePid = global(lib, "kSecGuestAttributePid")
	kSecCodeInfoUnique = global(lib, "kSecCodeInfoUnique")
	// CFDictionaryCreate wants the address of each callback struct, which
	// is what Dlsym returns for these exported globals; dereferencing them
	// would pass the first field instead of the struct.
	addr, err := purego.Dlsym(cf, "kCFTypeDictionaryKeyCallBacks")
	if err != nil {
		symErr = err
	} else {
		peerKeyCallbacks = addr
	}
	addr, err = purego.Dlsym(cf, "kCFTypeDictionaryValueCallBacks")
	if err != nil {
		symErr = err
	} else {
		peerValueCallbacks = addr
	}
	if symErr != nil {
		secErr = symErr
	}
}

// codeHash returns the cdhash of pid's code, or this process's when pid is
// zero. A binary without a signature has no unique hash.
func codeHash(pid int) ([]byte, error) {
	secOnce.Do(loadSecurity)
	if secErr != nil {
		return nil, secErr
	}
	var code uintptr
	if pid > 0 {
		p := int32(pid)
		num := peerCFNumberCreate(0, kCFNumberSInt32Type, unsafe.Pointer(&p))
		if num == 0 {
			return nil, errors.New("mac: CFNumberCreate failed")
		}
		defer peerCFRelease(num)
		key := kSecGuestAttributePid
		val := num
		attrs := peerCFDictionaryCreate(0, &key, &val, 1, peerKeyCallbacks, peerValueCallbacks)
		if attrs == 0 {
			return nil, errors.New("mac: CFDictionaryCreate failed")
		}
		defer peerCFRelease(attrs)
		if st := secCodeCopyGuestWithAttributes(0, attrs, 0, &code); st != 0 {
			return nil, fmt.Errorf("mac: SecCodeCopyGuestWithAttributes returned %d", st)
		}
	} else {
		if st := secCodeCopySelf(0, &code); st != 0 {
			return nil, fmt.Errorf("mac: SecCodeCopySelf returned %d", st)
		}
	}
	if code == 0 {
		return nil, errors.New("mac: no code object")
	}
	defer peerCFRelease(code)
	var info uintptr
	if st := secCodeCopySigningInformation(code, kSecCSSigningInformation, &info); st != 0 {
		return nil, fmt.Errorf("mac: SecCodeCopySigningInformation returned %d", st)
	}
	if info == 0 {
		return nil, errors.New("mac: no signing information")
	}
	defer peerCFRelease(info)
	data := peerCFDictionaryGetValue(info, kSecCodeInfoUnique)
	if data == 0 {
		return nil, errors.New("mac: no cdhash")
	}
	n := peerCFDataGetLength(data)
	ptr := peerCFDataGetBytePtr(data)
	if n <= 0 || n > 64 || ptr == nil {
		return nil, errors.New("mac: malformed cdhash")
	}
	out := make([]byte, n)
	copy(out, unsafe.Slice((*byte)(ptr), int(n)))
	return out, nil
}
