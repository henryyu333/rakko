//go:build darwin

package main

// Agent completion notices: the app side of internal/agentnotify. The
// socket only reports; this file keeps each session's latest task in
// memory, drops a completion that repeats the same session within two
// seconds, and hands the rest to the notch's live queue.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

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
	tool agentnotify.Tool
	task string
	at   time.Time
}

// agentRun is one row of the home page's AGENT section: a running or a
// just-finished session. Tool and At are what the row shows beside the
// task.
type agentRun struct {
	Tool agentnotify.Tool
	Task string
	At   time.Time
}

// agentFinishedWindow is how long a finished session stays in the home
// page's AGENT section.
const agentFinishedWindow = 30 * time.Minute

// agentNotifyFeature holds the socket, the per-session task names and the
// settings rows' last known state.
type agentNotifyFeature struct {
	host *shelf
	home string
	dir  string
	srv  *agentnotify.Server

	mu       sync.Mutex
	tasks    map[string]agentTask // running: prompt seen, no completion yet
	finished map[string]agentTask // completed, kept for the home page
	posted   map[string]time.Time
	// states and notes are the settings rows' last computed answer.
	states    map[agentnotify.Tool]agentnotify.State
	notes     map[agentnotify.Tool]string
	refreshed time.Time
	// offFailed is why switching off could not clean a tool's file.
	offFailed map[agentnotify.Tool]string
	// node is the whole-group failure: the binary is not an installed app.
	node string
}

func newAgentNotifyFeature(a *shelf) *agentNotifyFeature {
	home, _ := agentNotifyHome()
	return &agentNotifyFeature{
		host: a, home: home, dir: a.dataDir,
		tasks: map[string]agentTask{}, finished: map[string]agentTask{},
		posted: map[string]time.Time{},
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
			f.tasks[key] = agentTask{tool: ev.Tool, task: task, at: now}
			delete(f.finished, key) // a new turn in this session
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
	if task != "" {
		f.mu.Lock()
		f.finished[key] = agentTask{tool: tool, task: task, at: now}
		f.mu.Unlock()
	}
	agentRunOnMain(func() { f.host.postAgentDone(tool, task) })
}

// snapshot is the home page's AGENT section: the turns under way now,
// newest first, and the ones that finished in the last 30 minutes, newest
// first. Both are copies; the caller may run off the main thread's lock.
func (f *agentNotifyFeature) snapshot(now time.Time) (running, done []agentRun) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.tasks {
		running = append(running, agentRun{Tool: t.tool, Task: t.task, At: t.at})
	}
	for _, t := range f.finished {
		if now.Sub(t.at) <= agentFinishedWindow {
			done = append(done, agentRun{Tool: t.tool, Task: t.task, At: t.at})
		}
	}
	byRecent := func(rows []agentRun) {
		sort.Slice(rows, func(i, j int) bool {
			if !rows[i].At.Equal(rows[j].At) {
				return rows[i].At.After(rows[j].At)
			}
			if rows[i].Task != rows[j].Task {
				return rows[i].Task < rows[j].Task
			}
			return rows[i].Tool < rows[j].Tool
		})
	}
	byRecent(running)
	byRecent(done)
	return running, done
}

// pruneLocked forgets sessions and dedupe marks that aged out.
func (f *agentNotifyFeature) pruneLocked(now time.Time) {
	for key, t := range f.tasks {
		if now.Sub(t.at) > agentNotifyTaskTTL {
			delete(f.tasks, key)
		}
	}
	for key, t := range f.finished {
		if now.Sub(t.at) > agentNotifyTaskTTL {
			delete(f.finished, key)
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
	ev := liveEvent{kind: liveAgentDone, agent: tool.Name(), task: task, tool: tool}
	a.pushLive(ev, liveHoldAgent, time.Now())
}

// The agent completion card (design/explore/agent-done.html, option B): a
// first line with the tool's mark, its name and a green 完成, and the task
// on a second line under the name.
const (
	agentCardW     = 360
	agentCardBody  = 46 // both lines under the notch band
	agentCardPadX  = 16
	agentCardIsleH = 58 // the island's floating card
	agentCardCheck = 16
)

// agentCardSize is the shell the card needs.
func (a *shelf) agentCardSize() (w, h float32) {
	baseW, _ := a.anchorSize()
	w = max(agentCardW, baseW+60)
	if a.island {
		return w, agentCardIsleH
	}
	return w, float32(max(32, a.notchH) + agentCardBody)
}

// agentCard draws the card's two lines inside the shell's lower band.
func (a *shelf) agentCard(c *ui.Context, w, h float32, k float32, ev liveEvent) ui.Element {
	l := loc(c)
	top := h - agentCardBody
	if a.island {
		top = (h - (agentCardBody - 8)) / 2
	}
	card := ui.Column(c.Key("live-agent-card")).Width(w).Height(h).
		Padding(top, agentCardPadX, 0, agentCardPadX).Gap(5).
		Opacity(min(max(k*k, 0), 1)).Label(ev.statusLineIn(l))
	card.Children(func() {
		ui.Row(c.Key("live-agent-l1")).FillWidth().Gap(9).AlignItems(ui.Center).Children(func() {
			logo, tint, mark := agentLogo(ev.tool)
			settingsMark(c, logo, tint, mark)
			ui.Text(c, ev.agent).SingleLine().FontSize(13).FontWeight(600).TextColor(ink).
				Shrink(1).MinWidth(0).Ellipsis("…")
			ui.Box(c).Grow(1)
			ui.Row(c.Key("live-agent-done")).Gap(6).AlignItems(ui.Center).Children(func() {
				agentCheck(c)
				ui.Text(c, l.LiveAgentDone()).SingleLine().FontSize(12).FontWeight(600).TextColor(liveGreen)
			})
		})
		ui.Text(c, ev.task).SingleLine().FontSize(12).FontWeight(500).TextColor(ink2).
			Margin(0, 0, 0, settingsRowMark+9).Shrink(1).MinWidth(0).Ellipsis("…")
	})
	return card
}

// agentCheck is the green disc with a dark tick.
func agentCheck(c *ui.Context) {
	ui.Box(c.Key("live-agent-check")).Size(agentCardCheck, agentCardCheck).Draw(func(p *ui.Painter, r ui.Rect) {
		var disc ui.Path
		disc.Circle(r.X+r.W/2, r.Y+r.H/2, r.W/2)
		p.FillPath(&disc, liveGreen)
		var tick ui.Path
		tick.MoveTo(r.X+r.W*0.27, r.Y+r.H*0.52).LineTo(r.X+r.W*0.44, r.Y+r.H*0.69).LineTo(r.X+r.W*0.74, r.Y+r.H*0.34)
		p.StrokePath(&tick, 2, ui.RGB(0, 0, 0))
	})
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
		// Every tool, not only the detected ones: an entry written while a
		// tool looked installed must still go when it no longer does.
		// Disconnect leaves files without Rakko's marker untouched; one it
		// could not clean (unparseable now) keeps its row in 出错.
		failed := map[agentnotify.Tool]string{}
		for _, tool := range agentnotify.Tools() {
			if err := agentnotify.Disconnect(f.home, f.dir, tool); err != nil {
				failed[tool] = err.Error()
			}
		}
		f.mu.Lock()
		f.offFailed = failed
		f.mu.Unlock()
		f.refresh()
		return
	}
	f.mu.Lock()
	f.offFailed = nil
	f.mu.Unlock()
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
	f.mu.Lock()
	failed := f.offFailed
	f.mu.Unlock()
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
		} else if why, bad := failed[tool]; bad {
			st, note = agentnotify.StateError, why
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
	return clipUnits(s, 40)
}

// clipUnits shortens s to units display columns, a wide (CJK) rune
// counting two, and marks the cut with an ellipsis. The caller decides how
// many columns fit.
func clipUnits(s string, limit int) string {
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
