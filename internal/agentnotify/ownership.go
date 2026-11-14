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
	"strings"
)

// entryCommandFields are the JSON members a hook entry can name a program or
// a command line in.
var entryCommandFields = []string{"command", "exec", "bash", "powershell", "commandWindows"}

// carriesKind reports whether one configuration document (or one hook entry)
// holds Rakko's entry of this kind for tool.
func carriesKind(raw []byte, tool Tool, kind hookKind) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return valueOwned(v, tool, kind)
}

// valueOwned walks a parsed document, judging command entries only.
func valueOwned(v any, tool Tool, kind hookKind) bool {
	switch x := v.(type) {
	case map[string]any:
		if entryOwned(x, tool, kind) {
			return true
		}
		for _, item := range x {
			if valueOwned(item, tool, kind) {
				return true
			}
		}
	case []any:
		for _, item := range x {
			if valueOwned(item, tool, kind) {
				return true
			}
		}
	}
	return false
}

// entryOwned judges one hook entry object.
func entryOwned(m map[string]any, tool Tool, kind hookKind) bool {
	str := func(key string) string {
		s, _ := m[key].(string)
		return s
	}
	flag := flagFor(kind)
	// An exec-form entry runs a program directly: that program must be
	// Rakko's, and the arguments must start with its mode flag and this
	// tool's name.
	program := str("exec")
	if program == "" {
		program = str("command")
	}
	if program != "" && isRakkoProgram(program) {
		if args := stringSlice(m["args"]); len(args) >= 3 &&
			args[0] == flag && args[1] == "--tool" && args[2] == string(tool) {
			return true
		}
	}
	shim := shimName(kind, tool)
	for _, field := range entryCommandFields {
		cmd := str(field)
		if cmd == "" {
			continue
		}
		// The marker form is what macOS and the first Windows build wrote.
		if strings.Contains(cmd, markFor(kind, tool)) {
			return true
		}
		if invokesShim(cmd, shim) {
			return true
		}
	}
	return false
}

// itemCarries is entryOwned for one node of a parsed document: a bare
// command entry, or a list item that wraps its commands in "hooks" (Claude
// Code's matcher form).
func itemCarries(doc *jsonDoc, item *jnode, tool Tool, kind hookKind) bool {
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
			if e, ok := entry.(map[string]any); ok && entryOwned(e, tool, kind) {
				return true
			}
		}
		return false
	}
	return entryOwned(m, tool, kind)
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

// invokesShim reports whether a command line runs exactly the named shim:
// the doubled quotes cmd.exe's /c uses, a PowerShell single-quoted word with
// its call operator, a plain quoted word, or a bare path. Anything else on
// the line — another program, a redirect, a second word — makes it someone
// else's command.
func invokesShim(command, shim string) bool {
	v := strings.TrimSpace(command)
	v = strings.TrimSpace(strings.TrimPrefix(v, "&"))
	if v == "" {
		return false
	}
	switch {
	case strings.HasPrefix(v, `""`) && strings.HasSuffix(v, `""`) && len(v) >= 4:
		v = v[2 : len(v)-2]
	case strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) && len(v) >= 2:
		v = v[1 : len(v)-1]
		if strings.Contains(v, `"`) {
			return false
		}
	case strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") && len(v) >= 2:
		v = v[1 : len(v)-1]
		if strings.Contains(v, "'") {
			return false
		}
	}
	if strings.ContainsAny(v, " \t\r\n&|<>;") {
		return false
	}
	return path.Base(strings.ReplaceAll(v, `\`, "/")) == shim
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
