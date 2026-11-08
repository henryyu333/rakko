package agentnotify

// The app side: one unix socket inside the data directory, mode 0600, in a
// directory Rakko creates 0700. Only this user can open either, which is
// the whole admission rule for completion events; an approval request also
// checks the peer's uid, and the hook client checks that the process on the
// other end is this Rakko before it accepts a decision.
//
// Completion events are one line and close. Approval requests use the same
// connection in both directions: one line in, one decision line back, then
// close.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// protoVersion is the wire version the forwarder and the app share.
const protoVersion = 1

// askVersion is the approval frame's version.
const askVersion = 2

// AskKind is the frame kind an approval request carries.
const AskKind = "ask"

// askReadLimit bounds the input object inside one request.
const askReadLimit = forwardReadLimit

// askReplyTimeout bounds writing a decision back to a hook.
const askReplyTimeout = 200 * time.Millisecond

// answeredTTL is how long one request id is remembered.
const answeredTTL = 5 * time.Minute

// Decision values.
const (
	DecisionAllow = "allow"
	DecisionDeny  = "deny"
	DecisionPass  = "pass"
)

// Event is one tool event on its way to the notch.
type Event struct {
	V       int    `json:"v"`
	Tool    Tool   `json:"tool"`
	Event   string `json:"event"`
	Session string `json:"session"`
	Cwd     string `json:"cwd,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// Ask is one permission request, as the hook client sends it.
type Ask struct {
	V        int             `json:"v"`
	Kind     string          `json:"kind"`
	ID       string          `json:"id"`
	Tool     Tool            `json:"tool"`
	Event    string          `json:"event,omitempty"`
	Session  string          `json:"session,omitempty"`
	Cwd      string          `json:"cwd,omitempty"`
	ToolName string          `json:"tool_name"`
	Input    json.RawMessage `json:"tool_input"`
}

// Decision is the app's answer.
type Decision struct {
	ID       string `json:"id"`
	Decision string `json:"decision"`
	Message  string `json:"message,omitempty"`
}

// Server accepts hook events and approval requests.
type Server struct {
	path    string
	ln      net.Listener
	handler func(Event)
	ask     func(context.Context, Ask) Decision
	done    chan struct{}
	once    sync.Once

	mu       sync.Mutex
	answered map[string]time.Time
}

// Listen starts the server in dataDir. handler runs on its own goroutine
// for each accepted completion event and must not block. ask may block; it
// runs on the request's own goroutine, and ctx is cancelled when the hook
// hangs up.
func Listen(dataDir string, handler func(Event), ask func(context.Context, Ask) Decision) (*Server, error) {
	path := filepath.Join(dataDir, SocketName)
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	// A leftover socket from a crashed run would block Listen.
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("agentnotify: " + path + " exists and is not a socket")
		}
		_ = os.Remove(path)
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	s := &Server{path: path, ln: ln, handler: handler, ask: ask,
		done: make(chan struct{}), answered: map[string]time.Time{}}
	go s.accept()
	return s, nil
}

// Close stops the server and removes the socket.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		close(s.done)
		s.ln.Close()
		_ = os.Remove(s.path)
	})
}

func (s *Server) accept() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// A transient failure (too many open files, say) must not end
			// every later notice; try again shortly.
			time.Sleep(50 * time.Millisecond)
			continue
		}
		// One user only: a connection from another account is closed
		// without reading a byte.
		if uid, err := peerUID(conn); err != nil || uid != uint32(os.Getuid()) {
			conn.Close()
			continue
		}
		go s.handle(conn)
	}
}

// handle runs one connection: one line, then either a completion (no
// reply) or an approval (one decision back).
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(conn, forwardReadLimit)).ReadBytes('\n')
	if err != nil {
		return
	}
	// The request is in; the read deadline was for the framing only. An
	// approval connection stays open while its card waits, and a leftover
	// deadline would cancel it two seconds later.
	_ = conn.SetReadDeadline(time.Time{})
	var env struct {
		V    int    `json:"v"`
		Kind string `json:"kind"`
	}
	if json.Unmarshal(line, &env) != nil {
		return
	}
	if env.V == askVersion || env.Kind == AskKind {
		s.handleAsk(conn, line)
		return
	}
	if env.V != protoVersion {
		return
	}
	var ev Event
	if json.Unmarshal(line, &ev) != nil || !ev.Tool.Valid() {
		return
	}
	if s.handler != nil {
		s.handler(ev)
	}
}

// handleAsk validates one request, lets it wait for a decision, and writes
// that decision back. Every malformed frame closes the connection without
// a reply; the client turns that into its tool's fallback.
func (s *Server) handleAsk(conn net.Conn, line []byte) {
	var a Ask
	if json.Unmarshal(line, &a) != nil || !validAsk(&a) {
		return
	}
	if s.ask == nil {
		_ = writeDecision(conn, Decision{ID: a.ID, Decision: DecisionPass})
		return
	}
	if !s.remember(a.ID) {
		_ = writeDecision(conn, Decision{ID: a.ID, Decision: DecisionPass})
		return
	}
	// The hook may hang up while the card is up: in an interactive session
	// Esc kills the hook process, and the card must go with it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		var b [1]byte
		n, rerr := conn.Read(b[:])
		if rerr != nil || n > 0 {
			cancel()
		}
	}()
	d := s.ask(ctx, a)
	if d.Decision != DecisionAllow && d.Decision != DecisionDeny {
		d.Decision = DecisionPass
	}
	d.ID = a.ID
	_ = writeDecision(conn, d)
}

// remember reports whether this id is new. A repeat within five minutes
// gets no second card and is answered with pass.
func (s *Server) remember(id string) bool {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, at := range s.answered {
		if now.Sub(at) > answeredTTL {
			delete(s.answered, key)
		}
	}
	if _, ok := s.answered[id]; ok {
		return false
	}
	s.answered[id] = now
	return true
}

// validAsk checks every field the app relies on. Event and cwd are display
// material only; the rest must have the shapes the protocol states.
func validAsk(a *Ask) bool {
	if a.V != askVersion || a.Kind != AskKind {
		return false
	}
	if !validID(a.ID) || !a.Tool.Valid() || !a.Tool.Approvable() {
		return false
	}
	if len(a.ToolName) > 256 || len(a.Event) > 64 || len(a.Session) > 256 || len(a.Cwd) > 4096 {
		return false
	}
	if len(a.Input) > askReadLimit {
		return false
	}
	trimmed := bytes.TrimSpace(a.Input)
	if len(trimmed) == 0 {
		a.Input = json.RawMessage("{}")
		return true
	}
	if trimmed[0] != '{' || !json.Valid(trimmed) {
		return false
	}
	return true
}

// validID is 16 random bytes written as 32 lowercase hex digits.
func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for i := range len(id) {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func writeDecision(conn net.Conn, d Decision) error {
	body, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_ = conn.SetWriteDeadline(time.Now().Add(askReplyTimeout))
	_, err = conn.Write(append(body, '\n'))
	return err
}
