package agentnotify

// The app side: one unix socket inside the data directory, mode 0600, in a
// directory Rakko creates 0700. Only this user can open either, which is
// the whole admission rule: an event can do nothing but draw a capsule, so
// the executable check the old approval hub needed would only break
// legitimate launches (a test build, a reinstalled app) that share the
// socket's owner. The hook side never reads a reply; the server drops every
// connection after one line.

import (
	"bufio"
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

// Event is one tool event on its way to the notch.
type Event struct {
	V       int    `json:"v"`
	Tool    Tool   `json:"tool"`
	Event   string `json:"event"`
	Session string `json:"session"`
	Cwd     string `json:"cwd,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
}

// Server accepts hook events.
type Server struct {
	path    string
	ln      net.Listener
	handler func(Event)
	done    chan struct{}
	once    sync.Once
}

// Listen starts the server in dataDir. handler runs on its own goroutine
// for each accepted event and must not block.
func Listen(dataDir string, handler func(Event)) (*Server, error) {
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
	s := &Server{path: path, ln: ln, handler: handler, done: make(chan struct{})}
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
		go s.handle(conn)
	}
}

// handle runs one connection: one line, then close.
func (s *Server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	line, err := bufio.NewReader(io.LimitReader(conn, forwardReadLimit)).ReadBytes('\n')
	if err != nil {
		return
	}
	var ev Event
	if json.Unmarshal(line, &ev) != nil || ev.V != protoVersion || !ev.Tool.Valid() {
		return
	}
	if s.handler != nil {
		s.handler(ev)
	}
}
