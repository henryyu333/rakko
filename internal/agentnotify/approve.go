package agentnotify

// The hook side of approvals: the app binary run as
// `--agent-approve --tool <name>`. It reads the tool's permission payload
// from stdin, asks the running app for a decision, and prints exactly one
// JSON decision (or nothing) on stdout. Every failure falls back to the
// tool's own flow; it never assumes allow.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// approveTimeout is the seconds written into each approval hook entry:
	// the outer wall above the client's 55 s budget.
	approveTimeout = 60
	// approveBudget is how long the client waits for a card.
	approveBudget = 55 * time.Second
	// approveDial is how long the client looks around for the app before
	// falling back.
	approveDial = 300 * time.Millisecond
	// approveWrite bounds sending the request.
	approveWrite = 500 * time.Millisecond
	// approveReplyLimit bounds the app's reply line.
	approveReplyLimit = 4 << 10
	// approveDefaultReason is what a deny without a typed reason tells the
	// agent.
	approveDefaultReason = "Denied by the user in Rakko."
	// approveModuleMarker marks approval module files as Rakko's own.
	approveModuleMarker = "rakko-agent-approval"
)

// approveMarker identifies Rakko's approval entries. It is deliberately not
// a substring of the completion marker (`--agent-hook --tool …`).
func approveMarker(tool Tool) string { return "--agent-approve --tool " + string(tool) }

// ApproveCommand is the shell line written into a tool's approval entry.
// The else branch, when the tool has a fallback, is what keeps an
// uninstalled Rakko from changing the tool's behaviour.
func ApproveCommand(exe string, tool Tool, dataDir string) string {
	call := shellQuote(exe) + " " + approveMarker(tool)
	if dataDir != "" {
		call += " --data-dir " + shellQuote(dataDir)
	}
	line := "if [ -x " + shellQuote(exe) + " ]; then " + call + "; "
	if s, ok := specFor(tool); ok && s.approve != nil && s.approve.fallback != "" {
		line += "else printf '%s' " + shellQuote(s.approve.fallback) + "; "
	}
	return line + "fi"
}

// RunApproveCLI handles `--agent-approve`. handled=false means the flag was
// absent and the caller should continue starting the app.
func RunApproveCLI(args []string, stdin io.Reader, stdout io.Writer) (handled bool) {
	tool, dataDir, ok := parseApproveArgs(args)
	if !ok {
		return false
	}
	fallback := ApproveFallback(tool)
	if !tool.Valid() || !tool.Approvable() {
		io.WriteString(stdout, fallback)
		return true
	}
	payload, err := io.ReadAll(io.LimitReader(stdin, forwardReadLimit+1))
	if err != nil || len(payload) > forwardReadLimit {
		io.WriteString(stdout, fallback)
		return true
	}
	a, ok := extractAsk(tool, payload)
	if !ok {
		io.WriteString(stdout, fallback)
		return true
	}
	analysis, ok := Analyze(tool, a.ToolName, a.Input)
	if !ok || !NeedsHuman(tool, analysis) {
		io.WriteString(stdout, fallback)
		return true
	}
	if dataDir == "" {
		dataDir = os.Getenv("RAKKO_DATA_DIR")
	}
	if dataDir == "" {
		dir, derr := os.UserConfigDir()
		if derr != nil {
			io.WriteString(stdout, fallback)
			return true
		}
		dataDir = filepath.Join(dir, "Rakko")
	}
	d, ok := askRakko(context.Background(), filepath.Join(dataDir, SocketName), a)
	if !ok {
		io.WriteString(stdout, fallback)
		return true
	}
	io.WriteString(stdout, string(ApproveOutput(tool, d.Decision, d.Message)))
	return true
}

// parseApproveArgs reads the approval mode's arguments without the flag
// package, so the app can start as fast as the completion forwarder does.
func parseApproveArgs(args []string) (tool Tool, dataDir string, ok bool) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		name, value, hasValue := strings.Cut(a, "=")
		next := func() string {
			if hasValue {
				return value
			}
			if i+1 < len(args) {
				i++
				return args[i]
			}
			return ""
		}
		switch name {
		case "--agent-approve":
			ok = true
			if hasValue {
				tool = Tool(value)
			}
		case "--tool":
			tool = Tool(next())
		case "--data-dir":
			dataDir = next()
		case "--event":
			_ = next() // diagnostic only; the app does not trust it
		}
	}
	return tool, dataDir, ok
}

// extractAsk turns one tool's own payload into the normalized request the
// app receives. The inner input stays exactly the bytes the tool sent, so
// the app's analysis and the client's risk filter judge the same thing.
func extractAsk(tool Tool, payload []byte) (Ask, bool) {
	var top map[string]any
	if err := json.Unmarshal(payload, &top); err != nil {
		return Ask{}, false
	}
	a := Ask{V: askVersion, Kind: AskKind, Tool: tool, ID: newAskID()}
	a.Event = firstNonEmpty(stringOf(top, "hook_event_name", "hookEventName", "event"), tool.ApproveEvent())
	a.Session = firstNonEmpty(stringOf(top, "session_id", "sessionId", "sessionID", "conversationId", "conversation_id", "thread_id"))
	a.Cwd = firstNonEmpty(stringOf(top, "cwd", "workspaceRoot"), firstString(top["workspacePaths"]), firstString(top["workspace_roots"]))
	if a.Session == "" {
		a.Session = "pid:" + itoa(os.Getppid())
	}

	var toolName string
	var raw json.RawMessage
	switch tool {
	case ToolCopilot:
		toolName = stringOf(top, "toolName", "tool_name")
		raw = rawObject(top["toolArgs"])
	case ToolAntigravity:
		call, _ := top["toolCall"].(map[string]any)
		toolName = stringOf(call, "name")
		args := call["args"]
		if args == nil {
			// Some builds send tool_input-style fields at the top.
			args = top["tool_input"]
		}
		raw = rawObject(args)
		if a.Session == "" {
			a.Session = stringOf(top, "conversationId")
		}
	case ToolOpencode:
		// The plugin sends the evaluate event itself.
		toolName = stringOf(top, "action", "tool_name", "toolName")
		raw = mustRaw(top)
	case ToolPi:
		toolName = stringOf(top, "toolName", "tool_name", "name")
		raw = rawObject(top["input"])
		if len(raw) == 0 {
			raw = rawObject(top["tool_input"])
		}
	default:
		toolName = stringOf(top, "tool_name", "toolName", "name")
		raw = rawObject(top["tool_input"])
		if len(raw) == 0 {
			raw = rawObject(top["toolArgs"])
		}
	}
	if toolName == "" {
		toolName = string(tool)
	}
	if len(raw) == 0 || !json.Valid(raw) {
		raw = json.RawMessage("{}")
	}
	a.ToolName = toolName
	a.Input = raw
	return a, true
}

func rawObject(v any) json.RawMessage {
	switch x := v.(type) {
	case nil:
		return nil
	case json.RawMessage:
		return x
	case string:
		trimmed := strings.TrimSpace(x)
		if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
			return json.RawMessage(trimmed)
		}
		// A command string is still useful to the analyzer; wrap it as a
		// tool input field rather than losing it.
		b, _ := json.Marshal(map[string]string{"command": x})
		return b
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return nil
		}
		return b
	}
}

func mustRaw(m map[string]any) json.RawMessage {
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return b
}

func firstString(v any) string {
	if list, ok := v.([]any); ok {
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func newAskID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A clock-derived id is still unique enough for one connection; the
		// app only uses it to match a reply and to dedupe replays.
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> (8 * i))
		}
	}
	return hex.EncodeToString(b[:])
}

// askRakko dials the app, checks who is on the other end, and waits for one
// decision. ok is false on every abnormal path; the caller then falls back.
func askRakko(ctx context.Context, path string, a Ask) (Decision, bool) {
	conn, err := net.DialTimeout("unix", path, approveDial)
	if err != nil {
		return Decision{}, false
	}
	defer conn.Close()
	if !peerIsSelf(conn) {
		return Decision{}, false
	}
	body, err := json.Marshal(a)
	if err != nil {
		return Decision{}, false
	}
	_ = conn.SetWriteDeadline(time.Now().Add(approveWrite))
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return Decision{}, false
	}
	deadline := time.Now().Add(approveBudget)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	line, err := bufio.NewReader(io.LimitReader(conn, approveReplyLimit)).ReadBytes('\n')
	if err != nil && len(line) == 0 {
		return Decision{}, false
	}
	var d Decision
	if json.Unmarshal(line, &d) != nil {
		return Decision{}, false
	}
	if d.ID != a.ID {
		return Decision{}, false
	}
	switch d.Decision {
	case DecisionAllow, DecisionDeny, DecisionPass:
		return d, true
	}
	return Decision{}, false
}

// ApproveFallback is what the client prints when it cannot ask: the tool's
// own flow, unchanged. Antigravity's explicit allow delegates to its
// default policy; every other tool reads nothing.
func ApproveFallback(tool Tool) string {
	if s, ok := specFor(tool); ok && s.approve != nil {
		return s.approve.fallback
	}
	return ""
}

// ApproveOutput is the exact bytes a tool's hook reads for one decision.
// Allow and deny are the tool's own JSON shapes; pass is the fallback.
func ApproveOutput(tool Tool, decision, message string) string {
	if decision != DecisionAllow && decision != DecisionDeny {
		return ApproveFallback(tool)
	}
	msg := approveReason(message)
	allow := decision == DecisionAllow
	switch tool {
	case ToolClaude, ToolCodex, ToolQwen:
		type behavior struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message,omitempty"`
		}
		var out struct {
			HookSpecificOutput struct {
				HookEventName string   `json:"hookEventName"`
				Decision      behavior `json:"decision"`
			} `json:"hookSpecificOutput"`
		}
		out.HookSpecificOutput.HookEventName = "PermissionRequest"
		out.HookSpecificOutput.Decision.Behavior = decision
		if !allow {
			out.HookSpecificOutput.Decision.Message = msg
		}
		return marshal(out)
	case ToolDevin:
		if allow {
			return marshal(struct {
				Decision string `json:"decision"`
			}{"approve"})
		}
		return marshal(struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}{"block", msg})
	case ToolCopilot:
		if allow {
			return marshal(struct {
				Behavior string `json:"behavior"`
			}{"allow"})
		}
		return marshal(struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		}{"deny", msg})
	case ToolFactory:
		reason := "Approved in Rakko"
		if !allow {
			reason = msg
		}
		var out struct {
			HookSpecificOutput struct {
				HookEventName            string `json:"hookEventName"`
				PermissionDecision       string `json:"permissionDecision"`
				PermissionDecisionReason string `json:"permissionDecisionReason"`
			} `json:"hookSpecificOutput"`
		}
		out.HookSpecificOutput.HookEventName = "PreToolUse"
		out.HookSpecificOutput.PermissionDecision = decision
		out.HookSpecificOutput.PermissionDecisionReason = reason
		return marshal(out)
	case ToolAntigravity:
		if allow {
			return marshal(struct {
				Decision string `json:"decision"`
			}{"auto_approve"})
		}
		return marshal(struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}{"deny", msg})
	case ToolOpencode, ToolPi, ToolHermes, ToolDsh:
		if allow {
			return marshal(struct {
				Decision string `json:"decision"`
			}{"allow"})
		}
		return marshal(struct {
			Decision string `json:"decision"`
			Message  string `json:"message"`
		}{"deny", msg})
	}
	return ApproveFallback(tool)
}

func marshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

// approveReason normalizes a typed reason: one line, no surrounding space,
// at most 500 characters. Empty means the fixed English refusal.
func approveReason(message string) string {
	s := strings.Join(strings.Fields(message), " ")
	if s == "" {
		return approveDefaultReason
	}
	r := []rune(s)
	if len(r) > 500 {
		s = string(r[:500])
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// --- module sources ---

// copilotApproveHooks is Copilot CLI's permission hook file. It is separate
// from the completion file so either switch can be turned off alone.
const copilotApproveHooks = `{
  "version": 1,
  "hooks": {
    "permissionRequest": [
      { "type": "command", "bash": __RAKKO_APPROVE_CMD__, "timeoutSec": 60 }
    ]
  }
}
`

// opencodeApproveModule is opencode's V2 plugin: it spawns the same binary
// so the peer check lives in one place, and only asks when opencode itself
// was going to prompt.
const opencodeApproveModule = `// ` + approveModuleMarker + `: written by Rakko. Removed when its switch is off.
import { existsSync } from "node:fs";
import { execFile } from "node:child_process";
import { Plugin } from "@opencode/plugin";

const EXE = __RAKKO_EXE__;
const DATA = __RAKKO_DATA__;
const TOOL = __RAKKO_TOOL__;

function askRakko(payload) {
  return new Promise((resolve) => {
    if (!existsSync(EXE)) return resolve(null);
    const args = ["--agent-approve", "--tool", TOOL];
    if (DATA) args.push("--data-dir", DATA);
    const env = { ...process.env };
    if (payload.cwd) env.PWD = payload.cwd;
    const child = execFile(EXE, args,
      { timeout: 57000, maxBuffer: 1 << 20, env }, (err, out) => {
        if (err || !out) return resolve(null);
        try { resolve(JSON.parse(out)); } catch { resolve(null); }
      });
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify(payload));
  });
}

export default Plugin.define({
  id: "rakko-agent-approval",
  async setup(ctx) {
    await ctx.permission.hook("evaluate", async (event) => {
      if (event.effect !== "ask") return;
      const d = await askRakko({
        action: event.action,
        resources: event.resources || [],
        sessionID: event.sessionID,
        cwd: ctx.location && ctx.location.directory,
      });
      if (!d) return;
      if (d.decision === "allow") event.effect = "allow";
      if (d.decision === "deny") {
        event.effect = "deny";
        event.message = d.message || "Denied by the user in Rakko.";
      }
    });
  },
});
`

// piApproveModule blocks a pi tool call only when Rakko denies it. A
// missing app, a timeout or an error returns nothing, which is pi's normal
// behaviour: pi has no permission prompt of its own.
const piApproveModule = `// ` + approveModuleMarker + `: written by Rakko. Removed when its switch is off.
import { existsSync } from "node:fs";
import { execFile } from "node:child_process";

const EXE = __RAKKO_EXE__;
const DATA = __RAKKO_DATA__;
const TOOL = __RAKKO_TOOL__;

function askRakko(payload) {
  return new Promise((resolve) => {
    if (!existsSync(EXE)) return resolve(null);
    const args = ["--agent-approve", "--tool", TOOL];
    if (DATA) args.push("--data-dir", DATA);
    const child = execFile(EXE, args,
      { timeout: 57000, maxBuffer: 1 << 20 }, (err, out) => {
        if (err || !out) return resolve(null);
        try { resolve(JSON.parse(out)); } catch { resolve(null); }
      });
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify(payload));
  });
}

export default function (pi) {
  try {
    pi.on("tool_call", async (event, ctx) => {
      const d = await askRakko({
        toolName: event.toolName,
        input: event.input || {},
        cwd: (ctx && ctx.cwd) || process.cwd(),
        sessionID: String(process.pid),
      });
      if (d && d.decision === "deny") {
        return { block: true, reason: d.message || "Denied by the user in Rakko." };
      }
      return;
    });
  } catch (_) {}
}
`

// dshApproveModule answers DeepSeek Harness's approval/request waterfall.
// dsh asks only when it was going to prompt (a sandbox escalation or an
// ask decision from its own policy), and the module claims the request
// only with a real answer from Rakko: "allow" grants this one call,
// "deny" rejects it, and anything else — no app, no answer in 45 s, an
// error — calls next(), handing the request back to dsh's own channel.
// The request carries no tool arguments, so the module remembers each
// tool/call event and hands Rakko the same tool name and input shape the
// shell hooks use.
const dshApproveModule = `// ` + approveModuleMarker + `: written by Rakko. Removed when its switch is off.
import { existsSync } from "node:fs";
import { execFile } from "node:child_process";

const EXE = __RAKKO_EXE__;
const DATA = __RAKKO_DATA__;
const TOOL = __RAKKO_TOOL__;
const CALL_TTL = 10 * 60 * 1000;

function askRakko(payload, signal) {
  return new Promise((resolve) => {
    if (!existsSync(EXE)) return resolve(null);
    const args = ["--agent-approve", "--tool", TOOL];
    if (DATA) args.push("--data-dir", DATA);
    const options = { timeout: 57000, maxBuffer: 1 << 20 };
    if (signal) options.signal = signal;
    const child = execFile(EXE, args, options, (err, out) => {
      if (err || !out) return resolve(null);
      try { resolve(JSON.parse(out)); } catch { resolve(null); }
    });
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify(payload));
  });
}

export default function (ctx) {
  const calls = new Map();
  try {
    ctx.on("session/event", (session, event) => {
      try {
        const data = event && event.data;
        if (!event || event.type !== "tool/call" || !data || !data.callId) return;
        const id = String(data.callId);
        calls.set(id, data);
        const timer = setTimeout(() => calls.delete(id), CALL_TTL);
        if (timer.unref) timer.unref();
      } catch (_) {}
    }, true);
    ctx.on("approval/request", (request, next) => {
      try {
        if (!request || !request.agent) return next();
        const call = request.callId === void 0 ? void 0 : calls.get(String(request.callId));
        let input = {};
        if (call && typeof call.arguments === "string") {
          try { input = JSON.parse(call.arguments); } catch (_) {}
        }
        const session = request.agent.session || {};
        const header = session.header || {};
        return askRakko({
          hook_event_name: "approval/request",
          tool_name: request.toolName || (call && call.name) || "",
          tool_input: input,
          cwd: header.cwd || "",
          session_id: String(session.id || ""),
        }, request.signal).then((d) => {
          if (!d) return next();
          if (d.decision === "allow") return "allowed-once";
          if (d.decision === "deny") return "rejected";
          return next();
        }, () => next());
      } catch (_) {
        return next();
      }
    }, true);
  } catch (_) {}
}
`

// renderApproveModule fills an approval module's baked-in values.
func renderApproveModule(tmpl string, tool Tool, exe, dataDir string) string {
	r := strings.NewReplacer(
		"__RAKKO_EXE__", quoteJSON(exe),
		"__RAKKO_DATA__", quoteJSON(dataDir),
		"__RAKKO_TOOL__", quoteJSON(string(tool)),
		"__RAKKO_APPROVE_CMD__", quoteJSON(ApproveCommand(exe, tool, dataDir)),
	)
	return r.Replace(tmpl)
}
