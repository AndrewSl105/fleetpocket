import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkEnv, runCheck, CHECK_TIMEOUT_EXIT } from "../src/check.ts";
import { parseCommand } from "../src/protocol.ts";

const dir = () => mkdtempSync(join(tmpdir(), "fleetpocket-check-"));

test("runCheck: exit code and interleaved output", async () => {
  const ok = await runCheck("echo out; echo err >&2; exit 0", { cwd: dir(), timeoutMs: 5000 });
  assert.equal(ok.exit_code, 0);
  assert.equal(ok.timed_out, false);
  assert.match(ok.output, /out/);
  assert.match(ok.output, /err/);
  const bad = await runCheck("echo FAIL: 2 tests; exit 3", { cwd: dir(), timeoutMs: 5000 });
  assert.equal(bad.exit_code, 3);
});

test("runCheck: timeout kills the process group", async () => {
  const r = await runCheck("sleep 30 & sleep 30; echo never", { cwd: dir(), timeoutMs: 300 });
  assert.equal(r.timed_out, true);
  assert.equal(r.exit_code, CHECK_TIMEOUT_EXIT);
  assert.doesNotMatch(r.output, /never/);
  assert.ok(r.duration_ms < 5000);
});

test("runCheck: keeps the tail of long output", async () => {
  const r = await runCheck("i=0; while [ $i -lt 2000 ]; do echo line-$i; i=$((i+1)); done", { cwd: dir(), timeoutMs: 10000, maxBytes: 1024 });
  assert.ok(r.output.length <= 1100);
  assert.match(r.output, /^… \(earlier output cut\)\n/);
  assert.match(r.output, /line-1999/);
});

test("runCheck: no agent credentials in the environment", async () => {
  const env = { ...process.env, ANTHROPIC_API_KEY: "sk-secret", CLAUDE_CODE_OAUTH_TOKEN: "tok", KEEP: "yes" };
  assert.equal(checkEnv(env).ANTHROPIC_API_KEY, undefined);
  const r = await runCheck('echo "k=${ANTHROPIC_API_KEY:-none} t=${CLAUDE_CODE_OAUTH_TOKEN:-none} keep=$KEEP"', { cwd: dir(), timeoutMs: 5000, env });
  assert.match(r.output, /k=none t=none keep=yes/);
});

test("protocol: check defaults and limits", () => {
  const base = {
    type: "run",
    task_id: 1,
    prompt: "x",
    limits: { max_turns: 1, max_budget_usd: 1, max_duration_sec: 1, question_timeout_sec: 1 },
    commit: { message: "m", author_name: "a", author_email: "e", allow_empty: true },
  };
  const r = parseCommand(JSON.stringify({ ...base, check: { command: "npm test" } }));
  assert.ok(r.ok);
  if (r.ok && r.command.type === "run") {
    assert.equal(r.command.check?.timeout_sec, 600);
    assert.equal(r.command.commit.allow_empty, true);
  }
  assert.equal(parseCommand(JSON.stringify({ ...base, check: { command: "" } })).ok, false);
  assert.equal(parseCommand(JSON.stringify({ ...base, check: { command: "x", timeout_sec: 7200 } })).ok, false);
});
