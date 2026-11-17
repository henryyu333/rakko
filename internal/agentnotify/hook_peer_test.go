//go:build darwin

package agentnotify

// A completion event carries the prompt text, so the forwarder writes it
// only to a socket whose listener is this same Rakko. The approval client
// already insists on that (peerIsSelf); the completion path must too, or a
// process that took the socket path first reads the agent's prompts.

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testListenSock and testListenFile ask a second run of this test binary to
// accept one connection and store its line, so the forwarder can be tested
// against another process of the same executable file — exactly what the
// installed app and its hook are.
const (
	testListenSock = "RAKKO_TEST_LISTEN"
	testListenFile = "RAKKO_TEST_RECEIVED"
)

func TestMain(m *testing.M) {
	if sock := os.Getenv(testListenSock); sock != "" {
		if err := listenOnce(sock, os.Getenv(testListenFile)); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	os.Exit(m.Run())
}

// listenOnce is the helper process: one connection, one line, then done.
func listenOnce(sock, received string) error {
	ln, err := net.Listen("unix", sock)
	if err != nil {
		return err
	}
	defer ln.Close()
	conn, err := ln.Accept()
	if err != nil {
		return err
	}
	body, _ := bufio.NewReader(conn).ReadBytes('\n')
	conn.Close()
	return os.WriteFile(received, body, 0o600)
}

// selfListener is this test binary running again as a one-line listener.
type selfListener struct {
	cmd    *exec.Cmd
	done   chan error
	waited bool
}

func startSelfListener(t *testing.T, sock, received string) *selfListener {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), testListenSock+"="+sock, testListenFile+"="+received)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	l := &selfListener{cmd: cmd, done: make(chan error, 1)}
	go func() { l.done <- cmd.Wait() }()
	t.Cleanup(func() {
		if l.waited {
			return
		}
		_ = l.cmd.Process.Kill()
		<-l.done
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			return l
		}
		if time.Now().After(deadline) {
			t.Fatal("the helper never made its socket")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// await waits for the helper to finish.
func (l *selfListener) await(t *testing.T, what string) {
	t.Helper()
	l.waited = true
	select {
	case err := <-l.done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(5 * time.Second):
		_ = l.cmd.Process.Kill()
		<-l.done
		t.Fatal(what)
	}
}

// TestForwarderDeliversToItsOwnBinary: another process of this same
// executable file accepts the event, so the check admits the real
// app-and-hook pair.
func TestForwarderDeliversToItsOwnBinary(t *testing.T) {
	dir := testDataDir(t)
	received := filepath.Join(dir, "received")
	helper := startSelfListener(t, filepath.Join(dir, SocketName), received)
	forward(ToolClaude, dir, "", strings.NewReader(
		`{"session_id":"same-binary","hook_event_name":"UserPromptSubmit","cwd":"/tmp/proj","prompt":"ship the release notes"}`))
	helper.await(t, "the helper never received the event")
	body := readFile(t, received)
	if !strings.Contains(body, `"session":"same-binary"`) || !strings.Contains(body, "ship the release notes") {
		t.Fatalf("the helper did not receive the event: %q", body)
	}
}

// TestForwarderRefusesForeignServer: a listener that is not this binary
// gets no bytes at all, and the same payload still arrives once this same
// binary is the one listening.
func TestForwarderRefusesForeignServer(t *testing.T) {
	if _, err := exec.LookPath("nc"); err != nil {
		t.Skip("nc is not installed")
	}
	dir := testDataDir(t)
	sock := filepath.Join(dir, SocketName)
	received := filepath.Join(dir, "received")
	script := filepath.Join(dir, "listen.sh")
	// nc keeps reading its stdin (a live sleep) and exits when the client
	// hangs up, writing whatever it saw to received.
	writeFile(t, script, "#!/bin/sh\nsleep 5 | nc -lU "+shellQuote(sock)+" > "+shellQuote(received)+"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	server := exec.Command("/bin/sh", script)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- server.Wait() }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			_ = server.Process.Kill()
			t.Fatal("the foreign listener never made its socket")
		}
		time.Sleep(10 * time.Millisecond)
	}
	payload := `{"session_id":"foreign","hook_event_name":"UserPromptSubmit","cwd":"/tmp/proj","prompt":"delete the production database"}`
	forward(ToolClaude, dir, "", strings.NewReader(payload))

	// A write would land in the file at once; give it that chance before the
	// listener is stopped, so a leak cannot pass by timing.
	leakDeadline := time.Now().Add(500 * time.Millisecond)
	for {
		if body := readFile(t, received); body != "" {
			_ = server.Process.Kill()
			t.Fatalf("a foreign server received the event: %q", body)
		}
		if time.Now().After(leakDeadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = server.Process.Kill()
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("the foreign listener did not stop")
	}

	// The refusal is the only reason nothing arrived: this same binary on
	// the same socket path receives the same payload.
	_ = os.Remove(sock)
	helper := startSelfListener(t, sock, received)
	forward(ToolClaude, dir, "", strings.NewReader(payload))
	helper.await(t, "this binary's own listener did not receive the event")
	if body := readFile(t, received); !strings.Contains(body, `"session":"foreign"`) {
		t.Fatalf("this binary's own listener did not receive the event: %q", body)
	}
}
