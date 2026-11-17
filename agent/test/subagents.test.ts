import { test } from "node:test";
import assert from "node:assert/strict";
import type { HookInput } from "@anthropic-ai/claude-agent-sdk";
import { ALLOWED_TOOLS, SUBAGENT_TOOL, policyHook, subagentOptions } from "../src/claude.ts";
import { ASK_TOOL } from "../src/mapper.ts";
import { parseCommand } from "../src/protocol.ts";

test("no subagents → no agents option", () => {
  assert.equal(subagentOptions(undefined), null);
  assert.equal(subagentOptions([]), null);
});

test("agents option keeps only pre-approved tools and never the ask tool", () => {
  const agents = subagentOptions(
    [
      { name: "explorer", description: "find code", prompt: "never edit", model: "haiku", tools: ["Read", "Grep", ASK_TOOL, SUBAGENT_TOOL, "KillShell", "mcp__linear__get_issue"] },
      { name: "reviewer", description: "review", prompt: "review the diff", tools: ["Read", "Bash"] },
    ],
    ["mcp__linear"],
  );
  assert.deepEqual(agents, {
    explorer: { description: "find code", prompt: "never edit", model: "haiku", tools: ["Read", "Grep", "mcp__linear__get_issue"], disallowedTools: [ASK_TOOL, SUBAGENT_TOOL] },
    reviewer: { description: "review", prompt: "review the diff", tools: ["Read", "Bash"], disallowedTools: [ASK_TOOL, SUBAGENT_TOOL] },
  });
  for (const a of Object.values(agents!)) for (const t of a.tools!) assert.ok(ALLOWED_TOOLS.includes(t) || t.startsWith("mcp__linear"));
});

test("the policy hook denies a subagent Write outside the workspace", async () => {
  const hook = policyHook("/workspace", {});
  const call = (tool_name: string, tool_input: unknown) =>
    hook(
      { hook_event_name: "PreToolUse", tool_name, tool_input, tool_use_id: "t", agent_id: "sub-1", agent_type: "explorer", session_id: "s", transcript_path: "", cwd: "/workspace" } as unknown as HookInput,
      "t",
      { signal: new AbortController().signal },
    );
  const denied = (await call("Write", { file_path: "/etc/passwd", content: "x" })) as { hookSpecificOutput?: { permissionDecision?: string } };
  assert.equal(denied.hookSpecificOutput?.permissionDecision, "deny");
  assert.deepEqual(await call("Write", { file_path: "/workspace/src/a.ts", content: "x" }), {});
});

test("run command validates subagents", () => {
  const base = { type: "run", task_id: 1, prompt: "p", limits: { max_turns: 1, max_budget_usd: 1, max_duration_sec: 1, question_timeout_sec: 1 }, commit: { message: "m", author_name: "a", author_email: "e" } };
  const sa = (name: string) => ({ name, description: "d", prompt: "p", tools: ["Read"] });
  assert.equal(parseCommand(JSON.stringify({ ...base, subagents: [sa("explorer")] })).ok, true);
  assert.equal(parseCommand(JSON.stringify({ ...base, subagents: [sa("Explorer")] })).ok, false);
  assert.equal(parseCommand(JSON.stringify({ ...base, subagents: ["a1", "a2", "a3", "a4", "a5", "a6"].map(sa) })).ok, false);
});
