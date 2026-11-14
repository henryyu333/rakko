package agentnotify

// Recognizing Rakko's entries in a tool's configuration.
//
// Ownership is decided on the command object itself, never on a substring
// found anywhere in the file: a hook entry is Rakko's when it runs Rakko's
// program with Rakko's flag and tool name, or when its command line names one
// of Rakko's shims — and nothing else. A user's own
// `echo agent-hook-cursor.cmd`, or a description that mentions it, is left
// alone.
//
// Entries an earlier Rakko wrote carry the marker inside their command line
// (`--agent-hook --tool <name>`); that spelling is still recognized so an
// upgrade can clean up what it wrote, but it is checked only in the members
// that actually run something.

import (
	"encoding/json"
	"path"
	"slices"
	"strings"
)

// entryCommandFields are the JSON members a hook entry can name a program or
// a command line in.
var entryCommandFields = []string{"command", "exec", "bash", "powershell", "commandWindows"}

// carriesKind reports whether one configuration document (or one hook entry)
// holds Rakko's entry of this kind for tool. dataDir is the directory whose
// shim path the entry names; the comparison there is exact.
func carriesKind(raw []byte, tool Tool, kind hookKind, dataDir string) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return valueOwned(v, tool, kind, dataDir)
}

// valueOwned walks a parsed document, judging command entries only.
func valueOwned(v any, tool Tool, kind hookKind, dataDir string) bool {
	switch x := v.(type) {
	case map[string]any:
		if entryOwned(x, tool, kind, dataDir) {
			return true
		}
		for _, item := range x {
			if valueOwned(item, tool, kind, dataDir) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if valueOwned(item, tool, kind, dataDir) {
				return true
			}
		}
	}
	return false
}

// entryOwned judges one hook entry object. An entry is Rakko's when its
// command is byte for byte one of the exact forms Rakko generates for this
// tool, kind and data directory, or — for the entries macOS and the first
// Windows build wrote — when a command field carries the legacy marker.
// Nothing is parsed or prefix-matched here: a user's own hook that happens to
// mention a shim's name is not ours.
func entryOwned(m map[string]any, tool Tool, kind hookKind, dataDir string) bool {
	str := func(key string) string {
		s, _ := m[key].(string)
		return s
	}
	for _, field := range entryCommandFields {
		cmd := str(field)
		if cmd == "" {
			continue
		}
		// Legacy: the guarded shell line carries the marker.
		if strings.Contains(cmd, markFor(kind, tool)) {
			return true
		}
		for _, want := range expectedShellCommands(dataDir, tool, kind) {
			if cmd == want {
				return true
			}
		}
	}
	// An exec-form entry runs a program directly. The program's exact path is
	// not known where cleanup happens (a development build may be cleaning up
	// after an installed one), so it must be named like Rakko's executable and
	// the arguments must equal the ones Rakko writes, in order.
	program := str("exec")
	if program == "" {
		program = str("command")
	}
	if program == "" || !isRakkoProgram(program) {
		return false
	}
	args := stringSlice(m["args"])
	if args == nil {
		return false
	}
	s, ok := specFor(tool)
	if !ok {
		return false
	}
	for _, want := range expectedArgvs(s, dataDir, kind) {
		if slices.Equal(args, want) {
			return true
		}
	}
	return false
}

// itemCarries is entryOwned for one node of a parsed document: a bare
// command entry, or a list item that wraps its commands in "hooks" (Claude
// Code's matcher form).
func itemCarries(doc *jsonDoc, item *jnode, tool Tool, kind hookKind, dataDir string) bool {
	if item == nil || item.start < 0 || item.end > len(doc.src) {
		return false
	}
	var v any
	if json.Unmarshal(doc.src[item.start:item.end], &v) != nil {
		return false
	}
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	if inner, wrapped := m["hooks"].([]any); wrapped {
		for _, entry := range inner {
			if e, ok := entry.(map[string]any); ok && entryOwned(e, tool, kind, dataDir) {
				return true
			}
		}
		return false
	}
	return entryOwned(m, tool, kind, dataDir)
}

// isRakkoProgram reports whether a program path names Rakko: the installed
// executable, the updater's name for a running one, or the bundle binary.
func isRakkoProgram(program string) bool {
	base := path.Base(strings.ReplaceAll(program, `\`, "/"))
	if strings.EqualFold(base, "Rakko.exe") {
		return true
	}
	if strings.HasPrefix(strings.ToLower(base), ".rakko.exe.old-") {
		return true
	}
	return strings.HasSuffix(program, ".app/Contents/MacOS/Rakko")
}

func stringSlice(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
