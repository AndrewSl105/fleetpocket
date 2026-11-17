import { execFileSync } from "node:child_process";
import { readdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { ATTACHMENTS_DIR, hasAttachments } from "./attachments.ts";
import { claudeSessionRef, parseClaudeSessionRef } from "./checkpoint.ts";
import { Cancelled, QuestionTimeout } from "./control.ts";
import type { Emitter } from "./emitter.ts";
import type { RunCommand } from "./protocol.ts";
import type { Session } from "./session.ts";

export const FAKE_FILE = "FLEETPOCKET_FAKE.md";

// fakeSession is a scripted session used by tests (FLEETPOCKET_RUNNER_FAKE=1):
// started, a few tool events, one question, wait for the answer, write a file.
// The caller commits and emits done.
export const fakeSession: Session = async ({ cmd, workspace, emitter, control, checkpointer }) => {
  // A restore with a session_ref forks like Claude: a new session id, and the
  // fork point is echoed so tests can check what the runner received.
  const fork = parseClaudeSessionRef(cmd.resume_session_ref);
  const sessionId = fork ? `fake-fork-${cmd.task_id}-${Date.now()}` : (cmd.resume_session_id ?? `fake-${cmd.task_id}-${Date.now()}`);
  // Fake transcript entries: one per emitted event, like message uuids.
  let entries = 0;
  if (checkpointer) checkpointer.sessionRef = () => claudeSessionRef(sessionId, `m${entries}`);
  const emit: typeof emitter.emit = (ev) => {
    entries++;
    return emitter.emit(ev);
  };
  emit({ type: "started", session_id: sessionId, model: cmd.model ?? "fake" });
  if (fork) emit({ type: "assistant_text", text: `Forked session ${fork.sessionId} at ${fork.at}` });
  emit({ type: "assistant_text", text: `Working on: ${cmd.prompt.slice(0, 200)}` });
  emit({ type: "tool_use", tool: "Glob", summary: "Glob **/*" });
  emit({ type: "tool_result", tool: "Glob", ok: true, summary: "3 files" });
  emit({ type: "tool_use", tool: "Read", summary: "Read README.md" });
  emit({ type: "tool_result", tool: "Read", ok: true });
  if (hasAttachments()) probeAttachments(emitter);
  if (cmd.mcp) await probeMcp(cmd, emitter);
  for (const m of cmd.prompt.matchAll(/egress-probe (\S+)/g)) probeEgress(m[1] ?? "", emitter);

  const questionId = "q1";
  emit({ type: "question", question_id: questionId, text: "Which greeting should I write?", options: ["hello", "hi"] });
  // Like a real session, an interrupt ends the run as done ("Interrupted by the user.").
  const interrupted = new Promise<null>((resolve) => (control.onInterrupt = () => resolve(null)));
  let answer: string;
  try {
    const got = await Promise.race([control.waitAnswer(questionId, cmd.limits.question_timeout_sec * 1000), interrupted]);
    if (got === null) return { kind: "done", session_id: sessionId, summary: "Interrupted by the user.", cost_usd_total: 0 };
    answer = got;
  } catch (err) {
    if (err instanceof QuestionTimeout) return { kind: "paused", session_id: sessionId, question_id: questionId };
    if (err instanceof Cancelled) return { kind: "cancelled", session_id: sessionId };
    throw err;
  }

  const extra: string[] = [];
  for (let m = control.takeMessage(); m !== undefined; m = control.takeMessage()) extra.push(m);

  emit({ type: "tool_use", tool: "Write", summary: `Write ${FAKE_FILE}` });
  writeFileSync(join(workspace, FAKE_FILE), `# ${answer}\n\n${cmd.prompt}\n${extra.map((m) => `\n- ${m}`).join("")}`);
  emit({ type: "tool_result", tool: "Write", ok: true });
  emit({ type: "usage", cost_usd_total: 0.01, input_tokens: 100, output_tokens: 50, turns: 2 });
  return { kind: "done", session_id: sessionId, summary: `Wrote ${FAKE_FILE} with "${answer}".`, cost_usd_total: 0.01 };
};

// probeMcp lists each MCP server's tools through the gateway, and calls
// the tools the prompt names as "mcp-call <server> <tool> <json args>", so
// tests see the token, the gateway and its policy work from a container.
async function probeMcp(cmd: RunCommand, emitter: Emitter): Promise<void> {
  const mcp = cmd.mcp!;
  const rpc = async (url: string, method: string, params: unknown) => {
    const res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", Authorization: `Bearer ${mcp.token}` },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
    });
    const text = await res.text();
    if (!res.ok) throw new Error(`HTTP ${res.status}: ${text.slice(0, 200)}`);
    return JSON.parse(text) as { result?: { tools?: Array<{ name: string }>; isError?: boolean }; error?: { message: string } };
  };
  for (const s of mcp.servers) {
    const tool = `mcp__${s.name}__list`;
    emitter.emit({ type: "tool_use", tool, summary: `List ${s.name} tools` });
    try {
      const r = await rpc(s.url, "tools/list", {});
      const names = (r.result?.tools ?? []).map((t) => t.name);
      emitter.emit({ type: "tool_result", tool, ok: !r.error, summary: r.error ? r.error.message : `tools: ${names.join(",")}` });
    } catch (err) {
      emitter.emit({ type: "tool_result", tool, ok: false, summary: err instanceof Error ? err.message : String(err) });
    }
  }
  for (const m of cmd.prompt.matchAll(/mcp-call (\S+) (\S+) (\{[^}]*\})/g)) {
    const [, server, name, args] = m;
    const s = mcp.servers.find((x) => x.name === server);
    const tool = `mcp__${server}__${name}`;
    emitter.emit({ type: "tool_use", tool, summary: `Call ${tool}` });
    try {
      if (!s) throw new Error(`no server ${server}`);
      const r = await rpc(s.url, "tools/call", { name, arguments: JSON.parse(args ?? "{}") });
      const ok = !r.error && !r.result?.isError;
      emitter.emit({ type: "tool_result", tool, ok, summary: r.error ? r.error.message : JSON.stringify(r.result).slice(0, 300) });
    } catch (err) {
      emitter.emit({ type: "tool_result", tool, ok: false, summary: err instanceof Error ? err.message : String(err) });
    }
  }
}

// probeEgress fetches a URL with curl (which honours HTTPS_PROXY) and
// reports the HTTP status, 000 when the connection failed.
function probeEgress(url: string, emitter: Emitter): void {
  emitter.emit({ type: "tool_use", tool: "Bash", summary: `curl ${url}` });
  let code = "000";
  try {
    code = execFileSync("curl", ["-s", "-o", "/dev/null", "-m", "15", "-w", "%{http_code}", url], { encoding: "utf8" }).trim();
  } catch (err) {
    const out = (err as { stdout?: string }).stdout;
    if (out) code = out.trim();
  }
  emitter.emit({ type: "tool_result", tool: "Bash", ok: code.startsWith("2") || code.startsWith("3"), summary: `egress ${url} ${code}` });
}

// probeAttachments reads every attached image and tries to write next to
// them, so tests can check the mount is there, readable and read-only.
function probeAttachments(emitter: Emitter): void {
  const files = readdirSync(ATTACHMENTS_DIR).filter((f) => !f.startsWith(".")).sort();
  for (const f of files) {
    const path = join(ATTACHMENTS_DIR, f);
    emitter.emit({ type: "tool_use", tool: "Read", summary: `Read ${path} (${readFileSync(path).length} bytes)` });
    emitter.emit({ type: "tool_result", tool: "Read", ok: true });
  }
  const probe = join(ATTACHMENTS_DIR, ".write-probe");
  emitter.emit({ type: "tool_use", tool: "Write", summary: `Write ${probe}` });
  try {
    writeFileSync(probe, "x");
    emitter.emit({ type: "tool_result", tool: "Write", ok: true });
  } catch (err) {
    const code = (err as NodeJS.ErrnoException).code ?? "error";
    emitter.emit({ type: "tool_result", tool: "Write", ok: false, summary: `${code}: ${ATTACHMENTS_DIR} is read-only` });
  }
}
