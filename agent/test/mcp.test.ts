import { test } from "node:test";
import assert from "node:assert/strict";
import { CODEX_MCP_TOKEN_VAR, claudeMcp, codexMcp, cursorMcp } from "../src/mcp.ts";
import { parseCommand } from "../src/protocol.ts";

const mcp = { token: "jmg_tok", servers: [{ name: "linear", url: "https://jm.example.com/api/mcp-gateway/mcp/linear" }] };

test("no mcp block: nothing changes", () => {
  assert.equal(claudeMcp(undefined), null);
  assert.equal(codexMcp(undefined), null);
  assert.equal(cursorMcp({ token: "x", servers: [] }), null);
});

test("claude gets http servers with the bearer header and auto-allowed tools", () => {
  const c = claudeMcp(mcp)!;
  assert.deepEqual(c.mcpServers.linear, { type: "http", url: mcp.servers[0].url, headers: { Authorization: "Bearer jmg_tok" } });
  assert.deepEqual(c.allowedTools, ["mcp__linear"]);
});

test("codex reads the token from the environment, not argv", () => {
  const c = codexMcp(mcp)!;
  assert.deepEqual(c.config.linear, { url: mcp.servers[0].url, bearer_token_env_var: CODEX_MCP_TOKEN_VAR });
  assert.ok(!JSON.stringify(c.config).includes("jmg_tok"));
  assert.equal(c.env[CODEX_MCP_TOKEN_VAR], "jmg_tok");
  assert.match(CODEX_MCP_TOKEN_VAR, /TOKEN/); // hidden from commands by shell_environment_policy
});

test("cursor gets http servers", () => {
  assert.equal(cursorMcp(mcp)!.linear.headers.Authorization, "Bearer jmg_tok");
});

test("the run command accepts an optional mcp block and validates names", () => {
  const run = {
    type: "run", task_id: 1, prompt: "p",
    limits: { max_turns: 1, max_budget_usd: 1, max_duration_sec: 60, question_timeout_sec: 30 },
    commit: { message: "m", author_name: "a", author_email: "a@b" },
  };
  assert.ok(parseCommand(JSON.stringify(run)).ok);
  const r = parseCommand(JSON.stringify({ ...run, mcp }));
  assert.ok(r.ok && r.command.type === "run" && r.command.mcp?.servers[0].name === "linear");
  assert.equal(parseCommand(JSON.stringify({ ...run, mcp: { token: "t", servers: [{ name: "Bad Name", url: "https://x" }] } })).ok, false);
});
