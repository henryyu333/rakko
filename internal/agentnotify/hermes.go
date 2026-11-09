package agentnotify

// Hermes Agent takes Rakko as a Python plugin. The plugin announces a
// finished turn through the same --agent-hook forwarder the other tools use,
// and registers an approval transport so a prompt Hermes itself decided to
// ask becomes a Rakko card. The plugin directory belongs to Rakko; the few
// lines it needs in ~/.hermes/config.yaml (enable the plugin, select the
// transport) are added textually and removed again, leaving the file's
// comments and formatting alone.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// hermesPluginName is both the plugin directory name and its manifest name.
const hermesPluginName = "rakko"

// hermesTransport is the transport name the plugin registers and the
// config selects.
const hermesTransport = "rakko"

// hermesReloadTimeout bounds the gateway's control-socket nudge. A gateway
// that is not running, or predates the verb, is not an error: the switch
// still takes effect at its next start.
const hermesReloadTimeout = 5 * time.Second

// hermesHome is the profile directory Hermes keeps its files in. $HERMES_HOME
// is Hermes's own override; a leading "~/" expands against home.
func hermesHome(home string) string {
	dir := os.Getenv("HERMES_HOME")
	if dir == "" {
		return join(home, ".hermes")
	}
	if dir == "~" {
		return home
	}
	if strings.HasPrefix(dir, "~/") {
		return join(home, dir[2:])
	}
	return dir
}

// hermesConfigPath is the profile's config.yaml.
func hermesConfigPath(home string) string { return join(hermesHome(home), "config.yaml") }

// hermesPluginDir is the directory Rakko owns inside Hermes's plugin
// directory.
func hermesPluginDir(home string) string { return join(hermesHome(home), "plugins", hermesPluginName) }

func hermesInitPath(home string) string {
	return join(hermesPluginDir(home), "__init__.py")
}

func hermesManifestPath(home string) string {
	return join(hermesPluginDir(home), "plugin.yaml")
}

func hermesStatePath(home string) string {
	return join(hermesPluginDir(home), "state.json")
}

// hermesState says which half of the integration is on. Both switches share
// one plugin, so the plugin stays while either half is on.
type hermesState struct {
	Completion bool `json:"completion"`
	Approval   bool `json:"approval"`
}

func readHermesState(home string) hermesState {
	var st hermesState
	raw, err := os.ReadFile(hermesStatePath(home))
	if err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func writeHermesState(home string, st hermesState) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := hermesStatePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'), 0o600)
}

// hermesManifest is the plugin manifest. The marker makes the file Rakko's.
const hermesManifest = `# ` + moduleMarker + `: written by Rakko. Removed when its switch is off.
name: ` + hermesPluginName + `
label: Rakko
kind: standalone
version: 1.0.0
description: Rakko completion notices and approval cards for Hermes.
provides_tools: []
provides_hooks:
  - post_llm_call
`

// hermesPlugin is the plugin itself. It registers both surfaces; the state
// file decides which one acts, so either switch can be turned off without
// touching the other. The approval path raises instead of answering when
// Rakko is unavailable, which hands the prompt back to Hermes's own flow.
const hermesPlugin = `# ` + moduleMarker + `: written by Rakko. Removed when its switch is off.
"""Rakko completion notices and approval cards for Hermes."""

import json
import os
import subprocess
import threading

EXE = __RAKKO_EXE__
DATA = __RAKKO_DATA__
STATE = os.path.join(os.path.dirname(os.path.abspath(__file__)), "state.json")


def _enabled(kind):
    try:
        with open(STATE, "r", encoding="utf-8") as f:
            return bool(json.load(f).get(kind))
    except Exception:
        return False


def _cwd():
    try:
        from agent.runtime_cwd import resolve_context_cwd
        path = resolve_context_cwd()
        if path:
            return str(path)
    except Exception:
        pass
    try:
        return os.getcwd()
    except Exception:
        return ""


def _args(flag):
    args = [EXE, flag, "--tool", "hermes"]
    if DATA:
        args += ["--data-dir", DATA]
    return args


def _on_post_llm_call(session_id=None, user_message=None, **kwargs):
    if not _enabled("completion"):
        return
    if not session_id or not os.path.isfile(EXE):
        return
    payload = {
        "hook_event_name": "post_llm_call",
        "session_id": session_id,
        "cwd": _cwd(),
        "prompt": user_message if isinstance(user_message, str) else "",
    }
    try:
        child = subprocess.Popen(
            _args("--agent-hook"),
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        child.stdin.write(json.dumps(payload).encode("utf-8"))
        child.stdin.close()
        threading.Thread(target=child.wait, daemon=True).start()
    except Exception:
        pass


def _present(request):
    if not _enabled("approval"):
        raise RuntimeError("rakko approval is off")
    if not os.path.isfile(EXE):
        raise RuntimeError("rakko app is missing")
    payload = {
        "hook_event_name": "pre_approval_request",
        "session_id": str(getattr(request, "request_id", "") or "hermes"),
        "cwd": _cwd(),
        "tool_name": "terminal",
        "tool_input": {
            "command": str(getattr(request, "command", "") or ""),
            "description": str(getattr(request, "description", "") or ""),
        },
    }
    try:
        limit = float(getattr(request, "timeout_seconds", 300) or 300)
    except Exception:
        limit = 300.0
    budget = max(1.0, min(57.0, limit - 2.0))
    try:
        done = subprocess.run(
            _args("--agent-approve"),
            input=json.dumps(payload).encode("utf-8"),
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            timeout=budget,
        )
        out = done.stdout.decode("utf-8", "replace").strip()
        if not out:
            raise RuntimeError("rakko did not answer")
        decision = json.loads(out)
        verdict = decision.get("decision")
        if verdict not in ("allow", "deny"):
            raise RuntimeError("rakko returned no decision")
        choice = "once" if verdict == "allow" else "deny"
    except Exception as exc:
        raise RuntimeError("rakko approval unavailable") from exc
    allowed = getattr(request, "allowed_choices", ())
    if allowed and choice not in allowed:
        raise RuntimeError("rakko returned a choice Hermes does not offer")
    return request.respond(choice)


def register(ctx):
    try:
        ctx.register_hook("post_llm_call", _on_post_llm_call)
    except Exception:
        pass
    try:
        ctx.register_approval_transport("` + hermesTransport + `", _present)
    except Exception:
        pass
`

// writeHermesPlugin writes the manifest and the plugin source, refusing to
// replace a file Rakko did not write.
func writeHermesPlugin(home, dataDir, exe string) error {
	dir := hermesPluginDir(home)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for _, f := range []struct {
		path string
		body string
	}{
		{hermesManifestPath(home), hermesManifest},
		{hermesInitPath(home), renderModule(hermesPlugin, ToolHermes, exe, dataDir)},
	} {
		path := f.path
		if err := updateFile(path, func(raw []byte, existed bool, _ os.FileMode) (filePlan, error) {
			if existed && !ownsModule(raw, ToolHermes) {
				return filePlan{}, fmt.Errorf("agentnotify: %s exists and was not written by Rakko; left alone", path)
			}
			if existed && string(raw) == f.body {
				return filePlan{}, nil
			}
			return filePlan{data: []byte(f.body), mode: 0o644, changed: true}, nil
		}); err != nil {
			return err
		}
	}
	return nil
}

// removeHermesPlugin deletes the files Rakko wrote and the directory once it
// holds nothing else. A plugin source Rakko did not write is left alone.
func removeHermesPlugin(home string) {
	dir := hermesPluginDir(home)
	if raw, err := os.ReadFile(hermesInitPath(home)); err == nil && !ownsModule(raw, ToolHermes) {
		return
	}
	for _, name := range []string{"__pycache__"} {
		_ = os.RemoveAll(filepath.Join(dir, name))
	}
	for _, path := range []string{hermesStatePath(home), hermesInitPath(home), hermesManifestPath(home)} {
		_ = os.Remove(path)
	}
	_ = os.Remove(dir)
	_ = os.Remove(filepath.Dir(dir))
}

// precheckHermesConfig runs both edits against the current bytes before
// anything is written, so a config Rakko cannot safely extend is reported
// without a half-installed plugin.
func precheckHermesConfig(home string, approval bool) error {
	cfg := hermesConfigPath(home)
	raw, existed, _, err := readConfig(cfg)
	if err != nil {
		return err
	}
	if !existed {
		return errors.New("agentnotify: " + cfg + " does not exist; Hermes is not installed")
	}
	after, _, err := hermesAddEnabled(raw, hermesPluginName)
	if err != nil {
		return fmt.Errorf("agentnotify: %s: %w", cfg, err)
	}
	if approval {
		if _, _, err := hermesSetTransport(after); err != nil {
			return fmt.Errorf("agentnotify: %s: %w", cfg, err)
		}
	}
	return nil
}

// connectHermes installs the completion half.
func connectHermes(s spec, home, dataDir, exe string) error {
	if err := precheckHermesConfig(home, false); err != nil {
		return err
	}
	if err := writeHermesPlugin(home, dataDir, exe); err != nil {
		return err
	}
	st := readHermesState(home)
	st.Completion = true
	if err := writeHermesState(home, st); err != nil {
		return err
	}
	if err := editHermesConfig(home, func(raw []byte) ([]byte, bool, error) {
		return hermesAddEnabled(raw, hermesPluginName)
	}); err != nil {
		return err
	}
	nudgeHermesReload(home)
	return nil
}

// disconnectHermes removes the completion half. With approval still on, the
// plugin stays and only its state changes.
func disconnectHermes(s spec, home, dataDir string) error {
	st := readHermesState(home)
	st.Completion = false
	if exists(hermesInitPath(home)) {
		if err := writeHermesState(home, st); err != nil {
			return err
		}
	}
	if st.Approval {
		return nil
	}
	if err := editHermesConfig(home, func(raw []byte) ([]byte, bool, error) {
		raw, changed, err := hermesRemoveTransport(raw)
		if err != nil {
			return nil, false, err
		}
		after, again, err := hermesRemoveEnabled(raw, hermesPluginName)
		return after, changed || again, err
	}); err != nil {
		return err
	}
	removeHermesPlugin(home)
	nudgeHermesReload(home)
	return nil
}

// connectHermesApprove installs the approval half.
func connectHermesApprove(s spec, home, dataDir, exe string) error {
	if err := precheckHermesConfig(home, true); err != nil {
		return err
	}
	if err := writeHermesPlugin(home, dataDir, exe); err != nil {
		return err
	}
	st := readHermesState(home)
	st.Approval = true
	if err := writeHermesState(home, st); err != nil {
		return err
	}
	if err := editHermesConfig(home, func(raw []byte) ([]byte, bool, error) {
		after, changed, err := hermesAddEnabled(raw, hermesPluginName)
		if err != nil {
			return nil, false, err
		}
		after, again, err := hermesSetTransport(after)
		return after, changed || again, err
	}); err != nil {
		return err
	}
	nudgeHermesReload(home)
	return nil
}

// disconnectHermesApprove removes the approval half. With completion still
// on, the plugin stays and only its state changes.
func disconnectHermesApprove(s spec, home, dataDir string) error {
	st := readHermesState(home)
	st.Approval = false
	if exists(hermesInitPath(home)) {
		if err := writeHermesState(home, st); err != nil {
			return err
		}
	}
	if err := editHermesConfig(home, func(raw []byte) ([]byte, bool, error) {
		return hermesRemoveTransport(raw)
	}); err != nil {
		return err
	}
	if !st.Completion {
		if err := editHermesConfig(home, func(raw []byte) ([]byte, bool, error) {
			return hermesRemoveEnabled(raw, hermesPluginName)
		}); err != nil {
			return err
		}
		removeHermesPlugin(home)
	}
	nudgeHermesReload(home)
	return nil
}

// statusHermes reports whether the completion half is installed.
func statusHermes(home string) (State, string) {
	raw, existed, _, err := readConfig(hermesInitPath(home))
	if err != nil {
		return StateError, err.Error()
	}
	if !existed {
		return StateNotInjected, ""
	}
	if !ownsModule(raw, ToolHermes) {
		return StateError, hermesInitPath(home) + " was not written by Rakko"
	}
	if !readHermesState(home).Completion {
		return StateNotInjected, ""
	}
	cfg, present, _, err := readConfig(hermesConfigPath(home))
	if err != nil {
		return StateError, err.Error()
	}
	if !present {
		return StateError, hermesConfigPath(home) + " is missing"
	}
	ok, err := hermesHasEnabled(cfg, hermesPluginName)
	if err != nil {
		return StateError, err.Error()
	}
	if !ok {
		return StateNotInjected, ""
	}
	return StateInjected, ""
}

// statusHermesApprove reports whether the approval half is installed.
func statusHermesApprove(home string) (State, string) {
	s, _ := specFor(ToolHermes)
	if s.approve == nil {
		return StateUnsupported, ""
	}
	raw, existed, _, err := readConfig(hermesInitPath(home))
	if err != nil {
		return StateError, err.Error()
	}
	if !existed {
		return StateNotInjected, ""
	}
	if !ownsModule(raw, ToolHermes) {
		return StateError, hermesInitPath(home) + " was not written by Rakko"
	}
	if !readHermesState(home).Approval {
		return StateNotInjected, ""
	}
	cfg, present, _, err := readConfig(hermesConfigPath(home))
	if err != nil {
		return StateError, err.Error()
	}
	if !present {
		return StateError, hermesConfigPath(home) + " is missing"
	}
	ok, err := hermesHasEnabled(cfg, hermesPluginName)
	if err != nil {
		return StateError, err.Error()
	}
	if !ok {
		return StateNotInjected, ""
	}
	ok, err = hermesHasTransport(cfg)
	if err != nil {
		return StateError, err.Error()
	}
	if !ok {
		return StateNotInjected, ""
	}
	return StateInjected, ""
}

// editHermesConfig applies one textual edit to config.yaml under updateFile's
// conflict rule. A file another program changed between the read and the
// write is re-read and the edit recomputed.
func editHermesConfig(home string, edit func(raw []byte) ([]byte, bool, error)) error {
	path := hermesConfigPath(home)
	return updateFile(path, func(raw []byte, existed bool, mode os.FileMode) (filePlan, error) {
		if !existed {
			return filePlan{}, nil
		}
		after, changed, err := edit(raw)
		if err != nil {
			return filePlan{}, fmt.Errorf("agentnotify: %s: %w", path, err)
		}
		if !changed {
			return filePlan{}, nil
		}
		return filePlan{data: after, mode: mode.Perm(), changed: true}, nil
	})
}

// nudgeHermesReload asks a running gateway to rediscover plugins, so a switch
// turned on takes effect in the next CLI session without restarting the
// user's gateway. Every failure is silent: the config is already saved.
func nudgeHermesReload(home string) {
	dir := hermesHome(home)
	socket := join(dir, "gateway.sock")
	if !exists(socket) {
		if pointer, err := os.ReadFile(join(dir, "gateway.sock.path")); err == nil {
			if p := strings.TrimSpace(string(pointer)); p != "" {
				socket = p
			}
		}
	}
	conn, err := net.DialTimeout("unix", socket, hermesReloadTimeout)
	if err != nil {
		return
	}
	defer conn.Close()
	req := `{"verb":"reload-plugins","id":1,"protocol":1,"params":{"home":` + quoteJSON(dir) + "}}\n"
	_ = conn.SetWriteDeadline(time.Now().Add(hermesReloadTimeout))
	if _, err := conn.Write([]byte(req)); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(hermesReloadTimeout))
	buf := make([]byte, 512)
	_, _ = conn.Read(buf)
}

// --- config.yaml, edited as text ---

// yamlLines keeps every line's own newline so an untouched file can be
// written back byte for byte.
type yamlLines struct {
	lines []string
}

func splitYAML(raw []byte) yamlLines {
	if len(raw) == 0 {
		return yamlLines{}
	}
	parts := strings.SplitAfter(string(raw), "\n")
	if parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return yamlLines{lines: parts}
}

func (d yamlLines) bytes() []byte { return []byte(strings.Join(d.lines, "")) }

// bare returns the line without its newline.
func yamlBare(line string) string {
	line = strings.TrimSuffix(line, "\n")
	return strings.TrimSuffix(line, "\r")
}

func yamlIndent(line string) int {
	bare := yamlBare(line)
	n := 0
	for n < len(bare) && bare[n] == ' ' {
		n++
	}
	if n < len(bare) && bare[n] == '\t' {
		return -1
	}
	return n
}

// yamlKey returns "key: value" split at the first colon for an unquoted key.
func yamlKey(line string) (key, value string, ok bool) {
	bare := strings.TrimLeft(yamlBare(line), " ")
	bare = strings.TrimSuffix(bare, "\r")
	if bare == "" || strings.HasPrefix(bare, "#") || strings.HasPrefix(bare, "-") {
		return "", "", false
	}
	i := strings.IndexByte(bare, ':')
	if i <= 0 {
		return "", "", false
	}
	key = bare[:i]
	for _, r := range key {
		if !(r == '_' || r == '-' || r == '.' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return "", "", false
		}
	}
	return key, strings.TrimSpace(bare[i+1:]), true
}

func yamlIsTopKey(line string) bool {
	return yamlIndent(line) == 0 && !strings.HasPrefix(strings.TrimSpace(yamlBare(line)), "#") &&
		yamlKeyOK(line)
}

func yamlKeyOK(line string) bool {
	_, _, ok := yamlKey(line)
	return ok
}

// yamlTopBlock returns [start, end) for a top-level key.
func yamlTopBlock(d yamlLines, key string) (int, int, bool) {
	for i := range d.lines {
		if yamlIndent(d.lines[i]) != 0 {
			continue
		}
		k, _, ok := yamlKey(d.lines[i])
		if ok && k == key {
			end := len(d.lines)
			for j := i + 1; j < len(d.lines); j++ {
				if yamlIsTopKey(d.lines[j]) {
					end = j
					break
				}
			}
			return i, end, true
		}
	}
	return 0, 0, false
}

// yamlChild finds a direct child key of the block that starts at parent.
func yamlChild(d yamlLines, start, end int, key string) (int, int, bool, error) {
	childIndent := -1
	for i := start + 1; i < end; i++ {
		bare := strings.TrimSpace(yamlBare(d.lines[i]))
		if bare == "" || strings.HasPrefix(bare, "#") {
			continue
		}
		ind := yamlIndent(d.lines[i])
		if ind < 0 {
			return 0, 0, false, errors.New("tab indentation is not supported")
		}
		if childIndent < 0 {
			childIndent = ind
		}
		if ind != childIndent {
			continue
		}
		k, _, ok := yamlKey(d.lines[i])
		if ok && k == key {
			return i, childIndent, true, nil
		}
	}
	return 0, childIndent, false, nil
}

// yamlBlockEnd returns where a nested block ends: the first following line
// whose indent is not greater than the key's.
func yamlBlockEnd(d yamlLines, key int) int {
	ind := yamlIndent(d.lines[key])
	for j := key + 1; j < len(d.lines); j++ {
		bare := strings.TrimSpace(yamlBare(d.lines[j]))
		if bare == "" {
			continue
		}
		lineInd := yamlIndent(d.lines[j])
		if lineInd < 0 {
			return j
		}
		if lineInd <= ind {
			return j
		}
	}
	return len(d.lines)
}

// yamlInsertAt puts one line at index at, keeping the preceding line's
// newline intact.
func yamlInsertAt(d *yamlLines, at int, line string) {
	if at > 0 && !strings.HasSuffix(d.lines[at-1], "\n") {
		d.lines[at-1] += "\n"
	}
	d.lines = append(d.lines, "")
	copy(d.lines[at+1:], d.lines[at:])
	d.lines[at] = line
}

// yamlAppendLine adds a line at the end of the document.
func yamlAppendLine(d *yamlLines, line string) {
	if len(d.lines) > 0 && !strings.HasSuffix(d.lines[len(d.lines)-1], "\n") {
		d.lines[len(d.lines)-1] += "\n"
	}
	d.lines = append(d.lines, line)
}

// yamlRemove deletes one line.
func yamlRemove(d *yamlLines, at int) {
	d.lines = append(d.lines[:at], d.lines[at+1:]...)
}

// yamlBlockEmpty reports whether a nested block has no child lines except
// blank lines and comments.
func yamlBlockEmpty(d yamlLines, key int) bool {
	ind := yamlIndent(d.lines[key])
	comment := false
	for j := key + 1; j < len(d.lines); j++ {
		bare := strings.TrimSpace(yamlBare(d.lines[j]))
		if bare == "" {
			continue
		}
		lineInd := yamlIndent(d.lines[j])
		if strings.HasPrefix(bare, "#") {
			if lineInd > ind {
				comment = true
			}
			continue
		}
		if lineInd < 0 || lineInd <= ind {
			return !comment
		}
		return false
	}
	return !comment
}

// yamlListItems returns the item lines of a block list key.
func yamlListItems(d yamlLines, key int) []int {
	ind := yamlIndent(d.lines[key])
	var out []int
	for j := key + 1; j < len(d.lines); j++ {
		bare := strings.TrimSpace(yamlBare(d.lines[j]))
		if bare == "" || strings.HasPrefix(bare, "#") {
			continue
		}
		lineInd := yamlIndent(d.lines[j])
		if lineInd < 0 || lineInd <= ind || !strings.HasPrefix(bare, "-") {
			break
		}
		out = append(out, j)
	}
	return out
}

// hermesHasEnabled reports whether plugins.enabled lists name.
func hermesHasEnabled(raw []byte, name string) (bool, error) {
	d := splitYAML(raw)
	start, end, ok := yamlTopBlock(d, "plugins")
	if !ok {
		return false, nil
	}
	enabled, _, ok, err := yamlChild(d, start, end, "enabled")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	_, value, _ := yamlKey(d.lines[enabled])
	if value != "" && !strings.HasPrefix(value, "#") {
		return false, errors.New("plugins.enabled is not a block list; left alone")
	}
	for _, item := range yamlListItems(d, enabled) {
		text := strings.TrimSpace(yamlBare(d.lines[item]))
		if len(text) > 1 && strings.TrimSpace(text[1:]) == name {
			return true, nil
		}
	}
	return false, nil
}

// hermesAddEnabled appends name to plugins.enabled. A block list is extended
// in place; a missing plugins/enabled key is created.
func hermesAddEnabled(raw []byte, name string) ([]byte, bool, error) {
	d := splitYAML(raw)
	start, end, ok := yamlTopBlock(d, "plugins")
	if !ok {
		yamlAppendLine(&d, "plugins:\n")
		yamlAppendLine(&d, "  enabled:\n")
		yamlAppendLine(&d, "    - "+name+"\n")
		return d.bytes(), true, nil
	}
	if _, value, _ := yamlKey(d.lines[start]); value != "" && !strings.HasPrefix(value, "#") {
		return nil, false, errors.New("plugins is not a block mapping; left alone")
	}
	enabled, ind, ok, err := yamlChild(d, start, end, "enabled")
	if err != nil {
		return nil, false, err
	}
	if !ok {
		ind = 2
		if child, _, found, err := yamlChild(d, start, end, "disabled"); err == nil && found {
			ind = yamlIndent(d.lines[child])
		}
		yamlInsertAt(&d, start+1, "  enabled:\n")
		yamlInsertAt(&d, start+2, strings.Repeat(" ", ind)+"- "+name+"\n")
		return d.bytes(), true, nil
	}
	_, value, _ := yamlKey(d.lines[enabled])
	if value != "" && !strings.HasPrefix(value, "#") {
		return nil, false, errors.New("plugins.enabled is not a block list; left alone")
	}
	items := yamlListItems(d, enabled)
	itemIndent := ind + 2
	for _, item := range items {
		text := strings.TrimSpace(yamlBare(d.lines[item]))
		if len(text) > 1 && strings.TrimSpace(text[1:]) == name {
			return raw, false, nil
		}
		itemIndent = yamlIndent(d.lines[item])
	}
	at := enabled + 1
	if len(items) > 0 {
		at = items[len(items)-1] + 1
	}
	yamlInsertAt(&d, at, strings.Repeat(" ", itemIndent)+"- "+name+"\n")
	return d.bytes(), true, nil
}

// hermesRemoveEnabled removes name from plugins.enabled, plus a container
// Rakko left empty.
func hermesRemoveEnabled(raw []byte, name string) ([]byte, bool, error) {
	d := splitYAML(raw)
	start, end, ok := yamlTopBlock(d, "plugins")
	if !ok {
		return raw, false, nil
	}
	enabled, _, ok, err := yamlChild(d, start, end, "enabled")
	if err != nil || !ok {
		return raw, false, err
	}
	removed := false
	items := yamlListItems(d, enabled)
	for i := len(items) - 1; i >= 0; i-- {
		text := strings.TrimSpace(yamlBare(d.lines[items[i]]))
		if len(text) > 1 && strings.TrimSpace(text[1:]) == name {
			yamlRemove(&d, items[i])
			removed = true
		}
	}
	if !removed {
		return raw, false, nil
	}
	// Re-find: the indexes moved.
	if start, end, ok = yamlTopBlock(d, "plugins"); !ok {
		return d.bytes(), true, nil
	}
	if enabled, _, ok, _ = yamlChild(d, start, end, "enabled"); ok && yamlBlockEmpty(d, enabled) {
		yamlRemove(&d, enabled)
		if start, end, ok = yamlTopBlock(d, "plugins"); ok && yamlBlockEmpty(d, start) {
			yamlRemove(&d, start)
		}
	}
	return d.bytes(), true, nil
}

// hermesTransportKeys are the two keys Rakko writes under
// security.approval.
func hermesTransportKeys() [][2]string {
	return [][2]string{
		{"transport", hermesTransport},
		{"transport_fallback", "builtin"},
	}
}

// hermesHasTransport reports whether security.approval selects Rakko's
// transport with the builtin fallback.
func hermesHasTransport(raw []byte) (bool, error) {
	d := splitYAML(raw)
	start, end, ok := yamlTopBlock(d, "security")
	if !ok {
		return false, nil
	}
	approval, _, ok, err := yamlChild(d, start, end, "approval")
	if err != nil || !ok {
		return false, err
	}
	blockEnd := yamlBlockEnd(d, approval)
	for _, pair := range hermesTransportKeys() {
		key, _, ok, err := yamlChild(d, approval, blockEnd, pair[0])
		if err != nil {
			return false, err
		}
		if !ok {
			return false, nil
		}
		_, value, _ := yamlKey(d.lines[key])
		if strings.TrimSpace(value) != pair[1] {
			return false, nil
		}
	}
	return true, nil
}

// hermesSetTransport writes the two selection keys. A security block that
// already carries a different choice is left alone and reported.
func hermesSetTransport(raw []byte) ([]byte, bool, error) {
	d := splitYAML(raw)
	start, end, ok := yamlTopBlock(d, "security")
	if !ok {
		for _, line := range []string{
			"security:\n",
			"  approval:\n",
			"    transport: " + hermesTransport + "\n",
			"    transport_fallback: builtin\n",
		} {
			yamlAppendLine(&d, line)
		}
		return d.bytes(), true, nil
	}
	if _, value, _ := yamlKey(d.lines[start]); value != "" && !strings.HasPrefix(value, "#") {
		return nil, false, errors.New("security is not a block mapping; left alone")
	}
	approval, approvalIndent, ok, err := yamlChild(d, start, end, "approval")
	if err != nil {
		return nil, false, err
	}
	if !ok {
		childIndent := 2
		for i := start + 1; i < end; i++ {
			bare := strings.TrimSpace(yamlBare(d.lines[i]))
			if bare == "" || strings.HasPrefix(bare, "#") {
				continue
			}
			childIndent = yamlIndent(d.lines[i])
			break
		}
		blockEnd := yamlBlockEnd(d, start)
		at := blockEnd
		yamlInsertAt(&d, at, strings.Repeat(" ", childIndent)+"approval:\n")
		for i, line := range []string{
			"transport: " + hermesTransport + "\n",
			"transport_fallback: builtin\n",
		} {
			yamlInsertAt(&d, at+1+i, strings.Repeat(" ", childIndent+2)+line)
		}
		return d.bytes(), true, nil
	}
	if _, value, _ := yamlKey(d.lines[approval]); value != "" && !strings.HasPrefix(value, "#") {
		return nil, false, errors.New("security.approval is not a block mapping; left alone")
	}
	blockEnd := yamlBlockEnd(d, approval)
	changed := false
	for _, pair := range hermesTransportKeys() {
		key, keyIndent, ok, err := yamlChild(d, approval, blockEnd, pair[0])
		if err != nil {
			return nil, false, err
		}
		if ok {
			_, value, _ := yamlKey(d.lines[key])
			if strings.TrimSpace(value) != pair[1] {
				return nil, false, fmt.Errorf("security.approval.%s is already set to %q; left alone", pair[0], strings.TrimSpace(value))
			}
			continue
		}
		if keyIndent < 0 {
			keyIndent = approvalIndent + 2
		}
		yamlInsertAt(&d, blockEnd, strings.Repeat(" ", keyIndent)+pair[0]+": "+pair[1]+"\n")
		blockEnd++
		changed = true
	}
	if !changed {
		return raw, false, nil
	}
	return d.bytes(), true, nil
}

// hermesRemoveTransport removes only the two keys Rakko wrote, and the
// containers it left empty. A value another program changed is reported,
// not overwritten.
func hermesRemoveTransport(raw []byte) ([]byte, bool, error) {
	d := splitYAML(raw)
	start, end, ok := yamlTopBlock(d, "security")
	if !ok {
		return raw, false, nil
	}
	approval, _, ok, err := yamlChild(d, start, end, "approval")
	if err != nil || !ok {
		return raw, false, err
	}
	blockEnd := yamlBlockEnd(d, approval)
	removed := false
	for _, pair := range hermesTransportKeys() {
		key, _, ok, err := yamlChild(d, approval, blockEnd, pair[0])
		if err != nil {
			return nil, false, err
		}
		if !ok {
			continue
		}
		_, value, _ := yamlKey(d.lines[key])
		if strings.TrimSpace(value) != pair[1] {
			return nil, false, fmt.Errorf("security.approval.%s was changed to %q; left alone", pair[0], strings.TrimSpace(value))
		}
		yamlRemove(&d, key)
		removed = true
		if key < approval {
			approval--
		}
		blockEnd--
	}
	if !removed {
		return raw, false, nil
	}
	if start, end, ok = yamlTopBlock(d, "security"); !ok {
		return d.bytes(), true, nil
	}
	if approval, _, ok, _ = yamlChild(d, start, end, "approval"); ok && yamlBlockEmpty(d, approval) {
		yamlRemove(&d, approval)
		if start, end, ok = yamlTopBlock(d, "security"); ok && yamlBlockEmpty(d, start) {
			yamlRemove(&d, start)
			_ = start
		}
	}
	return d.bytes(), true, nil
}
