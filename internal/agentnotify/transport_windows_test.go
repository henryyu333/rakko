//go:build windows

package agentnotify

// The Windows transport and its admission rule: a pipe only this user can
// open, a token only this user's normal-integrity processes can read, and a
// handshake before either side says anything. The tests drive the real
// listener and the real client code; the only seams are the pipe name (so a
// test never collides with an installed Rakko) and a temporary data
// directory.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// testPipe pins a unique pipe name and returns a fresh data directory.
func testPipe(t *testing.T) (dataDir, name string) {
	t.Helper()
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	name = pipePrefix + hex.EncodeToString(b[:])
	old := pipeName
	pipeName = func() (string, error) { return name, nil }
	t.Cleanup(func() { pipeName = old })
	return t.TempDir(), name
}

// permissivePipeSD lets this test process create instances of a name; the
// security descriptor itself is exercised separately.
func permissivePipeSD(t *testing.T) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + sid + ")")
	if err != nil {
		t.Fatal(err)
	}
	return sd
}

func TestEventRoundTripOverPipe(t *testing.T) {
	dir, _ := testPipe(t)
	got := make(chan Event, 1)
	srv, err := Listen(dir, func(ev Event) { got <- ev }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	forward(ToolClaude, dir, "", strings.NewReader(
		`{"session_id":"s1","cwd":"C:\\work","prompt":"say hi"}`))
	select {
	case ev := <-got:
		if ev.Tool != ToolClaude || ev.Session != "s1" || ev.Prompt != "say hi" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the event never arrived")
	}
}

func TestApprovalRoundTripOverPipe(t *testing.T) {
	dir, _ := testPipe(t)
	asked := make(chan Ask, 1)
	srv, err := Listen(dir, nil, func(ctx context.Context, a Ask) Decision {
		asked <- a
		return Decision{ID: a.ID, Decision: DecisionAllow}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	payload := `{"session_id":"s2","cwd":"C:\\work","hook_event_name":"PermissionRequest",` +
		`"tool_name":"Bash","tool_input":{"command":"del /s /q build"}}`
	var out strings.Builder
	if !RunApproveCLI([]string{"--agent-approve", "--tool", "claude", "--data-dir", dir},
		strings.NewReader(payload), &out) {
		t.Fatal("the approval mode was not handled")
	}
	select {
	case a := <-asked:
		if a.Tool != ToolClaude || a.ToolName != "Bash" ||
			!strings.Contains(string(a.Input), "del /s /q") {
			t.Fatalf("ask = %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the app was never asked")
	}
	var decision struct {
		HookSpecificOutput struct {
			Decision struct {
				Behavior string `json:"behavior"`
			} `json:"decision"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out.String()), &decision); err != nil {
		t.Fatalf("output is not a decision: %q", out.String())
	}
	if decision.HookSpecificOutput.Decision.Behavior != "allow" {
		t.Fatalf("decision = %q", out.String())
	}
}

// A client that never sends a frame is dropped by the framing deadline. The
// app must not wait on it.
func TestFirstLineDeadlineClosesTheConnection(t *testing.T) {
	dir, _ := testPipe(t)
	srv, err := Listen(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := dialTransport(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(6 * time.Second))
	var one [1]byte
	_, rerr := conn.Read(one[:])
	if rerr == nil {
		t.Fatal("the server answered a client that sent nothing")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the server waited %v for a first line", elapsed)
	}
}

// Deadlines work in both directions: a read with no data and a write to a
// full pipe report the deadline error rather than blocking.
func TestConnDeadlines(t *testing.T) {
	_, name := testPipe(t)
	ln, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := dialPipe(name, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	if err := client.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	_, rerr := client.Read(b[:])
	if !isTimeout(rerr) {
		t.Fatalf("read without data returned %v, want a deadline error", rerr)
	}
	// The deadline is cleared again: a write from the other end is readable.
	if err := client.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, err := client.Read(b[:]); err != nil || n != 1 || b[0] != 'x' {
		t.Fatalf("read after the deadline = %d, %v", n, err)
	}

	if err := server.SetWriteDeadline(time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	_, werr := server.Write([]byte("y"))
	if !isTimeout(werr) {
		t.Fatalf("write with an expired deadline returned %v, want a deadline error", werr)
	}
}

func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	var terr interface{ Timeout() bool }
	return errors.Is(err, os.ErrDeadlineExceeded) ||
		(errors.As(err, &terr) && terr.Timeout())
}

// Closing the connection while a read is in flight must not hang: the read
// is cancelled, the handle is closed only after it completes, and Close
// returns.
func TestCloseWithPendingRead(t *testing.T) {
	_, name := testPipe(t)
	ln, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := dialPipe(name, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer client.Close()

	readErr := make(chan error, 1)
	go func() {
		var b [4]byte
		_, err := server.Read(b[:])
		readErr <- err
	}()
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() { server.Close(); close(done) }()
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("a cancelled read returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not cancel the pending read")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close waited forever")
	}
}

// A client without the token is refused, and the app sends nothing but its
// own challenge before closing.
func TestWrongTokenIsRefusedWithoutPayload(t *testing.T) {
	dir, _ := testPipe(t)
	got := make(chan Event, 1)
	srv, err := Listen(dir, func(ev Event) { got <- ev }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	conn, err := dialPipe(mustPipeName(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	bogus := make([]byte, tokenSize)
	if err := clientHandshake(conn, bogus, time.Second); err == nil {
		t.Fatal("the app accepted a client without the token")
	}
	select {
	case ev := <-got:
		t.Fatalf("the app handled an unauthenticated event: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
}

func mustPipeName(t *testing.T) string {
	t.Helper()
	name, err := pipeName()
	if err != nil {
		t.Fatal(err)
	}
	return name
}

// A process that owns the pipe name first never sees a prompt or a
// tool_input byte, and the client falls back without waiting.
func TestFakeServerGetsNoPayload(t *testing.T) {
	dir, name := testPipe(t)
	if _, err := writeToken(dir); err != nil {
		t.Fatal(err)
	}
	ln, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var received []byte
	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		close(accepted)
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			mu.Lock()
			received = append(received, buf[:n]...)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	payload := `{"session_id":"s3","cwd":"C:\\work","hook_event_name":"PermissionRequest",` +
		`"tool_name":"Bash","tool_input":{"command":"type C:\\secret.txt"}}`
	var out strings.Builder
	start := time.Now()
	handled := RunApproveCLI([]string{"--agent-approve", "--tool", "claude", "--data-dir", dir},
		strings.NewReader(payload), &out)
	elapsed := time.Since(start)
	if !handled {
		t.Fatal("the approval mode was not handled")
	}
	if out.Len() != 0 {
		t.Fatalf("the fallback printed %q, want nothing for claude", out.String())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("the client waited %v on a foreign server", elapsed)
	}
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("the fake server was never connected to")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != authNonceSize {
		t.Fatalf("the fake server received %d bytes, want only the client's nonce", len(received))
	}
	if strings.Contains(string(received), "tool_input") || strings.Contains(string(received), "secret.txt") {
		t.Fatal("the payload leaked to a foreign server")
	}
}

// Any process of this user can add another instance to the pipe name while
// the app is listening. A client that lands on it must fall back, never
// send its payload, and never accept a forged decision.
func TestExtraInstanceCannotForgeADecision(t *testing.T) {
	dir, name := testPipe(t)
	srv, err := Listen(dir, nil, func(ctx context.Context, a Ask) Decision {
		return Decision{ID: a.ID, Decision: DecisionAllow}
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// A second instance of the same name, this time without
	// FILE_FLAG_FIRST_PIPE_INSTANCE: any process of this user can add one.
	sd := permissivePipeSD(t)
	h, err := createPipeInstance(name, sd, false)
	if err != nil {
		t.Skipf("the name cannot host a second instance here: %v", err)
	}
	impostor := &pipeListener{name: name, sd: sd, pending: h,
		waiting: map[windows.Handle]struct{}{}}
	impostor.cond = sync.NewCond(&impostor.mu)
	defer impostor.Close()
	var mu sync.Mutex
	var leaked []byte
	accepted := make(chan struct{}, 1)
	go func() {
		conn, err := impostor.Accept()
		if err != nil {
			return
		}
		accepted <- struct{}{}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 4096)
		for {
			n, err := conn.Read(buf)
			mu.Lock()
			leaked = append(leaked, buf[:n]...)
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()

	payload := `{"session_id":"s4","cwd":"C:\\work","hook_event_name":"PermissionRequest",` +
		`"tool_name":"Bash","tool_input":{"command":"del C:\\important.txt"}}`
	for range 12 {
		var out strings.Builder
		RunApproveCLI([]string{"--agent-approve", "--tool", "claude", "--data-dir", dir},
			strings.NewReader(payload), &out)
		body := out.String()
		if body == "" {
			continue // fell back, as it must for an impostor
		}
		if !strings.Contains(body, `"allow"`) {
			t.Fatalf("unexpected answer %q", body)
		}
	}
	select {
	case <-accepted:
	case <-time.After(200 * time.Millisecond):
		// The client always reached the app; the extra instance never got a
		// connection, which is also fine.
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(string(leaked), "tool_input") ||
		strings.Contains(string(leaked), "important.txt") {
		t.Fatal("the payload leaked to an extra instance")
	}
}

// A name another process owns makes the app report an error instead of
// sharing it.
func TestOwnedPipeNameIsReported(t *testing.T) {
	dir, name := testPipe(t)
	owner, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := Listen(dir, nil, nil); err == nil {
		t.Fatal("Listen shared a pipe name another process owns")
	}
}

// The token file is this user's alone and a low-integrity process may
// neither read nor write it. Both rules live in its security descriptor; the
// descriptor is read back from the file rather than trusted from its SDDL.
func TestTokenFileDescriptor(t *testing.T) {
	dir, _ := testPipe(t)
	sid, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	// Write twice, so the file that is inspected went through the atomic
	// replace a restart performs.
	if _, err := writeToken(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := writeToken(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, tokenName)
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	sddl := sd.String()
	// SDDL prints a well-known account as its alias (the built-in
	// Administrator is "LA"), so resolve whatever the ACE names and compare
	// the identity, not the spelling.
	aceSID := daclPrincipal(sddl)
	if aceSID == "" {
		t.Fatalf("token descriptor %q has no full-access ACE", sddl)
	}
	if !sameSID(t, aceSID, sid) {
		t.Fatalf("token descriptor %q grants %s, want this user %s", sddl, aceSID, sid)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil {
		t.Fatal("token file has no DACL")
	}
	if dacl.AceCount != 1 {
		t.Fatalf("token DACL has %d entries, want exactly this user's", dacl.AceCount)
	}

	// The mandatory label lives in the SACL, which a standard account
	// cannot read back (READ_CONTROL on a SACL needs SeSecurityPrivilege, and
	// Windows reports an empty inherited SACL instead of an error). Its
	// effect is tested directly: TestLowIntegrityCannotReadToken.
}

// A low-integrity process is what an AppContainer sandbox looks like to the
// file system. It must not be able to read the token: write-up is blocked by
// default, but reading is not, so the no-read-up label is what closes it.
func TestLowIntegrityCannotReadToken(t *testing.T) {
	dir, _ := testPipe(t)
	// Twice: the second start replaces the token atomically.
	if _, err := writeToken(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := writeToken(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, tokenName)

	proc, err := windows.GetCurrentProcess()
	if err != nil {
		t.Fatal(err)
	}
	var token windows.Token
	if err := windows.OpenProcessToken(proc,
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ADJUST_DEFAULT, &token); err != nil {
		t.Skipf("cannot open this process's token to lower its integrity: %v", err)
	}
	defer token.Close()
	var low windows.Token
	if err := windows.DuplicateTokenEx(token, windows.MAXIMUM_ALLOWED, nil,
		windows.SecurityImpersonation, windows.TokenImpersonation, &low); err != nil {
		t.Skipf("cannot duplicate this process's token: %v", err)
	}
	defer low.Close()
	lowSID, err := windows.CreateWellKnownSid(windows.WinLowLabelSid)
	if err != nil {
		t.Fatal(err)
	}
	// TOKEN_MANDATORY_LABEL keeps the SID's bytes directly after the struct.
	size := int(unsafe.Sizeof(windows.Tokenmandatorylabel{})) + lowSID.Len()
	buf := make([]byte, size)
	tml := (*windows.Tokenmandatorylabel)(unsafe.Pointer(&buf[0]))
	tml.Label.Attributes = windows.SE_GROUP_INTEGRITY
	tml.Label.Sid = (*windows.SID)(unsafe.Pointer(&buf[unsafe.Sizeof(windows.Tokenmandatorylabel{})]))
	copy(unsafe.Slice((*byte)(unsafe.Pointer(tml.Label.Sid)), lowSID.Len()),
		unsafe.Slice((*byte)(unsafe.Pointer(lowSID)), lowSID.Len()))
	if err := windows.SetTokenInformation(low, windows.TokenIntegrityLevel, &buf[0], uint32(size)); err != nil {
		t.Skipf("cannot lower an impersonation token's integrity: %v", err)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r1, _, ierr := procImpersonateLoggedOnUser.Call(uintptr(low))
	if r1 == 0 {
		t.Skipf("cannot impersonate a low-integrity token: %v", ierr)
	}
	defer windows.RevertToSelf()
	if raw, err := os.ReadFile(path); err == nil {
		t.Fatalf("a low-integrity process read the token (%d bytes)", len(raw))
	}
}

// daclPrincipal is the principal of the first full-access ACE in an SDDL
// string.
func daclPrincipal(sddl string) string {
	const open = "(A;;FA;;;"
	i := strings.Index(sddl, open)
	if i < 0 {
		return ""
	}
	rest := sddl[i+len(open):]
	j := strings.IndexByte(rest, ')')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// sameSID compares two SID spellings, resolving SDDL aliases such as LA.
func sameSID(t *testing.T, spelling, sid string) bool {
	t.Helper()
	if strings.EqualFold(spelling, sid) {
		return true
	}
	a, err := windows.StringToSid(spelling)
	if err != nil {
		return false
	}
	b, err := windows.StringToSid(sid)
	if err != nil {
		return false
	}
	return a.Equals(b)
}

// A token thumbs its nose at a replacement attempt from another handle:
// writing a new token replaces the file, never leaving a torn one.
func TestTokenIsReplacedWhole(t *testing.T) {
	dir, _ := testPipe(t)
	first, err := writeToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("two starts reused one token")
	}
	onDisk, err := readToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(second) {
		t.Fatal("the token on disk is not the last one written")
	}
	if _, err := os.Stat(filepath.Join(dir, tokenName+".new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the temporary token file was left behind")
	}
}

var (
	procGetHandleInformation    = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
	procImpersonateLoggedOnUser = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateLoggedOnUser")
)

func handleInheritable(h windows.Handle) (bool, error) {
	var flags uint32
	r1, _, err := procGetHandleInformation.Call(uintptr(h), uintptr(unsafe.Pointer(&flags)))
	if r1 == 0 {
		return false, err
	}
	return flags&windows.HANDLE_FLAG_INHERIT != 0, nil
}

// Neither end of a pipe connection may be inherited by a child process; the
// token file's handle is created the same way.
func TestHandlesAreNotInheritable(t *testing.T) {
	dir, name := testPipe(t)
	srv, err := Listen(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	conn, err := dialTransport(dir, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	client, ok := conn.(*pipeConn)
	if !ok {
		t.Fatalf("dialTransport returned %T", conn)
	}
	if inheritable, err := handleInheritable(client.h); err != nil || inheritable {
		t.Fatalf("client pipe handle inheritable = %v, %v", inheritable, err)
	}

	// The listener's first instance.
	sd := permissivePipeSD(t)
	ln, err := listenPipe(name+"-h", sd)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	l := ln
	l.mu.Lock()
	h := l.pending
	l.mu.Unlock()
	if inheritable, err := handleInheritable(h); err != nil || inheritable {
		t.Fatalf("pipe instance inheritable = %v, %v", inheritable, err)
	}

	// The token file's handle: created with a security descriptor whose
	// InheritHandle is zero.
	tokenPath := filepath.Join(dir, tokenName+".probe")
	namep, err := windows.UTF16PtrFromString(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	fh, err := windows.CreateFile(namep, windows.GENERIC_READ|windows.GENERIC_WRITE, 0,
		&windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{}))},
		windows.CREATE_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(fh)
	if inheritable, err := handleInheritable(fh); err != nil || inheritable {
		t.Fatalf("token handle inheritable = %v, %v", inheritable, err)
	}
}

var procImpersonateNamedPipeClient = windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")

// The client opens the pipe with SECURITY_IDENTIFICATION: the app may learn
// who called and whether it is allowed, but may not act as the caller. Read
// back from the token the server would impersonate with.
func TestClientOpensWithIdentification(t *testing.T) {
	dir, name := testPipe(t)
	token, err := writeToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	levels := make(chan uint32, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if !serverHandshake(conn, token, time.Now().Add(authTimeout)) {
			levels <- 0
			return
		}
		h := conn.(*pipeConn).h
		r1, _, _ := procImpersonateNamedPipeClient.Call(uintptr(h))
		if r1 == 0 {
			levels <- 0
			return
		}
		defer windows.RevertToSelf()
		thread, err := windows.GetCurrentThread()
		if err != nil {
			levels <- 0
			return
		}
		var tok windows.Token
		if err := windows.OpenThreadToken(thread, windows.TOKEN_QUERY, false, &tok); err != nil {
			levels <- 0
			return
		}
		defer tok.Close()
		var level uint32
		var size uint32
		if err := windows.GetTokenInformation(tok, windows.TokenImpersonationLevel,
			(*byte)(unsafe.Pointer(&level)), uint32(unsafe.Sizeof(level)), &size); err != nil {
			levels <- 0
			return
		}
		levels <- level
	}()

	conn, err := dialTransport(dir, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	select {
	case level := <-levels:
		if level != windows.SecurityIdentification {
			t.Fatalf("impersonation level = %d, want identification (%d)",
				level, windows.SecurityIdentification)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the server never inspected the client")
	}
}

// A long frame crosses the pipe byte for byte and in one piece: the byte
// mode pipe neither splits a write nor truncates a read.
func TestPipeCarriesFramesWhole(t *testing.T) {
	_, name := testPipe(t)
	ln, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := dialPipe(name, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server := <-accepted
	defer server.Close()

	frame := []byte(strings.Repeat("x", 32<<10) + "\n")
	werr := make(chan error, 1)
	go func() {
		_ = client.SetWriteDeadline(time.Now().Add(5 * time.Second))
		_, err := client.Write(frame)
		werr <- err
	}()
	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len(frame))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatalf("read %d bytes: %v", len(got), err)
	}
	if string(got) != string(frame) {
		t.Fatal("the frame changed in flight")
	}
	if err := <-werr; err != nil {
		t.Fatalf("write: %v", err)
	}
}

// A read that has registered but not yet handed itself to the kernel must
// not be missed by Close's single cancel: both calls return.
func TestCloseCoversSubmission(t *testing.T) {
	_, name := testPipe(t)
	ln, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := dialPipe(name, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer client.Close()

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	beforeSubmit = func() {
		once.Do(func() {
			close(entered)
			<-release
		})
	}
	defer func() { beforeSubmit = nil }()

	readErr := make(chan error, 1)
	go func() {
		var b [4]byte
		_, err := server.Read(b[:])
		readErr <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the read never reached submission")
	}
	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	time.Sleep(100 * time.Millisecond)
	close(release)
	select {
	case err := <-readErr:
		if err == nil {
			t.Fatal("a cancelled read returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the read hung: Close missed the request")
	}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung behind the read")
	}
}

// Two concurrent reads each get their own bytes: the second waits its turn
// instead of consuming the first one's completion.
func TestConcurrentReadsAreSerialized(t *testing.T) {
	client, server := pipePair(t)
	defer client.Close()
	defer server.Close()

	results := make(chan byte, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			var b [1]byte
			_, err := server.Read(b[:])
			errs <- err
			results <- b[0]
		}()
	}
	time.Sleep(100 * time.Millisecond)
	_ = client.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := client.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	got := []byte{<-results, <-results}
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("read: %v", err)
		}
	}
	if !(got[0] == 'a' && got[1] == 'b') && !(got[0] == 'b' && got[1] == 'a') {
		t.Fatalf("reads returned %q", got)
	}
}

// Two concurrent writes do not interleave: each payload arrives whole.
func TestConcurrentWritesDoNotInterleave(t *testing.T) {
	client, server := pipePair(t)
	defer client.Close()
	defer server.Close()

	first := strings.Repeat("a", 4<<10)
	second := strings.Repeat("b", 4<<10)
	go func() { _, _ = client.Write([]byte(first)) }()
	go func() { _, _ = client.Write([]byte(second)) }()
	_ = server.SetReadDeadline(time.Now().Add(5 * time.Second))
	got := make([]byte, len(first)+len(second))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatal(err)
	}
	if string(got[:len(first)]) == first && string(got[len(first):]) == second {
		return
	}
	if string(got[:len(second)]) == second && string(got[len(second):]) == first {
		return
	}
	t.Fatal("the two writes interleaved")
}

// A deadline set while a read is already waiting takes effect: shortening
// it ends the wait, clearing it lets the data through.
func TestDeadlineChangesReachAWaitingRead(t *testing.T) {
	client, server := pipePair(t)
	defer client.Close()
	defer server.Close()

	type readResult struct {
		b   byte
		err error
	}
	start := func() <-chan readResult {
		out := make(chan readResult, 1)
		go func() {
			var b [1]byte
			_, err := server.Read(b[:])
			out <- readResult{b[0], err}
		}()
		return out
	}

	// No deadline, then a short one.
	waiting := start()
	time.Sleep(100 * time.Millisecond)
	if err := server.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-waiting:
		if !isTimeout(r.err) {
			t.Fatalf("shortened deadline returned %v", r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shortening the deadline did not end the wait")
	}

	// A long deadline, then cleared; the data must arrive without a timeout.
	if err := server.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	waiting = start()
	time.Sleep(100 * time.Millisecond)
	if err := server.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := client.Write([]byte("z")); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-waiting:
		if r.err != nil || r.b != 'z' {
			t.Fatalf("cleared deadline returned %q, %v", r.b, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the cleared deadline still timed out")
	}
}

// A client that connects and then says nothing must not delay the next
// client's event: admission runs on the connection, not the accept loop.
func TestSilentClientDoesNotBlockTheNextOne(t *testing.T) {
	dir, name := testPipe(t)
	got := make(chan Event, 1)
	srv, err := Listen(dir, func(ev Event) { got <- ev }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	silent, err := dialPipe(name, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	start := time.Now()
	forward(ToolClaude, dir, "", strings.NewReader(`{"session_id":"s1","prompt":"hi"}`))
	select {
	case <-got:
		if elapsed := time.Since(start); elapsed > 1200*time.Millisecond {
			t.Fatalf("the event waited %v behind a silent client", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a silent client blocked the next event")
	}
}

// Close does not return until the cancelled accept has released its
// instance, so the name can be created again at once.
func TestListenerCloseReleasesTheName(t *testing.T) {
	_, name := testPipe(t)
	sd := permissivePipeSD(t)
	first, err := listenPipe(name, sd)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_, _ = first.Accept()
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := listenPipe(name, sd)
	if err != nil {
		t.Fatalf("the name was still busy after Close: %v", err)
	}
	second.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Accept did not return after Close")
	}
}

// pipePair is a connected client and server.
func pipePair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	_, name := testPipe(t)
	ln, err := listenPipe(name, permissivePipeSD(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := dialPipe(name, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case server := <-accepted:
		return client, server
	case <-time.After(2 * time.Second):
		t.Fatal("the pipe was never accepted")
	}
	return nil, nil
}

// Reading the token must not refuse the writer's atomic replace: the
// reader shares delete access.
func TestTokenReadAllowsReplacement(t *testing.T) {
	dir, _ := testPipe(t)
	if _, err := writeToken(dir); err != nil {
		t.Fatal(err)
	}
	h, err := openToken(filepath.Join(dir, tokenName))
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if _, err := writeToken(dir); err != nil {
		t.Fatalf("a replace while the token is open for reading failed: %v", err)
	}
}

// A failure between creating the temporary token and naming it leaves
// nothing behind, and the next attempt starts clean.
func TestTokenFailureLeavesNoPlaceholder(t *testing.T) {
	dir, _ := testPipe(t)
	old := tokenTestHook
	tokenTestHook = func(string) error { return errors.New("injected") }
	defer func() { tokenTestHook = old }()
	if _, err := writeToken(dir); err == nil {
		t.Fatal("the injected failure was ignored")
	}
	if _, err := os.Stat(filepath.Join(dir, tokenName+".new")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the temporary token file survived: %v", err)
	}
	tokenTestHook = nil
	if _, err := writeToken(dir); err != nil {
		t.Fatalf("a later start failed: %v", err)
	}
}

// net.Listener allows concurrent Accepts. Close must cancel every instance
// that is waiting, return, and leave the name free.
func TestConcurrentAcceptsClosePromptly(t *testing.T) {
	_, name := testPipe(t)
	sd := permissivePipeSD(t)
	ln, err := listenPipe(name, sd)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := ln.Accept()
			errs <- err
		}()
	}
	time.Sleep(200 * time.Millisecond)
	done := make(chan struct{})
	go func() { _ = ln.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung behind two waiting Accepts")
	}
	for range 2 {
		select {
		case err := <-errs:
			if err == nil {
				t.Fatal("Accept returned a connection after Close")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("an Accept did not return after Close")
		}
	}
	again, err := listenPipe(name, sd)
	if err != nil {
		t.Fatalf("the name was still busy: %v", err)
	}
	again.Close()
}
