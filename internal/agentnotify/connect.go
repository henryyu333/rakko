package agentnotify

// Installing and removing Rakko's own entries in a tool's configuration.
//
// The files belong to the user and to the tool, so:
//
//   - only Rakko's entries are added or removed; the rest of the file keeps
//     its bytes (order, indentation, spacing),
//   - a backup is written next to the file before the first change,
//   - the write is atomic and keeps the file's mode,
//   - a file that cannot be parsed is never touched,
//   - a file Rakko created disappears again when the switch goes off, and
//     an empty file it filled goes back to empty.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State is what the settings row says about one tool.
type State int

const (
	// StateNotInjected means Rakko's entry is absent (or the switch is off).
	StateNotInjected State = iota
	// StateInjected means Rakko's entry is in the tool's configuration.
	StateInjected
	// StateUnsupported means the tool has no channel Rakko can install into.
	StateUnsupported
	// StateError means the configuration could not be read or written; the
	// detail says why and is shown in the row.
	StateError
)

// hookTimeout is the seconds a tool gives Rakko's forwarder. The forwarder
// never waits on Rakko: it writes to a socket and exits.
const hookTimeout = 5

// StableExe reports whether exe is the binary of an installed app bundle.
// A development build runs from a temporary directory; injecting its path
// would leave the tools with a command that stops working at once, so only
// a bundle is written into their configuration.
func StableExe(exe string) bool {
	return strings.Contains(exe, ".app/Contents/MacOS/")
}

// marker identifies Rakko's own entries inside a hooks document.
func marker(tool Tool) string { return "--agent-hook --tool " + string(tool) }

// namedHook is the member Rakko owns in a hooks document keyed by hook name.
const namedHook = moduleMarker

// HookCommand is the command line written into a tool's configuration. It
// runs the forwarder only while the app binary still exists: after the app
// is moved to the Trash the command succeeds and does nothing, so the agent
// keeps working.
func HookCommand(exe string, tool Tool, dataDir string) string {
	return hookCommandFor(exe, tool, dataDir, "")
}

// hookCommandFor is HookCommand naming the event on the command line, for
// tools whose payload does not.
func hookCommandFor(exe string, tool Tool, dataDir, event string) string {
	call := shellQuote(exe) + " " + marker(tool)
	if event != "" {
		call += " --event " + shellQuote(event)
	}
	if dataDir != "" {
		call += " --data-dir " + shellQuote(dataDir)
	}
	return "if [ -x " + shellQuote(exe) + " ]; then " + call + "; fi"
}

// ownsModule reports whether a module file is Rakko's: a script carries
// moduleMarker in its header, a JSON hook file Rakko's command.
func ownsModule(raw []byte, tool Tool) bool {
	return bytes.Contains(raw, []byte(moduleMarker)) || bytes.Contains(raw, []byte(marker(tool)))
}

// shellQuote quotes one word for /bin/sh so the shell hands it over
// unchanged.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		// A backslash is an escape character to the shell even though it is
		// a legal part of a macOS path: "/Users/u/a\b" must be quoted or the
		// tool is asked for "ab".
		if r < 0x20 || strings.ContainsRune(" \t'\"`$&|;<>()*?[]~#!{}\\", r) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Status reports whether Rakko's entry is installed for tool. The detail is
// a short reason when the state is StateError.
func Status(home string, tool Tool) (State, string) {
	s, ok := specFor(tool)
	if !ok {
		return StateUnsupported, ""
	}
	path := s.config(home)
	raw, existed, _, err := readConfig(path)
	if err != nil {
		return StateError, err.Error()
	}
	switch s.kind {
	case KindModule:
		if !existed {
			return StateNotInjected, ""
		}
		if ownsModule(raw, tool) {
			return StateInjected, ""
		}
		return StateError, path + " was not written by Rakko"
	case KindNamed:
		if !existed {
			return StateNotInjected, ""
		}
		doc, derr := newJSONDoc(raw)
		if derr != nil {
			return StateError, derr.Error()
		}
		if member, _ := doc.root.lookup(namedHook); member != nil {
			return StateInjected, ""
		}
		return StateNotInjected, ""
	default:
		if !existed {
			return StateNotInjected, ""
		}
		doc, derr := newJSONDoc(raw)
		if derr != nil {
			return StateError, derr.Error()
		}
		if hooksHasMarker(doc, tool) {
			return StateInjected, ""
		}
		return StateNotInjected, ""
	}
}

// Connect installs Rakko's entries for tool. exe is the app binary, dataDir
// the app's data directory (empty when it is the default).
func Connect(home, dataDir, exe string, tool Tool) error {
	s, ok := specFor(tool)
	if !ok {
		return fmt.Errorf("agentnotify: %s has no hook channel", tool)
	}
	switch s.kind {
	case KindModule:
		return writeModule(s, home, dataDir, exe)
	case KindNamed:
		return connectNamed(s, home, dataDir, exe)
	default:
		return connectHooks(s, home, dataDir, exe)
	}
}

// Disconnect removes every entry Rakko added for tool and puts back what
// the file looked like before the first change.
func Disconnect(home, dataDir string, tool Tool) error {
	s, ok := specFor(tool)
	if !ok {
		return fmt.Errorf("agentnotify: %s has no hook channel", tool)
	}
	switch s.kind {
	case KindModule:
		return removeModule(s, home, dataDir)
	case KindNamed:
		return disconnectNamed(s, home, dataDir)
	default:
		return disconnectHooks(s, home, dataDir)
	}
}

// connectHooks adds one entry per event unless the file already carries it.
func connectHooks(s spec, home, dataDir, exe string) error {
	path := s.config(home)
	raw, existed, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	doc, derr := newJSONDoc(raw)
	if derr != nil {
		return fmt.Errorf("agentnotify: %s: %w", path, derr)
	}
	if existed && !bytes.Contains(raw, []byte(marker(s.tool))) && isDedicated(s.tool) {
		// The file is Rakko's own name; anything else in it is not ours to
		// replace.
		return fmt.Errorf("agentnotify: %s exists and was not written by Rakko; left alone", path)
	}
	changed := false
	if s.version != "" && (!existed || len(bytes.TrimSpace(raw)) == 0) {
		if v, _ := doc.root.lookup("version"); v == nil {
			if err := doc.appendMember(doc.root, "version", func(string) string { return s.version }); err != nil {
				return err
			}
		}
	}
	timeout := s.timeout
	if timeout == 0 {
		timeout = hookTimeout
	}
	for _, ev := range s.events {
		added, aerr := addHook(doc, s, exe, dataDir, ev, timeout)
		if aerr != nil {
			return aerr
		}
		changed = changed || added
	}
	if !changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	backed, err := backupOnce(path, existed)
	if err != nil {
		return err
	}
	recordOrigin(dataDir, path, originRecord{
		Created:  !existed,
		Blank:    existed && len(bytes.TrimSpace(raw)) == 0,
		BackedUp: backed,
	})
	m := os.FileMode(0o600)
	if existed {
		m = mode.Perm()
	}
	return writeAtomic(path, doc.bytes(), m)
}

// disconnectHooks removes Rakko's entries, and the containers it emptied.
func disconnectHooks(s spec, home, dataDir string) error {
	path := s.config(home)
	raw, existed, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	if !existed {
		forgetOrigin(dataDir, path)
		return nil
	}
	doc, derr := newJSONDoc(raw)
	if derr != nil {
		return fmt.Errorf("agentnotify: %s: %w", path, derr)
	}
	if !removeHooks(doc, s.tool) {
		return nil
	}
	rec := readOrigin(dataDir, path)
	after := doc.bytes()
	switch {
	case rec.Created && (isEmptyObject(string(after)) || onlyVersion(after)):
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		forgetOrigin(dataDir, path)
		return nil
	case isEmptyObject(string(after)) && rec.Blank:
		after = nil
	}
	if err := writeAtomic(path, after, mode.Perm()); err != nil {
		return err
	}
	if isEmptyObject(string(after)) && !rec.BackedUp {
		forgetOrigin(dataDir, path)
	}
	return nil
}

// isDedicated reports whether the tool's file is Rakko's alone (Grok's
// per-hook file), as opposed to a shared settings document.
func isDedicated(tool Tool) bool { return tool == ToolGrok }

// writeModule writes the plugin/extension file for a module tool.
func writeModule(s spec, home, dataDir, exe string) error {
	path := s.config(home)
	if raw, existed, _, err := readConfig(path); err != nil {
		return err
	} else if existed && !ownsModule(raw, s.tool) {
		return fmt.Errorf("agentnotify: %s exists and was not written by Rakko; left alone", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	body := renderModule(s.module, s.tool, exe, dataDir)
	recordOrigin(dataDir, path, originRecord{Created: true})
	return writeAtomic(path, []byte(body), 0o644)
}

// removeModule deletes Rakko's module file and nothing else.
func removeModule(s spec, home, dataDir string) error {
	path := s.config(home)
	raw, existed, _, err := readConfig(path)
	if err != nil || !existed {
		forgetOrigin(dataDir, path)
		return err
	}
	if !ownsModule(raw, s.tool) {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	forgetOrigin(dataDir, path)
	return nil
}

// onlyVersion reports whether a document holds nothing but the "version"
// Rakko started it with.
func onlyVersion(text []byte) bool {
	doc, err := newJSONDoc(text)
	if err != nil || doc.root == nil || doc.root.kind != jObject || len(doc.root.members) != 1 {
		return false
	}
	v, _ := doc.root.lookup("version")
	return v != nil
}

// connectNamed adds Rakko's member to a hooks document keyed by hook name:
// {"rakko-agent-completion": {"<event>": [{"type", "command", "timeout"}]}}.
func connectNamed(s spec, home, dataDir, exe string) error {
	path := s.config(home)
	raw, existed, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	doc, derr := newJSONDoc(raw)
	if derr != nil {
		return fmt.Errorf("agentnotify: %s: %w", path, derr)
	}
	if member, _ := doc.root.lookup(namedHook); member != nil {
		return nil
	}
	timeout := s.timeout
	if timeout == 0 {
		timeout = hookTimeout
	}
	render := func(indent string) string {
		inner := indent + doc.indent
		var b strings.Builder
		b.WriteString("{\n")
		for i, ev := range s.events {
			event := ""
			if s.eventArg {
				event = ev
			}
			b.WriteString(inner + quoteJSON(ev) + ": [{" +
				`"type": "command", "command": ` + quoteJSON(hookCommandFor(exe, s.tool, dataDir, event)) +
				fmt.Sprintf(`, "timeout": %d}]`, timeout))
			if i < len(s.events)-1 {
				b.WriteString(",")
			}
			b.WriteString("\n")
		}
		b.WriteString(indent + "}")
		return b.String()
	}
	if err := doc.appendMember(doc.root, namedHook, render); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	backed, err := backupOnce(path, existed)
	if err != nil {
		return err
	}
	recordOrigin(dataDir, path, originRecord{
		Created:  !existed,
		Blank:    existed && len(bytes.TrimSpace(raw)) == 0,
		BackedUp: backed,
	})
	m := os.FileMode(0o600)
	if existed {
		m = mode.Perm()
	}
	return writeAtomic(path, doc.bytes(), m)
}

// disconnectNamed removes Rakko's member, and the file when Rakko made it.
func disconnectNamed(s spec, home, dataDir string) error {
	path := s.config(home)
	raw, existed, mode, err := readConfig(path)
	if err != nil {
		return err
	}
	if !existed {
		forgetOrigin(dataDir, path)
		return nil
	}
	doc, derr := newJSONDoc(raw)
	if derr != nil {
		return fmt.Errorf("agentnotify: %s: %w", path, derr)
	}
	idx := memberIndex(doc.root, namedHook)
	if idx < 0 {
		return nil
	}
	if err := doc.removeMember(doc.root, idx); err != nil {
		return err
	}
	rec := readOrigin(dataDir, path)
	after := doc.bytes()
	switch {
	case isEmptyObject(string(after)) && rec.Created:
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		forgetOrigin(dataDir, path)
		return nil
	case isEmptyObject(string(after)) && rec.Blank:
		after = nil
	}
	return writeAtomic(path, after, mode.Perm())
}

// addHook appends Rakko's entry for ev unless it is already there. Every
// edit re-reads the tree, because a splice replaces the parsed document.
func addHook(doc *jsonDoc, s spec, exe, dataDir, ev string, timeout int) (bool, error) {
	tool := s.tool
	hooks, _ := doc.root.lookup("hooks")
	if hooks == nil {
		if err := doc.appendMember(doc.root, "hooks", func(string) string { return "{}" }); err != nil {
			return false, err
		}
		hooks, _ = doc.root.lookup("hooks")
	}
	if hooks == nil || hooks.kind != jObject {
		return false, errors.New("agentnotify: \"hooks\" is not an object")
	}
	arr, _ := hooks.lookup(ev)
	if arr == nil {
		if err := doc.appendMember(hooks, ev, func(string) string { return "[]" }); err != nil {
			return false, err
		}
		hooks, _ = doc.root.lookup("hooks")
		arr, _ = hooks.lookup(ev)
	}
	if arr == nil || arr.kind != jArray {
		return false, fmt.Errorf("agentnotify: hooks.%s is not an array", ev)
	}
	for _, item := range arr.items {
		if itemHasMarker(doc, item, tool) {
			return false, nil
		}
	}
	event := ""
	if s.eventArg {
		event = ev
	}
	command := hookCommandFor(exe, tool, dataDir, event)
	render := func(indent string) string {
		inner := indent + doc.indent
		if s.flat {
			return "{\n" +
				inner + `"command": ` + quoteJSON(command) + ",\n" +
				inner + fmt.Sprintf(`"timeout": %d`, timeout) + "\n" + indent + "}"
		}
		deep := inner + doc.indent
		return "{\n" + inner + `"hooks": [` + "\n" +
			deep + "{\n" +
			deep + doc.indent + `"type": ` + quoteJSON("command") + ",\n" +
			deep + doc.indent + `"command": ` + quoteJSON(command) + ",\n" +
			deep + doc.indent + fmt.Sprintf(`"timeout": %d`, timeout) + "\n" +
			deep + "}\n" + inner + "]\n" + indent + "}"
	}
	return true, doc.appendItem(arr, render)
}

// hooksHasMarker reports whether any hooks array holds Rakko's command.
func hooksHasMarker(doc *jsonDoc, tool Tool) bool {
	hooks, _ := doc.root.lookup("hooks")
	if hooks == nil || hooks.kind != jObject {
		return false
	}
	for _, m := range hooks.members {
		if m.value == nil || m.value.kind != jArray {
			continue
		}
		for _, item := range m.value.items {
			if itemHasMarker(doc, item, tool) {
				return true
			}
		}
	}
	return false
}

// removeHooks deletes every command carrying Rakko's marker for a tool, and
// the containers it leaves empty.
func removeHooks(doc *jsonDoc, tool Tool) bool {
	changed := false
	// One command per step: every splice re-parses the document, so the
	// nodes of the previous step no longer describe the file.
	for range 4096 {
		if !removeOneHookCommand(doc, tool) {
			break
		}
		changed = true
	}
	dropEmptyHookContainers(doc)
	return changed
}

// removeOneHookCommand drops exactly one of Rakko's commands. A hook list
// item that holds other commands keeps them: only the inner command with our
// marker goes, and the item itself only when nothing else is left in it.
func removeOneHookCommand(doc *jsonDoc, tool Tool) bool {
	hooks, _ := doc.root.lookup("hooks")
	if hooks == nil || hooks.kind != jObject {
		return false
	}
	for _, key := range memberKeys(hooks) {
		hooks, _ := doc.root.lookup("hooks")
		if hooks == nil {
			return false
		}
		arr, _ := hooks.lookup(key)
		if arr == nil || arr.kind != jArray {
			continue
		}
		for i := range arr.items {
			// Re-resolve: an earlier step may have shifted the spans.
			hooks, _ = doc.root.lookup("hooks")
			if hooks == nil {
				return false
			}
			arr, _ = hooks.lookup(key)
			if arr == nil || arr.kind != jArray || i >= len(arr.items) {
				break
			}
			item := arr.items[i]
			inner, _ := item.lookup("hooks")
			if inner == nil || inner.kind != jArray {
				// A command written directly as the item: remove it whole.
				if itemHasMarker(doc, item, tool) {
					return doc.removeItem(arr, i) == nil
				}
				continue
			}
			for j := range inner.items {
				item = reResolveHookItem(doc, key, i)
				if item == nil {
					break
				}
				inner, _ = item.lookup("hooks")
				if inner == nil || inner.kind != jArray || j >= len(inner.items) {
					break
				}
				if !itemHasMarker(doc, inner.items[j], tool) {
					continue
				}
				if err := doc.removeItem(inner, j); err != nil {
					return false
				}
				// An item that held nothing but our command goes too.
				item = reResolveHookItem(doc, key, i)
				if item == nil {
					return true
				}
				if inner, _ := item.lookup("hooks"); inner != nil && inner.kind == jArray && len(inner.items) == 0 {
					hooks, _ = doc.root.lookup("hooks")
					if hooks == nil {
						return true
					}
					if arr, _ := hooks.lookup(key); arr != nil && arr.kind == jArray && i < len(arr.items) {
						doc.removeItem(arr, i)
					}
				}
				return true
			}
		}
	}
	return false
}

// reResolveHookItem returns hooks[event][index] after a splice.
func reResolveHookItem(doc *jsonDoc, event string, index int) *jnode {
	hooks, _ := doc.root.lookup("hooks")
	if hooks == nil || hooks.kind != jObject {
		return nil
	}
	arr, _ := hooks.lookup(event)
	if arr == nil || arr.kind != jArray || index >= len(arr.items) {
		return nil
	}
	return arr.items[index]
}

// dropEmptyHookContainers removes the event arrays and the hooks object Rakko
// emptied, so a file the user no longer needs does not keep our scaffolding.
func dropEmptyHookContainers(doc *jsonDoc) {
	for range 64 {
		hooks, _ := doc.root.lookup("hooks")
		if hooks == nil || hooks.kind != jObject {
			return
		}
		dropped := false
		for _, key := range memberKeys(hooks) {
			hooks, _ = doc.root.lookup("hooks")
			if hooks == nil {
				return
			}
			arr, _ := hooks.lookup(key)
			if arr == nil || arr.kind != jArray || len(arr.items) != 0 {
				continue
			}
			if idx := memberIndex(hooks, key); idx >= 0 {
				if err := doc.removeMember(hooks, idx); err != nil {
					return
				}
				dropped = true
				break
			}
		}
		if !dropped {
			break
		}
	}
	if hooks, _ := doc.root.lookup("hooks"); hooks != nil && hooks.kind == jObject && len(hooks.members) == 0 {
		if idx := memberIndex(doc.root, "hooks"); idx >= 0 {
			_ = doc.removeMember(doc.root, idx)
		}
	}
}

func memberKeys(obj *jnode) []string {
	keys := make([]string, 0, len(obj.members))
	for _, m := range obj.members {
		keys = append(keys, m.key)
	}
	return keys
}

func memberIndex(obj *jnode, key string) int {
	_, idx := obj.lookup(key)
	return idx
}

// itemHasMarker reports whether an array item is one of Rakko's entries.
func itemHasMarker(doc *jsonDoc, item *jnode, tool Tool) bool {
	if item == nil || item.start < 0 || item.end > len(doc.src) {
		return false
	}
	return bytes.Contains(doc.src[item.start:item.end], []byte(marker(tool)))
}

// readConfig reads a settings file, reporting whether it exists.
func readConfig(path string) ([]byte, bool, os.FileMode, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, err
	}
	return data, true, fi.Mode(), nil
}

// writeAtomic replaces path with data, creating the file with mode.
func writeAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".rakko-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// backupOnce copies the file next to itself before the first change Rakko
// makes to it. A second connect does not add another copy.
func backupOnce(path string, existed bool) (bool, error) {
	if !existed {
		return false, nil
	}
	if matches, _ := filepath.Glob(path + ".rakko-backup-*"); len(matches) > 0 {
		return true, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	name := fmt.Sprintf("%s.rakko-backup-%s", path, time.Now().Format("20060102-150405"))
	// The backup is the only copy of what the user had: put it on disk
	// before the file it copies is replaced.
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return true, nil
		}
		return false, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(name)
		return false, err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(name)
		return false, err
	}
	return true, f.Close()
}

// isEmptyObject reports whether text holds nothing but an empty object.
func isEmptyObject(text string) bool {
	return strings.TrimSpace(text) == "{}"
}

// The files Rakko writes belong to the user, so what it needs to remember
// about them is kept here instead: whether it created the file, and whether
// the file held no settings before the first write. A disconnect then puts
// back exactly what was there — a missing file stays missing, an empty file
// stays empty.

// StateFileName is the bookkeeping file in Rakko's data directory.
const StateFileName = "agent-notify.json"

// originRecord is what one configuration file looked like before Rakko
// first wrote it.
type originRecord struct {
	Created  bool `json:"created,omitempty"`
	Blank    bool `json:"blank,omitempty"`
	BackedUp bool `json:"backedUp,omitempty"`
}

type connectState struct {
	Files map[string]originRecord `json:"files"`
}

// readOrigin returns the recorded origin of path, if any.
func readOrigin(dataDir, path string) originRecord {
	return readConnectState(dataDir).Files[path]
}

// recordOrigin stores the origin of path once. Failures are ignored: the
// note only makes a later disconnect tidier, and a read-only data directory
// must not stop a connection.
func recordOrigin(dataDir, path string, rec originRecord) {
	if dataDir == "" {
		return
	}
	st := readConnectState(dataDir)
	if _, ok := st.Files[path]; ok {
		return
	}
	st.Files[path] = rec
	writeConnectState(dataDir, st)
}

// forgetOrigin drops the note for path once the file is back to its origin.
func forgetOrigin(dataDir, path string) {
	if dataDir == "" {
		return
	}
	st := readConnectState(dataDir)
	if _, ok := st.Files[path]; !ok {
		return
	}
	delete(st.Files, path)
	writeConnectState(dataDir, st)
}

func readConnectState(dataDir string) connectState {
	st := connectState{Files: map[string]originRecord{}}
	if dataDir == "" {
		return st
	}
	b, err := os.ReadFile(filepath.Join(dataDir, StateFileName))
	if err != nil {
		return st
	}
	var onDisk connectState
	if json.Unmarshal(b, &onDisk) != nil {
		return st
	}
	if onDisk.Files != nil {
		st.Files = onDisk.Files
	}
	return st
}

func writeConnectState(dataDir string, st connectState) {
	if dataDir == "" {
		return
	}
	if len(st.Files) == 0 {
		os.Remove(filepath.Join(dataDir, StateFileName))
		return
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return
	}
	path := filepath.Join(dataDir, StateFileName)
	_ = writeAtomic(path, append(b, '\n'), 0o600)
}
