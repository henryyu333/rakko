package agentnotify

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func listenAsk(t *testing.T, ask func(context.Context, Ask) Decision) (string, *Server) {
	t.Helper()
	// A short directory: Darwin's unix socket path is capped near 104 bytes,
	// and a long test name under /var/folders can exceed it.
	dir, err := os.MkdirTemp("/tmp", "rakko-ask-")
	if err != nil {
		t.Fatal(err)
	}
	srv, err := Listen(dir, nil, ask)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close(); os.RemoveAll(dir) })
	return filepath.Join(dir, SocketName), srv
}

func askFrame(t *testing.T, id, tool string) Ask {
	t.Helper()
	return Ask{
		V: askVersion, Kind: AskKind, ID: id, Tool: Tool(tool),
		Event: "PermissionRequest", Session: "s", Cwd: "/tmp/p",
		ToolName: "Bash", Input: json.RawMessage(`{"command":"rm -rf build"}`),
	}
}

const testAskID = "0123456789abcdef0123456789abcdef"

func TestAskRoundTrip(t *testing.T) {
	path, _ := listenAsk(t, func(ctx context.Context, a Ask) Decision {
		if a.ToolName != "Bash" || !strings.Contains(string(a.Input), "rm -rf") {
			t.Errorf("ask payload lost: %+v", a)
		}
		return Decision{ID: a.ID, Decision: DecisionAllow}
	})
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	a := askFrame(t, testAskID, "claude")
	body, _ := json.Marshal(a)
	if _, err := conn.Write(append(body, '\n')); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading decision: %v", err)
	}
	var d Decision
	if err := json.Unmarshal(line, &d); err != nil {
		t.Fatal(err)
	}
	if d.ID != testAskID || d.Decision != DecisionAllow {
		t.Fatalf("decision %+v", d)
	}
}

func TestAskRejectsBadFrames(t *testing.T) {
	called := false
	path, _ := listenAsk(t, func(context.Context, Ask) Decision {
		called = true
		return Decision{Decision: DecisionAllow}
	})
	long := strings.Repeat("x", askReadLimit+10)
	cases := []string{
		`{"v":1,"kind":"ask","id":"` + testAskID + `","tool":"claude","tool_name":"Bash","tool_input":{}}`,
		`{"v":2,"kind":"other","id":"` + testAskID + `","tool":"claude","tool_name":"Bash","tool_input":{}}`,
		`{"v":2,"kind":"ask","id":"short","tool":"claude","tool_name":"Bash","tool_input":{}}`,
		`{"v":2,"kind":"ask","id":"` + testAskID + `","tool":"cursor","tool_name":"Bash","tool_input":{}}`,
		`{"v":2,"kind":"ask","id":"` + testAskID + `","tool":"gemini","tool_name":"Bash","tool_input":{}}`,
		`{"v":2,"kind":"ask","id":"` + testAskID + `","tool":"claude","tool_name":"Bash","tool_input":[]}`,
		`{"v":2,"kind":"ask","id":"` + testAskID + `","tool":"claude","tool_name":"Bash","tool_input":{"command":"` + long + `"}}`,
	}
	for i, frame := range cases {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write(append([]byte(frame), '\n'))
		buf := make([]byte, 1)
		n, err := conn.Read(buf)
		conn.Close()
		if n != 0 || err == nil {
			t.Errorf("frame %d got a reply %q (err %v), want a closed connection", i, buf[:n], err)
		}
	}
	if called {
		t.Fatal("ask ran for an invalid frame")
	}
}

func TestAskOutlivesTheReadDeadline(t *testing.T) {
	path, _ := listenAsk(t, func(ctx context.Context, a Ask) Decision {
		select {
		case <-ctx.Done():
			return Decision{ID: a.ID, Decision: DecisionPass}
		case <-time.After(2100 * time.Millisecond):
			return Decision{ID: a.ID, Decision: DecisionAllow}
		}
	})
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	body, _ := json.Marshal(askFrame(t, testAskID, "claude"))
	conn.Write(append(body, '\n'))
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading after two seconds: %v", err)
	}
	var d Decision
	if err := json.Unmarshal(line, &d); err != nil {
		t.Fatal(err)
	}
	if d.Decision != DecisionAllow {
		t.Fatalf("decision %q after the framing deadline passed, want allow", d.Decision)
	}
}

func TestAskIdempotent(t *testing.T) {
	var calls int
	path, _ := listenAsk(t, func(ctx context.Context, a Ask) Decision {
		calls++
		return Decision{ID: a.ID, Decision: DecisionAllow}
	})
	send := func() Decision {
		conn, err := net.Dial("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		body, _ := json.Marshal(askFrame(t, testAskID, "claude"))
		conn.Write(append(body, '\n'))
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		line, err := bufio.NewReader(conn).ReadBytes('\n')
		if err != nil {
			t.Fatal(err)
		}
		var d Decision
		if err := json.Unmarshal(line, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	if d := send(); d.Decision != DecisionAllow {
		t.Fatalf("first decision %q", d.Decision)
	}
	if d := send(); d.Decision != DecisionPass {
		t.Fatalf("replay decision %q, want pass", d.Decision)
	}
	if calls != 1 {
		t.Fatalf("ask ran %d times, want once", calls)
	}
}

func TestAskContextCancel(t *testing.T) {
	done := make(chan string, 1)
	path, _ := listenAsk(t, func(ctx context.Context, a Ask) Decision {
		<-ctx.Done()
		done <- "cancelled"
		return Decision{ID: a.ID, Decision: DecisionPass}
	})
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(askFrame(t, testAskID, "claude"))
	conn.Write(append(body, '\n'))
	conn.Close()
	select {
	case got := <-done:
		if got != "cancelled" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a hanging-up client did not cancel the ask")
	}
}
