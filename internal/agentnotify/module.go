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
          if (info.sessionID && !prompts.has(info.sessionID)) prompts.set(info.sessionID, "");
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
      if (event && event.willContinue) return;
      rakkoSend("agent_end", String(process.pid), (ctx && ctx.cwd) || process.cwd(), task);
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
