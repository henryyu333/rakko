package agentnotify

import (
	"encoding/json"
	"strings"
)

// Recognizing Rakko's entries in a tool's configuration.
//
// Matching used to be a substring search for the marker inside the raw JSON
// (`--agent-hook --tool <name>`). That still works for every entry written
// on macOS and for the shell forms on Windows, but an exec-form entry carries
// the program and the flag as separate JSON values, and a shim entry names a
// file instead. So ownership is decided on the parsed value: a string that
// holds the marker, a string that names one of Rakko's shims, or an argument
// array that holds the mode flag followed by the tool's name. Entries an
// older Rakko wrote match the first rule, which is what lets an upgrade
// clean them up.

// carriesKind reports whether one configuration value holds Rakko's entry of
// this kind for tool.
func carriesKind(v any, tool Tool, kind hookKind) bool {
	switch x := v.(type) {
	case string:
		if strings.Contains(x, markFor(kind, tool)) {
			return true
		}
		return strings.Contains(x, shimName(kind, tool))
	case []any:
		if argvCarries(x, tool, kind) {
			return true
		}
		for _, item := range x {
			if carriesKind(item, tool, kind) {
				return true
			}
		}
	case map[string]any:
		for _, item := range x {
			if carriesKind(item, tool, kind) {
				return true
			}
		}
	}
	return false
}

// argvCarries reports whether an argument list is one of Rakko's exec-form
// invocations: the mode flag, then --tool, then this tool's name.
func argvCarries(args []any, tool Tool, kind hookKind) bool {
	flag := flagFor(kind)
	for i, item := range args {
		if s, _ := item.(string); s != flag {
			continue
		}
		for j := i + 1; j < len(args) && j <= i+4; j++ {
			name, _ := args[j].(string)
			if name != "--tool" {
				continue
			}
			if j+1 < len(args) {
				value, _ := args[j+1].(string)
				if value == string(tool) {
					return true
				}
			}
			break
		}
	}
	return false
}

// carriesDocument parses one configuration document (or one hook entry) and
// reports whether it holds Rakko's entry of this kind.
func carriesDocument(raw []byte, tool Tool, kind hookKind) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return carriesKind(v, tool, kind)
}

// itemCarries is carriesDocument for one node of a parsed document.
func itemCarries(doc *jsonDoc, item *jnode, tool Tool, kind hookKind) bool {
	if item == nil || item.start < 0 || item.end > len(doc.src) {
		return false
	}
	return carriesDocument(doc.src[item.start:item.end], tool, kind)
}
