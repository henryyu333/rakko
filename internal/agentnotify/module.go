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

const EXE = __RAKKO_EXE__;
const DATA = __RAKKO_DATA__;
const TOOL = __RAKKO_TOOL__;
` + sendHelper + `
export const RakkoAgentCompletion = async ({ directory }) => {
  const prompts = new Map();
  const users = new Set();
  return {
    event: async ({ event }) => {
      try {
        const type = event && event.type;
        const p = (event && event.properties) || {};
        const info = p.info || {};
        const part = p.part || {};
        if (type === "message.updated" && info.role === "user" && info.id) {
          users.add(info.id);
          // A new user message starts a new task: clear what the previous
          // turn left, so the first text part of this message names it.
          if (info.sessionID) prompts.set(info.sessionID, "");
        }
        if (type === "message.part.updated" && part.type === "text" && part.text &&
            users.has(part.messageID)) {
          const id = part.sessionID || p.sessionID;
          if (id && !prompts.get(id)) prompts.set(id, part.text);
        }
        if (type === "session.idle") {
          const id = p.sessionID || p.sessionId;
          rakkoSend("session.idle", id, p.cwd || directory, prompts.get(id) || "");
        }
      } catch (_) {}
    },
  };
};
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

// renderModule fills a module template's baked-in values. The values are
// JSON strings, so a path with quotes or backslashes stays a literal.
func renderModule(tmpl string, tool Tool, exe, dataDir string) string {
	r := strings.NewReplacer(
		"__RAKKO_EXE__", quoteJSON(exe),
		"__RAKKO_DATA__", quoteJSON(dataDir),
		"__RAKKO_TOOL__", quoteJSON(string(tool)),
	)
	return r.Replace(tmpl)
}
