//go:build darwin

package main

// The local preview server: the real panel and settings views, rendered by
// ui.NewTester at Retina scale over a fake desktop, served to a browser at
// 127.0.0.1:5199. Input comes back from the page and is forwarded to the
// Tester; the clock runs in real time so the springs play as they do in the
// app. It touches no user data, opens no window and posts no real events.
//
//	RAKKO_PREVIEW=1 go test -run TestPreviewServer -timeout 0 ./cmd/shelfu

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const (
	previewAddr = "127.0.0.1:5199"
	// The shared preview folder in the main worktree (git ignored): the
	// annotations the page saves and the shots they make.
	previewRoot = "/Users/y3u/AIProjects/test/y3/.scratch/preview"
)

// TestPreviewServer is the only entry point. Without RAKKO_PREVIEW=1 it
// skips, so the normal test suite never starts a server.
func TestPreviewServer(t *testing.T) {
	if os.Getenv("RAKKO_PREVIEW") != "1" {
		t.Skip("set RAKKO_PREVIEW=1 to run the preview server")
	}
	w := newPreviewWorld(t)
	s := &previewServer{
		world:       w,
		clients:     map[*previewClient]bool{},
		cmds:        make(chan func(), 256),
		recordings:  filepath.Join(previewRoot, "recordings"),
		annotations: filepath.Join(previewRoot, "annotations.json"),
		shots:       filepath.Join(previewRoot, "shots"),
	}
	for _, dir := range []string{s.recordings, s.shots} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("preview dir: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/events", s.handleEvents)
	mux.HandleFunc("/api/input", s.handleInput)
	mux.HandleFunc("/api/state", s.handleState)
	mux.HandleFunc("/api/record", s.handleRecord)
	mux.HandleFunc("/api/annotations", s.handleAnnotations)
	mux.Handle("/rec/", http.StripPrefix("/rec/", http.FileServer(http.Dir(s.recordings))))
	mux.Handle("/shots/", http.StripPrefix("/shots/", http.FileServer(http.Dir(s.shots))))

	ln, err := net.Listen("tcp", previewAddr)
	if err != nil {
		t.Fatalf("preview server: %v", err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		log.Printf("preview: http://%s", previewAddr)
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("preview: serve: %v", err)
		}
	}()
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	t.Cleanup(func() { srv.Close() })
	s.run(ctx)
}

// previewClient is one EventSource stream.
type previewClient struct {
	ch chan []byte
}

// previewServer owns the HTTP side; the world is only touched from run's
// goroutine, and HTTP handlers post closures to it.
type previewServer struct {
	world *previewWorld

	mu         sync.Mutex
	clients    map[*previewClient]bool
	lastFrame  []byte
	lastHash   [20]byte
	lastState  string
	seq        int
	recording  bool
	pressed    bool
	pendingPtr *mygo.Point
	wheelDX    float32
	wheelDY    float32
	text       strings.Builder

	recordings  string
	annotations string
	shots       string

	stateMu   sync.RWMutex
	stateName string
	lastTick  time.Time
	textAt    time.Time

	cmds chan func()
}

// currentState is the state name the page should show, safe for handlers.
func (s *previewServer) currentState() string {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.stateName == "" {
		return previewCollapsed
	}
	return s.stateName
}

func (s *previewServer) setCurrentState(name string) {
	s.stateMu.Lock()
	s.stateName = name
	s.stateMu.Unlock()
}

// post queues fn for the world's goroutine without blocking the caller.
func (s *previewServer) post(fn func()) {
	select {
	case s.cmds <- fn:
	default:
		// A full queue means the page is far ahead of the renderer; the
		// newest input is what matters, so drop the oldest.
		select {
		case <-s.cmds:
		default:
		}
		select {
		case s.cmds <- fn:
		default:
		}
	}
}

func (s *previewServer) run(ctx context.Context) {
	s.lastTick = time.Now()
	tick := time.NewTicker(16 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-s.cmds:
			fn()
		case now := <-tick.C:
			d := now.Sub(s.lastTick)
			s.lastTick = now
			if d > 0 {
				s.world.shelfView.Advance(d)
				if s.world.settingsView != nil {
					s.world.settingsView.Advance(d)
				}
			}
			s.flushInput()
			s.world.a.animate(now)
			s.render()
		}
	}
}

// flushInput sends the pending pointer move, wheel and typed text before the
// frame is built. Pointer moves wait for a resting surface: a Tester renders
// up to 20 frames per input, which would cost more than the move shows while
// a spring is in flight, and the latest position is kept.
func (s *previewServer) flushInput() {
	if s.text.Len() > 0 && time.Now().After(s.textAt) {
		text := s.text.String()
		s.text.Reset()
		s.activeTester().Type(text)
	}
	if s.wheelDX != 0 || s.wheelDY != 0 {
		dx, dy := s.wheelDX, s.wheelDY
		s.wheelDX, s.wheelDY = 0, 0
		x, y := s.pointerCanvas()
		if s.pressed || s.world.settingsShown {
			s.activeTester().Wheel(float32(x), float32(y), dx, dy)
		}
	}
	t := s.activeTester()
	if s.pendingPtr != nil && (!s.shellBusy() || s.pressed) {
		p := s.pendingPtr
		s.pendingPtr = nil
		x, y := s.pointCanvas(*p)
		t.Move(float32(x), float32(y))
	}
}

// activeTester is the surface keys and clicks belong to: the settings
// window when it is up, otherwise the notch panel.
func (s *previewServer) activeTester() *ui.Tester {
	if s.world.settingsShown && s.world.settingsView != nil {
		return s.world.settingsView
	}
	return s.world.shelfView
}

// shellBusy reports a spring, a fade or a page switch still drawing. A
// focused text field keeps asking for caret frames forever, so the tester's
// own FramePending is too broad to gate pointer moves on.
func (s *previewServer) shellBusy() bool {
	a := s.world.a
	return !a.animationStart.IsZero() || !a.peekAnimStart.IsZero() || !a.closeAt.IsZero() ||
		a.fx.openMoving || a.fx.sizeMoving || a.tabPhase(time.Now()) < 1
}

// pointerCanvas is the current pointer inside the headless surface.
func (s *previewServer) pointerCanvas() (int, int) { return s.pointCanvas(s.world.pointer) }

func (s *previewServer) pointCanvas(p mygo.Point) (int, int) {
	if s.world.settingsShown {
		r := settingsFrame(s.world.a.dispWork)
		return p.X - r.X, p.Y - r.Y
	}
	ox, oy := s.world.canvasOrigin()
	return p.X - ox, p.Y - oy
}

// inLayer reports which surface p belongs to.
func (s *previewServer) inLayer(p mygo.Point) (panel, settings bool) {
	if s.world.settingsShown {
		if inFrame(p, settingsFrame(s.world.a.dispWork)) {
			return false, true
		}
	}
	if inFrame(p, s.world.a.desiredFrame()) {
		return true, false
	}
	return false, false
}

// render builds one frame with the current state, skips it when nothing
// changed, and marks the annotation context.
func (s *previewServer) render() {
	s.world.shelfView.Frame()
	if s.world.settingsView != nil {
		s.world.settingsView.Frame()
	}
	layers := s.world.windows()
	var h hash.Hash = sha1.New()
	for _, l := range layers {
		var head [24]byte
		binary.LittleEndian.PutUint64(head[0:], uint64(l.X))
		binary.LittleEndian.PutUint64(head[8:], uint64(l.Y))
		binary.LittleEndian.PutUint64(head[16:], uint64(l.W)<<16|uint64(l.H&0xffff))
		h.Write(head[:])
		h.Write(l.Img.Pix)
	}
	var sum [20]byte
	copy(sum[:], h.Sum(nil))
	if sum == s.lastHash && s.world.state == s.lastState {
		return
	}
	s.lastHash, s.lastState = sum, s.world.state
	s.seq++

	type layerMsg struct {
		Name   string `json:"name"`
		X      int    `json:"x"`
		Y      int    `json:"y"`
		W      int    `json:"w"`
		H      int    `json:"h"`
		Scale  int    `json:"scale"`
		Opaque bool   `json:"opaque"`
		PNG    string `json:"png"`
	}
	msg := struct {
		Type   string     `json:"type"`
		Seq    int        `json:"seq"`
		State  string     `json:"state"`
		View   string     `json:"view"`
		Layers []layerMsg `json:"layers"`
		When   int64      `json:"when"`
	}{Type: "frame", Seq: s.seq, State: s.world.state, When: time.Now().UnixMilli()}
	if len(layers) > 0 {
		msg.View = layers[0].Name
	}
	for _, l := range layers {
		msg.Layers = append(msg.Layers, layerMsg{
			Name: l.Name, X: l.X, Y: l.Y, W: l.W, H: l.H, Scale: l.Scale,
			Opaque: l.Opaque,
			PNG:    base64.StdEncoding.EncodeToString(pngBytes(l.Img)),
		})
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	ev := []byte("event: frame\ndata: " + string(data) + "\n\n")
	s.mu.Lock()
	s.lastFrame = ev
	s.mu.Unlock()
	s.broadcast(ev)
}

// broadcast fans one event out, dropping frames for clients that fell
// behind: the next frame is a full picture, so nothing is lost for good.
func (s *previewServer) broadcast(ev []byte) {
	s.mu.Lock()
	clients := make([]*previewClient, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		select {
		case c.ch <- ev:
		default:
			select {
			case <-c.ch:
			default:
			}
			select {
			case c.ch <- ev:
			default:
			}
		}
	}
}

func (s *previewServer) broadcastState(name string) {
	data, _ := json.Marshal(map[string]any{"type": "state", "name": name})
	s.broadcast([]byte("event: state\ndata: " + string(data) + "\n\n"))
}

// --- HTTP -----------------------------------------------------------------

func (s *previewServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(previewHTML))
}

func (s *previewServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")

	c := &previewClient{ch: make(chan []byte, 4)}
	s.mu.Lock()
	s.clients[c] = true
	last := s.lastFrame
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.clients, c)
		s.mu.Unlock()
	}()

	// A reconnect gets the current picture at once and the state it should
	// restore (the page keeps it in the URL hash and asks again).
	if last != nil {
		_, _ = w.Write(last)
	}
	stateData, _ := json.Marshal(map[string]any{"type": "state", "name": s.currentState()})
	if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", stateData); err != nil {
		return
	}
	fl.Flush()

	keep := time.NewTicker(15 * time.Second)
	defer keep.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-c.ch:
			if _, err := w.Write(ev); err != nil {
				return
			}
			fl.Flush()
		case <-keep.C:
			if _, err := fmt.Fprint(w, ": keep\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

type previewInput struct {
	Kind string  `json:"kind"` // move, down, up, wheel, key, type
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	DX   float64 `json:"dx"`
	DY   float64 `json:"dy"`
	Key  string  `json:"key"`
	Mods int     `json:"mods"` // 1 shift, 2 ctrl, 4 alt, 8 cmd
	Text string  `json:"text"`
}

func (s *previewServer) handleInput(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var in previewInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p := mygo.Point{X: int(in.X), Y: int(in.Y)}
	switch in.Kind {
	case "move":
		s.post(func() {
			s.world.pointer = p
			s.world.hoverAt(p)
			pp := p
			s.pendingPtr = &pp
		})
	case "down":
		s.post(func() {
			s.world.pointer = p
			if !s.world.settingsShown && !s.world.a.visible && !s.world.a.peeking &&
				s.world.a.overAnchor(p) {
				s.world.a.show(false)
				return
			}
			panel, settings := s.inLayer(p)
			if panel || settings {
				x, y := s.pointCanvas(p)
				s.activeTester().Press(float32(x), float32(y))
				s.pressed = true
			} else if s.world.a.visible && !s.world.settingsShown {
				s.world.a.hide(hideExplicit)
			}
		})
	case "up":
		s.post(func() {
			s.world.pointer = p
			if s.pressed {
				s.pressed = false
				x, y := s.pointCanvas(p)
				s.activeTester().Release(float32(x), float32(y))
			}
		})
	case "wheel":
		s.post(func() {
			s.world.pointer = p
			if !s.world.a.visible && !s.world.settingsShown {
				return
			}
			s.wheelDX += float32(in.DX)
			s.wheelDY += float32(in.DY)
		})
	case "type":
		if in.Text == "" {
			return
		}
		s.post(func() {
			if s.world.a.visible || s.world.settingsShown {
				s.text.WriteString(in.Text)
				s.textAt = time.Now().Add(40 * time.Millisecond)
			}
		})
	case "key":
		mods, key, ok := previewKey(in.Key, in.Mods)
		if !ok {
			return
		}
		s.post(func() {
			// Text typed before a special key must land first (Enter on a
			// half-typed query, for example).
			t := s.activeTester()
			if s.text.Len() > 0 {
				text := s.text.String()
				s.text.Reset()
				t.Type(text)
			}
			t.Key(mods, key)
		})
	default:
		http.Error(w, "unknown kind", http.StatusBadRequest)
	}
	w.WriteHeader(http.StatusNoContent)
}

// previewKey maps a browser KeyboardEvent.key to the mygo key and
// modifiers. Printable keys are typed as text by the caller, so only
// named/control keys are here.
func previewKey(k string, mods int) (ui.Modifiers, ui.Key, bool) {
	var m ui.Modifiers
	if mods&1 != 0 {
		m |= ui.Shift
	}
	if mods&2 != 0 {
		m |= ui.Ctrl
	}
	if mods&4 != 0 {
		m |= ui.Alt
	}
	if mods&8 != 0 {
		m |= ui.Cmd
	}
	switch k {
	case "Enter":
		return m, ui.KeyEnter, true
	case "Escape":
		return m, ui.KeyEscape, true
	case "Backspace":
		return m, ui.KeyBackspace, true
	case "Tab":
		return m, ui.KeyTab, true
	case "Delete":
		return m, ui.KeyDelete, true
	case "ArrowUp":
		return m, ui.KeyUp, true
	case "ArrowDown":
		return m, ui.KeyDown, true
	case "ArrowLeft":
		return m, ui.KeyLeft, true
	case "ArrowRight":
		return m, ui.KeyRight, true
	case "Home":
		return m, ui.KeyHome, true
	case "End":
		return m, ui.KeyEnd, true
	case "PageUp":
		return m, ui.KeyPageUp, true
	case "PageDown":
		return m, ui.KeyPageDown, true
	case "Space":
		return m, ui.KeySpace, true
	}
	// Letters and digits arrive as DOM codes so a modified key (Cmd+C) is
	// layout independent; plain typing comes as text instead.
	if len(k) == 4 && strings.HasPrefix(k, "Key") {
		ch := k[3]
		if ch >= 'A' && ch <= 'Z' {
			return m, ui.KeyA + ui.Key(ch-'A'), true
		}
	}
	if len(k) == 6 && strings.HasPrefix(k, "Digit") {
		ch := k[5]
		if ch >= '0' && ch <= '9' {
			return m, ui.Key0 + ui.Key(ch-'0'), true
		}
	}
	switch k {
	case "Minus":
		return m, ui.KeyMinus, true
	case "Equal":
		return m, ui.KeyEqual, true
	case "Comma":
		return m, ui.KeyComma, true
	case "Period":
		return m, ui.KeyPeriod, true
	case "Slash":
		return m, ui.KeySlash, true
	case "Semicolon":
		return m, ui.KeySemicolon, true
	case "Quote":
		return m, ui.KeyQuote, true
	case "BracketLeft":
		return m, ui.KeyBracketLeft, true
	case "BracketRight":
		return m, ui.KeyBracketRight, true
	case "Backslash":
		return m, ui.KeyBackslash, true
	case "Backquote":
		return m, ui.KeyBackquote, true
	}
	return 0, 0, false
}

func (s *previewServer) handleState(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		_, _ = w.Write([]byte(s.currentState()))
	case http.MethodPost:
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !validPreviewState(body.Name) {
			http.Error(w, "unknown state", http.StatusBadRequest)
			return
		}
		s.post(func() {
			s.world.applyState(body.Name)
			s.setCurrentState(body.Name)
			s.lastState = ""
			s.broadcastState(body.Name)
		})
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}

func validPreviewState(name string) bool {
	switch name {
	case previewCollapsed, previewHover, previewSearch, previewResults, previewTray,
		previewClipboard, previewTools, previewUsage, previewDrag,
		previewCharge, previewCharged, previewLow, previewHeadset, previewHeadsetOff,
		previewDownload, previewSettings:
		return true
	}
	switch name {
	case previewSettings + "-general", previewSettings + "-notch", previewSettings + "-tray",
		previewSettings + "-clipboard", previewSettings + "-usage", previewSettings + "-shortcuts",
		previewSettings + "-permissions", previewSettings + "-about":
		return true
	}
	return false
}

// --- recording ------------------------------------------------------------

type previewRecordFrame struct {
	I     int    `json:"i"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Scale int    `json:"scale"`
	State string `json:"state"`
}

func (s *previewServer) handleRecord(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	switch body.Action {
	case "open", "switch", "close", "hover", "settings":
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	s.post(func() { s.record(body.Action) })
	w.WriteHeader(http.StatusNoContent)
}

// record runs one action on a 60 Hz virtual clock and writes every frame.
// The loop is blocked meanwhile; the clock stays aligned with real time
// because each frame waits for its wall-clock moment before it is drawn.
func (s *previewServer) record(action string) {
	if s.recording {
		return
	}
	s.recording = true
	defer func() { s.recording = false }()
	start := time.Now()
	s.applyRecordAction(action)
	const fps = 60
	const frames = 84 // 1.4 s: every spring in the panel is done
	id := start.Format("20060102-150405")
	dir := filepath.Join(s.recordings, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("preview: recording dir: %v", err)
		return
	}
	type frameMeta struct {
		previewRecordFrame
		URL string `json:"url"`
	}
	var metas []frameMeta
	for i := range frames {
		vt := start.Add(time.Duration(i) * time.Second / fps)
		if d := time.Until(vt); d > 0 {
			time.Sleep(d)
		}
		s.world.shelfView.Advance(time.Second / fps)
		if s.world.settingsView != nil {
			s.world.settingsView.Advance(time.Second / fps)
		}
		s.world.a.animate(vt)
		s.world.shelfView.Frame()
		if s.world.settingsView != nil {
			s.world.settingsView.Frame()
		}
		layers := s.world.windows()
		if len(layers) == 0 {
			continue
		}
		l := layers[0]
		name := fmt.Sprintf("%03d.png", i)
		if err := os.WriteFile(filepath.Join(dir, name), pngBytes(l.Img), 0o644); err != nil {
			log.Printf("preview: frame: %v", err)
			return
		}
		metas = append(metas, frameMeta{
			previewRecordFrame: previewRecordFrame{I: i, X: l.X, Y: l.Y, W: l.W, H: l.H, Scale: l.Scale, State: l.Name},
			URL:                "/rec/" + id + "/" + name,
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"type": "record", "id": id, "action": action, "fps": fps, "state": s.currentState(), "frames": metas,
	})
	s.broadcast([]byte("event: record\ndata: " + string(payload) + "\n\n"))
	// The manual 60 Hz steps already moved the Tester's clock; the loop
	// must not add the recording's wall time again.
	s.lastTick = time.Now()
	s.pruneRecordings()
	s.lastState = ""
}

// applyRecordAction starts the recorded gesture from the current state.
func (s *previewServer) applyRecordAction(action string) {
	a := s.world.a
	switch action {
	case "open":
		if a.visible {
			a.goHome()
		} else {
			a.show(false)
		}
	case "switch":
		if !a.visible {
			a.show(false)
		}
		cur := a.currentPage().id()
		next := pageTray
		switch cur {
		case pageTray:
			next = pageClipboard
		case pageClipboard:
			next = pageTools
		case pageTools:
			next = pageUsage
		default:
			next = pageTray
		}
		a.setLevel(2)
		a.setTab(next)
	case "close":
		a.hide(hideExplicit)
	case "hover":
		// Start from folded so the recording always shows the growth.
		a.peeking, a.peekProgress, a.peekFrom = false, 0, 0
		a.peekAnimStart = time.Time{}
		a.setPeek(true)
	case "settings":
		s.world.applyState(previewSettings + "-general")
	}
}

// pruneRecordings keeps the eight newest recordings.
func (s *previewServer) pruneRecordings() {
	entries, err := os.ReadDir(s.recordings)
	if err != nil {
		return
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	for len(dirs) > 8 {
		_ = os.RemoveAll(filepath.Join(s.recordings, dirs[0]))
		dirs = dirs[1:]
	}
}

// --- annotations ----------------------------------------------------------

type previewAnnotation struct {
	ID    string  `json:"id"`
	Tool  string  `json:"tool"` // rect, arrow, pin
	State string  `json:"state"`
	Frame int     `json:"frame"`
	X     float64 `json:"x"`
	Y     float64 `json:"y"`
	X2    float64 `json:"x2"`
	Y2    float64 `json:"y2"`
	Text  string  `json:"text"`
	Color string  `json:"color"`
}

func (s *previewServer) handleAnnotations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		data, err := os.ReadFile(s.annotations)
		if err != nil {
			_, _ = w.Write([]byte(`{"annotations":[]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	case http.MethodPost:
		var body struct {
			Annotations []previewAnnotation `json:"annotations"`
			PNG         string              `json:"png"`
			State       string              `json:"state"`
			Frame       int                 `json:"frame"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		out, _ := json.MarshalIndent(map[string]any{"annotations": body.Annotations}, "", "  ")
		tmp := s.annotations + ".tmp"
		if err := os.WriteFile(tmp, out, 0o644); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := os.Rename(tmp, s.annotations); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		shot := ""
		if body.PNG != "" {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(body.PNG, "data:image/png;base64,"))
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			shot = filepath.Join(s.shots, fmt.Sprintf("shot-%s-%s.png", time.Now().Format("20060102-150405"), body.State))
			if err := os.WriteFile(shot, raw, 0o644); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
		}
		_, _ = w.Write([]byte(shot))
	default:
		http.Error(w, "GET or POST", http.StatusMethodNotAllowed)
	}
}
