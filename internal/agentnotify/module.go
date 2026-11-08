package agentnotify

// The module sources Rakko writes for tools that extend themselves with a
// JavaScript/TypeScript file (opencode's plugin directory, omp's hooks
// directory, pi's extensions directory).
//
// Each module watches its tool's own completion event, keeps the last user
// prompt in memory only, and hands Rakko the same small JSON the shell
// hooks send. The app binary's path and data directory are baked in; the
// module checks the binary exists before spawning, so a removed app leaves
// the agent unaffected. Every module carries moduleMarker, which is how a
// later disconnect knows the file is Rakko's.

import "strings"

// moduleMarker marks a module file as Rakko's own.
const moduleMarker = "rakko-agent-completion"

const sendHelper = `
function rakkoSend(event, session, cwd, prompt) {
  if (!session || !existsSync(EXE)) return;
  try {
    const args = ["--agent-hook", "--tool", TOOL];
    if (DATA) args.push("--data-dir", DATA);
    const child = spawn(EXE, args, { stdio: ["pipe", "ignore", "ignore"], detached: true });
    child.on("error", () => {});
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify({
      hook_event_name: event,
      session_id: session,
      cwd: cwd || "",
      prompt: prompt || "",
    }));
    child.unref();
  } catch (_) {}
}
`

const opencodeModule = `// ` + moduleMarker + `: written by Rakko. Removed when its switch is off.
import { existsSync } from "node:fs";
import { spawn } from "node:child_process";
import { Plugin } from "@opencode/plugin";

const EXE = __RAKKO_EXE__;
const DATA = __RAKKO_DATA__;
const TOOL = __RAKKO_TOOL__;
` + sendHelper + `
export default Plugin.define({
  id: "` + moduleMarker + `",
  async setup(ctx) {
    const prompts = new Map();
    const users = new Set();
    const controller = new AbortController();
    const run = async () => {
      try {
        for await (const event of ctx.event.subscribe({ signal: controller.signal })) {
          try {
            const type = event && event.type;
            const p = (event && event.properties) || event || {};
            const info = p.info || {};
            const part = p.part || {};
            if (type === "message.updated" && info.role === "user" && info.id) {
              users.add(info.id);
              // A new user message starts a new task: clear what the
              // previous turn left, so the first text part of this message
              // names it.
              if (info.sessionID) prompts.set(info.sessionID, "");
            }
            if (type === "message.part.updated" && part.type === "text" && part.text &&
                users.has(part.messageID)) {
              const id = part.sessionID || p.sessionID;
              if (id && !prompts.get(id)) prompts.set(id, part.text);
            }
            if (type === "session.idle") {
              const id = p.sessionID || p.sessionId;
              rakkoSend("session.idle", id, p.cwd || (ctx.location && ctx.location.directory), prompts.get(id) || "");
            }
          } catch (_) {}
        }
      } catch (_) {}
    };
    void run();
    return () => controller.abort();
  },
});
`

const extensionModule = `// ` + moduleMarker + `: written by Rakko. Removed when its switch is off.
import { existsSync } from "node:fs";
import { spawn } from "node:child_process";

const EXE = __RAKKO_EXE__;
const DATA = __RAKKO_DATA__;
const TOOL = __RAKKO_TOOL__;
` + sendHelper + `
export default function (pi) {
  let task = "";
  try {
    pi.on("before_agent_start", (event) => {
      if (event && typeof event.prompt === "string") task = event.prompt;
    });
    pi.on("agent_end", (event, ctx) => {
      // A continuation is already scheduled, and awaiting background work
      // means the session has not finished yet.
      if (event && (event.willContinue || event.awaitingAsyncWork === true)) return;
      rakkoSend("agent_end", String(process.pid), (ctx && ctx.cwd) || process.cwd(), task);
      task = "";
    });
  } catch (_) {}
}
`

// ompModule differs from extensionModule on purpose. omp runs a user-level
// hook inside task/subagent sessions too, which share the process id, so
// agent_end would announce every subagent; session_stop is omp's main-session
// stop hook and never fires for a subagent. It also waits until the session's
// own background jobs are idle, so an awaited stop is a real finish. The
// handler returns nothing: omp reads a returned decision as a request to keep
// the turn going.
const ompModule = `// ` + moduleMarker + `: written by Rakko. Removed when its switch is off.
import { existsSync } from "node:fs";
import { spawn } from "node:child_process";

const EXE = __RAKKO_EXE__;
const DATA = __RAKKO_DATA__;
const TOOL = __RAKKO_TOOL__;
` + sendHelper + `
export default function (pi) {
  let task = "";
  try {
    pi.on("before_agent_start", (event) => {
      if (event && typeof event.prompt === "string") task = event.prompt;
    });
    pi.on("session_stop", (event, ctx) => {
      rakkoSend("session_stop", String(process.pid), (ctx && ctx.cwd) || process.cwd(), task);
      task = "";
    });
  } catch (_) {}
}
`

// copilotHooks is GitHub Copilot CLI's hook file. Its payloads name no
// event, so each command says which one it is.
const copilotHooks = `{
  "version": 1,
  "hooks": {
    "userPromptSubmitted": [
      { "type": "command", "bash": __RAKKO_CMD:userPromptSubmitted__, "timeoutSec": 5 }
    ],
    "agentStop": [
      { "type": "command", "bash": __RAKKO_CMD:agentStop__, "timeoutSec": 5 }
    ]
  }
}
`

// kiroHooks is Kiro's v1 hook file; its payloads carry hook_event_name.
const kiroHooks = `{
  "version": "v1",
  "hooks": [
    {
      "name": "` + moduleMarker + `-prompt",
      "trigger": "UserPromptSubmit",
      "action": { "type": "command", "command": __RAKKO_CMD:__ },
      "timeout": 5,
      "enabled": true
    },
    {
      "name": "` + moduleMarker + `-stop",
      "trigger": "Stop",
      "action": { "type": "command", "command": __RAKKO_CMD:__ },
      "timeout": 5,
      "enabled": true
    }
  ]
}
`

// renderModule fills a module template's baked-in values. The values are
// JSON strings, so a path with quotes or backslashes stays a literal.
// __RAKKO_CMD:<event>__ becomes the hook command line for that event (an
// empty event leaves it to the payload).
func renderModule(tmpl string, tool Tool, exe, dataDir string) string {
	r := strings.NewReplacer(
		"__RAKKO_EXE__", quoteJSON(exe),
		"__RAKKO_DATA__", quoteJSON(dataDir),
		"__RAKKO_TOOL__", quoteJSON(string(tool)),
	)
	out := r.Replace(tmpl)
	const open, end = "__RAKKO_CMD:", "__"
	for {
		i := strings.Index(out, open)
		if i < 0 {
			return out
		}
		j := strings.Index(out[i+len(open):], end)
		if j < 0 {
			return out
		}
		event := out[i+len(open) : i+len(open)+j]
		out = out[:i] + quoteJSON(hookCommandFor(exe, tool, dataDir, event)) + out[i+len(open)+j+len(end):]
	}
}
