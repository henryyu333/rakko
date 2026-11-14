package agentnotify

// How one hook entry is written into a tool's configuration. The two
// platforms disagree in two ways, and both are the tool's, not Rakko's:
//
//   - macOS tools run a shell line; on Windows some tools take a program and
//     an argument array instead (Claude Code, Copilot CLI, Auggie), while the
//     rest run a command line through a particular interpreter (cmd.exe,
//     PowerShell, or the Git Bash that Qwen Code picks at run time).
//   - For the shell tools Rakko writes one batch file per tool and function
//     into its data directory, and the entry only names that file, so the
//     quoting rules of each interpreter are applied in one place and the
//     tool's own spelling of "run this" cannot break the command.
//
// hookEntry is the platform-neutral result: the JSON members that name the
// program or command, and the argument array when the tool takes one.

import (
	"path/filepath"
	"strings"
)

// hookKind names the two entries Rakko writes into a tool's configuration:
// the completion forwarder and the approval client. They live in the same
// files, are marked differently, and are installed and removed separately.
type hookKind int

const (
	hookCompletion hookKind = iota
	hookApproval
)

// hookField is one JSON member of a command entry.
type hookField struct {
	name  string
	value string
}

// hookEntry is one command entry in the shape the tool accepts.
type hookEntry struct {
	// fields are the members naming the program or command line. The first
	// is the one a shell tool runs.
	fields []hookField
	// argv is the argument array of an exec-form entry; nil for a command
	// line.
	argv []string
}

// command is what the entry runs: a command line, or the program's path.
func (e hookEntry) command() string {
	if len(e.fields) == 0 {
		return ""
	}
	return e.fields[0].value
}

// members renders the entry's JSON members in order, without braces.
func (e hookEntry) members() []string {
	out := make([]string, 0, len(e.fields)+1)
	for _, f := range e.fields {
		out = append(out, quoteJSON(f.name)+": "+quoteJSON(f.value))
	}
	if e.argv != nil {
		args := make([]string, len(e.argv))
		for i, a := range e.argv {
			args[i] = quoteJSON(a)
		}
		out = append(out, `"args": [`+strings.Join(args, ", ")+`]`)
	}
	return out
}

// inline renders the entry's members on one line: the form a member written
// into an existing array uses.
func (e hookEntry) inline() string { return strings.Join(e.members(), ", ") }

// name is the word a shim's file name uses for this kind.
func (k hookKind) name() string {
	if k == hookApproval {
		return "approve"
	}
	return "hook"
}

// shimName is one shim's file name inside the data directory. A written
// entry that names this file is recognizable as Rakko's.
func shimName(kind hookKind, tool Tool) string {
	return "agent-" + kind.name() + "-" + string(tool) + ".cmd"
}

// shimPath is where one shim lives.
func shimPath(dataDir string, kind hookKind, tool Tool) string {
	return filepath.Join(dataDir, shimName(kind, tool))
}

// moduleSource is the module source Rakko writes for a tool and kind. Only
// Copilot CLI differs: its files are JSON, and Windows uses the argv form.
func moduleSource(s spec, kind hookKind) string {
	if s.tool == ToolCopilot {
		if kind == hookApproval {
			return copilotApproveHooksExec
		}
		return copilotHooksExec
	}
	if kind == hookApproval {
		return s.approve.module
	}
	return s.module
}

// hookEntryFor renders the completion entry for tool.
func hookEntryFor(exe string, tool Tool, dataDir, event string) hookEntry {
	return hookEntryOS(exe, tool, dataDir, event)
}

// approveEntryFor renders the approval entry for tool.
func approveEntryFor(exe string, tool Tool, dataDir string) hookEntry {
	return approveEntryOS(exe, tool, dataDir)
}

// flagFor is the mode flag an entry of this kind carries.
func flagFor(kind hookKind) string {
	if kind == hookApproval {
		return "--agent-approve"
	}
	return "--agent-hook"
}

// markFor is the marker an entry of this kind carries. The shell forms
// include it verbatim; the argv and shim forms are recognized structurally
// (see carriesKind).
func markFor(kind hookKind, tool Tool) string {
	if kind == hookApproval {
		return approveMarker(tool)
	}
	return marker(tool)
}

// rakkoArgs is the fixed argument list behind one entry: the mode flag, the
// tool, the event when the tool's payload does not name it, and the data
// directory when the GUI is not using the default one. A shim bakes exactly
// these in; an exec-form entry passes them as its argument array.
func rakkoArgs(kind hookKind, tool Tool, dataDir, event string) []string {
	args := []string{flagFor(kind), "--tool", string(tool)}
	if event != "" {
		args = append(args, "--event", event)
	}
	if dataDir != "" {
		args = append(args, "--data-dir", dataDir)
	}
	return args
}

// eventArgFor is the event fixed on the command line for a tool whose
// payload does not name it. Only a tool with a single event can bake it in;
// the others name the event in their payload.
func eventArgFor(s spec) string {
	if s.eventArg && len(s.events) == 1 {
		return s.events[0]
	}
	return ""
}
