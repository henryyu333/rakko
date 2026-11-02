//go:build darwin

package main

// Agent completion notices: the app side of internal/agentnotify. The
// socket only reports; this file keeps each session's latest task in
// memory, drops a completion that repeats the same session within two
// seconds, and hands the rest to the notch's live queue.

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"

	"shelf/internal/agentnotify"
)

// agentNotifyAgentWindow is how long two completions from one session are
// treated as the same report (Grok reads Claude's config too, a resumed
// turn can fire twice).
const agentNotifyAgentWindow = 2 * time.Second

// agentNotifyTaskTTL bounds the in-memory task table: a session that has
// not spoken for this long is forgotten, so a day of terminals does not
// grow without end.
const agentNotifyTaskTTL = 2 * time.Hour

// agentNotifyHome and agentNotifyExe are seams: tests point them at a
// temporary home and a fake bundle binary; production reads the real ones.
var (
	agentNotifyHome = os.UserHomeDir
	agentNotifyExe  = os.Executable
	agentRunOnMain  = mygo.RunOnMain
)

// agentNeedsAppNote marks the one error the UI words itself: the switch is
// on but this build is not an installed app, so nothing was written.
const agentNeedsAppNote = "needs-app"

// agentTask is one session's latest prompt and when it arrived.
type agentTask struct {
	task string
	at   time.Time
}

// agentNotifyFeature holds the socket, the per-session task names and the
// settings rows' last known state.
type agentNotifyFeature struct {
	host *shelf
	home string
	dir  string
	srv  *agentnotify.Server

	mu     sync.Mutex
	tasks  map[string]agentTask
	posted map[string]time.Time
	// states and notes are the settings rows' last computed answer.
	states    map[agentnotify.Tool]agentnotify.State
	notes     map[agentnotify.Tool]string
	refreshed time.Time
	// node is the whole-group failure: the binary is not an installed app.
	node string
}

func newAgentNotifyFeature(a *shelf) *agentNotifyFeature {
	home, _ := agentNotifyHome()
	return &agentNotifyFeature{
		host: a, home: home, dir: a.dataDir,
		tasks: map[string]agentTask{}, posted: map[string]time.Time{},
		states: map[agentnotify.Tool]agentnotify.State{}, notes: map[agentnotify.Tool]string{},
	}
}

// agentFeature returns the feature, creating it on first use.
func (a *shelf) agentFeature() *agentNotifyFeature {
	if a.agent == nil {
		a.agent = newAgentNotifyFeature(a)
	}
	return a.agent
}

// agentStartListening opens the socket; a failure is kept on the feature
// and shown in the settings group.
func (f *agentNotifyFeature) start() {
	if f.srv != nil {
		return
	}
	srv, err := agentnotify.Listen(f.dir, f.accept)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.node = err.Error()
		return
	}
	f.node = ""
	f.srv = srv
}

// stop closes the socket. Entries already written into a tool's
// configuration are removed by the switch handler, not here.
func (f *agentNotifyFeature) stop() {
	f.mu.Lock()
	srv := f.srv
	f.srv = nil
	f.mu.Unlock()
	srv.Close()
}

// accept folds one hook event into the session table and posts a
// completion. It runs on the socket's goroutine.
func (f *agentNotifyFeature) accept(ev agentnotify.Event) {
	now := time.Now()
	key := string(ev.Tool) + "|" + ev.Session
	if agentnotify.PromptEvent(ev.Event) {
		if task := agentTaskName(ev.Prompt); task != "" {
			f.mu.Lock()
			f.tasks[key] = agentTask{task: task, at: now}
			f.pruneLocked(now)
			f.mu.Unlock()
		}
		return
	}
	if !agentnotify.DoneEvent(ev.Event) {
		return
	}
	f.mu.Lock()
	entry := f.tasks[key]
	delete(f.tasks, key)
	last, seen := f.posted[key]
	if seen && now.Sub(last) < agentNotifyAgentWindow {
		f.mu.Unlock()
		return // the same finish, reported twice
	}
	f.posted[key] = now
	f.pruneLocked(now)
	f.mu.Unlock()

	task := entry.task
	if task == "" {
		// Plugin tools (opencode, omp, pi) carry the prompt on the
		// completion event itself, because their module kept it in memory.
		task = agentTaskName(ev.Prompt)
	}
	if task == "" {
		task = filepath.Base(ev.Cwd)
		if task == "." || task == string(filepath.Separator) || task == "" {
			task = ""
		}
	}
	tool := ev.Tool
	agentRunOnMain(func() { f.host.postAgentDone(tool, task) })
}

// pruneLocked forgets sessions and dedupe marks that aged out.
func (f *agentNotifyFeature) pruneLocked(now time.Time) {
	for key, t := range f.tasks {
		if now.Sub(t.at) > agentNotifyTaskTTL {
			delete(f.tasks, key)
		}
	}
	for key, at := range f.posted {
		if now.Sub(at) > agentNotifyTaskTTL {
			delete(f.posted, key)
		}
	}
}

// postAgentDone shows one completion capsule. Runs on the main thread.
func (a *shelf) postAgentDone(tool agentnotify.Tool, task string) {
	if task == "" {
		task = a.appLoc().LiveAgentTaskFallback()
	}
	ev := liveEvent{kind: liveAgentDone, agent: tool.Name(), task: task}
	a.pushLive(ev, liveHoldAgent, time.Now())
}

// setAgentNotify saves the switch and applies it: on starts the socket and
// writes Rakko's entries into every installed tool, off removes every entry
// again.
func (a *shelf) setAgentNotify(on bool) {
	next := a.settings
	next.AgentNotify.Enabled = on
	if !a.applySettings(next) {
		return
	}
	a.agentApply(on)
}

// agentApply is the switch's side effects, shared with startup.
func (a *shelf) agentApply(on bool) {
	f := a.agentFeature()
	if !on {
		f.stop()
		for _, tool := range agentnotify.Tools() {
			if !agentnotify.Installed(f.home, tool) {
				continue
			}
			_ = agentnotify.Disconnect(f.home, f.dir, tool)
		}
		f.refresh()
		return
	}
	f.start()
	exe, err := agentNotifyExe()
	if err != nil {
		exe = ""
	}
	if exe != "" && !agentnotify.StableExe(exe) {
		exe = "" // a development build: nothing may be written
	}
	for _, tool := range agentnotify.Tools() {
		if !agentnotify.Installed(f.home, tool) {
			continue
		}
		if exe == "" {
			continue
		}
		_ = agentnotify.Connect(f.home, f.dir, exe, tool)
	}
	f.refresh()
}

// agentStartEnabled starts listening at launch when the switch is on. It
// writes nothing: entries belong to the switch being turned on.
func (a *shelf) agentStartEnabled() {
	if !a.settings.AgentNotify.Enabled {
		return
	}
	f := a.agentFeature()
	f.start()
	f.refresh()
}

// refresh recomputes the settings rows' states. It reads only the files
// Rakko itself may have written, and only while the switch is on: with the
// switch off every row is simply 未注入 and none of the tools' files are
// opened.
func (f *agentNotifyFeature) refresh() {
	exe, _ := agentNotifyExe()
	stable := exe != "" && agentnotify.StableExe(exe)
	states := map[agentnotify.Tool]agentnotify.State{}
	notes := map[agentnotify.Tool]string{}
	on := f.host.settings.AgentNotify.Enabled
	for _, tool := range agentnotify.Tools() {
		if !agentnotify.Installed(f.home, tool) {
			continue
		}
		st, note := agentnotify.StateNotInjected, ""
		if on {
			st, note = agentnotify.Status(f.home, tool)
			if st != agentnotify.StateInjected && !stable {
				// The switch is on but this build cannot own a hook entry.
				st, note = agentnotify.StateError, agentNeedsAppNote
			}
		}
		states[tool] = st
		if note != "" {
			notes[tool] = note
		}
	}
	f.mu.Lock()
	f.states, f.notes, f.refreshed = states, notes, time.Now()
	f.mu.Unlock()
}

// refreshIfStale recomputes the rows at most once a second while the
// settings page redraws.
func (f *agentNotifyFeature) refreshIfStale() {
	f.mu.Lock()
	stale := f.states == nil || time.Since(f.refreshed) > time.Second
	f.mu.Unlock()
	if stale {
		f.refresh()
	}
}

// settingRow is one detected tool's row: its state and the reason.
func (f *agentNotifyFeature) settingRow(tool agentnotify.Tool) (agentnotify.State, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	st, ok := f.states[tool]
	return st, f.notes[tool], ok
}

// detected lists the installed tools in settings order.
func (f *agentNotifyFeature) detected() []agentnotify.Tool {
	var out []agentnotify.Tool
	for _, tool := range agentnotify.Tools() {
		if agentnotify.Installed(f.home, tool) {
			out = append(out, tool)
		}
	}
	return out
}

// agentTaskName is the task line: the prompt's first line, whitespace
// collapsed, cut to about 40 display units (20 Chinese characters) with an
// ellipsis. Pure; unit-tested.
func agentTaskName(prompt string) string {
	first := prompt
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	first = strings.Join(strings.Fields(first), " ")
	return truncateAgentTask(first)
}

// truncateAgentTask cuts at the display width: a wide (CJK) rune counts
// two units, so 20 Chinese characters and 40 Latin ones end at the same
// place on the capsule.
func truncateAgentTask(s string) string {
	const limit = 40
	units := 0
	for i, r := range s {
		w := 1
		if r > 0x2E80 {
			w = 2
		}
		if units+w > limit {
			return strings.TrimSpace(s[:i]) + "…"
		}
		units += w
	}
	return s
}
