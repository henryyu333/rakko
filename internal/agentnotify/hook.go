package agentnotify

// The hook side: the app binary run as `--agent-hook --tool <name>`.
//
// It reads the tool's hook payload from stdin, turns it into the small
// event the app needs, writes that to a unix socket, and exits 0. It writes
// nothing to stdout — no tool may read a decision out of it — and it never
// waits on Rakko: a missing socket or a slow app costs it at most a couple
// of hundred milliseconds, then it leaves. When Rakko is not running the
// agent sees nothing at all.

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// SocketName is the socket inside the data directory.
const SocketName = "agent.sock"

// forwardDial and forwardWrite bound the forwarder's whole visit to Rakko.
// forwardStay is how long it keeps the connection open for the server to
// take the event: a unix peer that closes before accept has no pid left to
// verify, so the app would drop a perfectly good hook.
const (
	forwardDial  = 120 * time.Millisecond
	forwardWrite = 150 * time.Millisecond
	forwardStay  = 200 * time.Millisecond
	// forwardReadLimit caps the tool payload the forwarder will read.
	forwardReadLimit = 256 << 10
)

// RunHookCLI handles `--agent-hook`. handled=false means the flag was
// absent and the caller should continue starting the app.
func RunHookCLI(args []string, stdin io.Reader) (handled bool) {
	tool, dataDir, ok := parseHookArgs(args)
	if !ok {
		return false
	}
	forward(tool, dataDir, stdin)
	return true
}

// parseHookArgs reads the forwarder's arguments without the flag package,
// so nothing allocates more than needed on the hot path.
func parseHookArgs(args []string) (tool Tool, dataDir string, ok bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		next := func() string {
			if hasValue {
				return value
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch name {
		case "--agent-hook":
			ok = true
			// `--agent-hook=<tool>` names the tool in one word; a bare
			// --agent-hook takes no value and must not swallow the next
			// flag.
			if hasValue {
				tool = Tool(value)
			}
		case "--tool":
			tool = Tool(next())
		case "--data-dir":
			dataDir = next()
		}
	}
	return tool, dataDir, ok
}

// forward sends one event, then returns. Every failure is silent.
func forward(tool Tool, dataDir string, stdin io.Reader) {
	if !tool.Valid() {
		return
	}
	// Grok reads Claude Code's hook files too. Its own hook file reports the
	// event with the same session; the Claude command fired into a Grok
	// process must not announce it a second time as Claude Code.
	if tool == ToolClaude && os.Getenv("GROK_SESSION_ID") != "" {
		return
	}
	payload, err := io.ReadAll(io.LimitReader(stdin, forwardReadLimit))
	if err != nil {
		return
	}
	req := buildRequest(tool, payload)
	if req.Session == "" {
		return
	}
	if dataDir == "" {
		dataDir = os.Getenv("RAKKO_DATA_DIR")
	}
	if dataDir == "" {
		dir, derr := os.UserConfigDir()
		if derr != nil {
			return
		}
		dataDir = filepath.Join(dir, "Rakko")
	}
	send(filepath.Join(dataDir, SocketName), req)
}

// buildRequest turns a tool's hook payload into the wire event.
func buildRequest(tool Tool, payload []byte) Event {
	var p struct {
		SessionID     string `json:"session_id"`
		SessionIDAlt  string `json:"sessionId"`
		ThreadID      string `json:"thread_id"`
		Cwd           string `json:"cwd"`
		WorkspaceRoot string `json:"workspaceRoot"`
		EventName     string `json:"hook_event_name"`
		EventNameAlt  string `json:"hookEventName"`
		Prompt        string `json:"prompt"`
		UserPrompt    string `json:"user_prompt"`
		Message       string `json:"message"`
	}
	if len(payload) > 0 {
		_ = json.Unmarshal(payload, &p)
	}
	req := Event{
		V:       protoVersion,
		Tool:    tool,
		Session: firstNonEmpty(p.SessionID, p.SessionIDAlt, p.ThreadID, os.Getenv("GROK_SESSION_ID")),
		Cwd:     firstNonEmpty(p.Cwd, p.WorkspaceRoot),
		Prompt:  firstNonEmpty(p.Prompt, p.UserPrompt, p.Message),
		Event:   firstNonEmpty(p.EventName, p.EventNameAlt, os.Getenv("GROK_HOOK_EVENT")),
	}
	if req.Session == "" {
		// No tool names the session: the harness process is the window, so
		// its pid separates two terminals running the same tool.
		req.Session = "pid:" + strconv.Itoa(os.Getppid())
	}
	return req
}

// send writes one line to the socket and closes. It never blocks on a
// missing app: the dial's own timeout ends the attempt.
func send(path string, req Event) {
	conn, err := net.DialTimeout("unix", path, forwardDial)
	if err != nil {
		return
	}
	defer conn.Close()
	body, err := json.Marshal(req)
	if err != nil {
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(forwardWrite))
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return
	}
	// Stay connected until the app has read the line and closed: its peer
	// check needs a connected peer, and its own 2s read deadline is bounded
	// by ours.
	_ = conn.SetReadDeadline(time.Now().Add(forwardStay))
	_, _ = io.Copy(io.Discard, io.LimitReader(conn, 1))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
