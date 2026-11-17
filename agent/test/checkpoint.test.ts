import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { fileURLToPath } from "node:url";
import { Checkpointer, checkpointRef, parseClaudeSessionRef } from "../src/checkpoint.ts";
import { resumeOptions, SessionPoint } from "../src/claude.ts";
import { commitAll } from "../src/git.ts";
import { parseCommand, type EventBody } from "../src/protocol.ts";

const MAIN = fileURLToPath(new URL("../src/main.ts", import.meta.url));

function repo(): { dir: string; git: (...a: string[]) => string } {
  const dir = mkdtempSync(join(tmpdir(), "jm-checkpoint-"));
  const git = (...a: string[]) => execFileSync("git", a, { cwd: dir, encoding: "utf8" }).trim();
  git("init", "-q", "-b", "agent/1-x");
  writeFileSync(join(dir, "README.md"), "# test\n");
  git("add", "-A");
  git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init");
  return { dir, git };
}

function checkpointer(dir: string) {
  const events: EventBody[] = [];
  const cp = new Checkpointer({ workspace: dir, runId: "9", author: { name: "bot", email: "bot@x" }, emit: (e) => events.push(e) });
  return { cp, events };
}

test("a step that changed files is checkpointed to a hidden ref; HEAD, index and branch stay put", () => {
  const { dir, git } = repo();
  const head = git("rev-parse", "HEAD");
  const { cp, events } = checkpointer(dir);

  cp.observe({ type: "tool_result", tool: "Read", ok: true });
  assert.equal(events.length, 0, "no change, no checkpoint");

  writeFileSync(join(dir, "a.txt"), "a\n");
  writeFileSync(join(dir, "README.md"), "# changed\n");
  cp.observe({ type: "tool_result", tool: "Write", ok: true });
  assert.equal(events.length, 1);
  const ev = events[0] as Extract<EventBody, { type: "checkpoint" }>;
  assert.equal(ev.n, 1);
  assert.equal(ev.turn, 2);
  assert.equal(ev.files_changed, 2);
  assert.equal(ev.reason, "turn");
  assert.equal(git("rev-parse", checkpointRef("9", 1)), ev.ref_sha);
  assert.equal(git("rev-parse", `${ev.ref_sha}^`), head, "parent is HEAD");
  assert.equal(git("rev-parse", "HEAD"), head, "HEAD did not move");
  assert.equal(git("diff", "--cached", "--name-only"), "", "the real index is untouched");
  assert.match(git("status", "--porcelain"), /\?\? a\.txt/);

  // Same tree again: nothing; a question forces one (once per step).
  cp.observe({ type: "tool_result", tool: "Read", ok: true });
  assert.equal(events.length, 1);
  cp.before({ type: "question", question_id: "q1", text: "?" });
  cp.before({ type: "question", question_id: "q2", text: "?" });
  assert.equal(events.length, 2);
  assert.equal((events[1] as { reason: string }).reason, "question");
  assert.equal((events[1] as { files_changed: number }).files_changed, 0);

  // The final commit does not carry checkpoint commits in its history.
  commitAll(dir, "final", "bot", "bot@x");
  assert.throws(() => git("merge-base", "--is-ancestor", ev.ref_sha, "HEAD"));
  assert.equal(git("rev-list", "--count", "HEAD"), "2");
});

test("excluded paths (.fleetpocket/) never enter a checkpoint", () => {
  const { dir, git } = repo();
  execFileSync("mkdir", ["-p", join(dir, ".fleetpocket")]);
  writeFileSync(join(dir, ".git/info/exclude"), ".fleetpocket/\n");
  writeFileSync(join(dir, ".fleetpocket/events.jsonl"), "{}\n");
  writeFileSync(join(dir, "b.txt"), "b\n");
  const { cp, events } = checkpointer(dir);
  cp.observe({ type: "tool_result", tool: "Write", ok: true });
  const sha = (events[0] as { ref_sha: string }).ref_sha;
  assert.equal(git("ls-tree", "-r", "--name-only", sha), "README.md\nb.txt");
});

test("resumeOptions: a session_ref forks at its message, else plain resume", () => {
  assert.deepEqual(resumeOptions({ resume_session_ref: "s1/u9" }), { resume: "s1", resumeSessionAt: "u9", forkSession: true });
  assert.deepEqual(resumeOptions({ resume_session_id: "s2", resume_session_ref: "s1/u9" }), { resume: "s1", resumeSessionAt: "u9", forkSession: true });
  assert.deepEqual(resumeOptions({ resume_session_id: "s2" }), { resume: "s2" });
  assert.deepEqual(resumeOptions({}), {});
  assert.equal(parseClaudeSessionRef("nope"), null);
});

test("SessionPoint keeps the last entry that leaves no tool call open", () => {
  const p = new SessionPoint();
  p.track({ type: "system", session_id: "s" });
  p.track({ type: "assistant", uuid: "a1", message: { content: [{ type: "text", text: "hi" }] } });
  assert.equal(p.ref(), "s/a1");
  p.track({ type: "assistant", uuid: "a2", message: { content: [{ type: "tool_use", name: "AskUserQuestion" }] } });
  assert.equal(p.ref(), "s/a1", "a pending tool call is not a fork point");
  p.track({ type: "user", uuid: "u3" });
  assert.equal(p.ref(), "s/u3");
});

test("run command: checkpoints and resume_session_ref are optional", () => {
  const base = {
    type: "run",
    task_id: 1,
    prompt: "p",
    limits: { max_turns: 1, max_budget_usd: 1, max_duration_sec: 1, question_timeout_sec: 1 },
    commit: { message: "m", author_name: "a", author_email: "a@b" },
  };
  const r = parseCommand(JSON.stringify({ ...base, checkpoints: { run_id: 12 }, resume_session_ref: "s/u" }));
  assert.ok(r.ok);
  if (r.ok && r.command.type === "run") {
    assert.equal(r.command.checkpoints?.run_id, "12");
    assert.equal(r.command.resume_session_ref, "s/u");
  }
  const plain = parseCommand(JSON.stringify(base));
  assert.ok(plain.ok && plain.command.type === "run" && plain.command.checkpoints === undefined);
});

function start(workspace: string) {
  const child = spawn(process.execPath, ["--experimental-strip-types", "--no-warnings", MAIN], {
    env: { ...process.env, FLEETPOCKET_RUNNER_FAKE: "1", FLEETPOCKET_WORKSPACE: workspace },
    stdio: ["pipe", "pipe", "inherit"],
  });
  const events: any[] = [];
  createInterface({ input: child.stdout! }).on("line", (l) => {
    const ev = JSON.parse(l);
    events.push(ev);
    if (ev.type === "question") child.stdin!.write(JSON.stringify({ type: "answer", question_id: ev.question_id, text: "hello" }) + "\n");
  });
  const exited = new Promise<number>((r) => child.on("exit", (c) => r(c ?? -1)));
  const send = (o: object) => child.stdin!.write(JSON.stringify(o) + "\n");
  return { events, send, exited };
}

const runCmd = {
  type: "run",
  task_id: 7,
  prompt: "Say hello",
  limits: { max_turns: 10, max_budget_usd: 1, max_duration_sec: 60, question_timeout_sec: 30 },
  commit: { message: "agent: say hello", author_name: "fleetpocket[bot]", author_email: "fleetpocket@example.com" },
};

test("fake run with checkpoints: one at the question, one after the write; none without the setting", async () => {
  const { dir, git } = repo();
  const r = start(dir);
  r.send({ ...runCmd, checkpoints: { run_id: 5 } });
  assert.equal(await r.exited, 0);
  const cps = r.events.filter((e) => e.type === "checkpoint");
  assert.deepEqual(cps.map((c) => [c.n, c.reason]), [[1, "question"], [2, "turn"]]);
  assert.match(cps[0].session_ref, /^fake-7-\d+\/m\d+$/);
  // The checkpoint comes right before its question.
  const qi = r.events.findIndex((e) => e.type === "question");
  assert.equal(r.events[qi - 1].type, "checkpoint");
  assert.equal(git("for-each-ref", "--format=%(refname)", "refs/jm/"), "refs/jm/checkpoints/5/1\nrefs/jm/checkpoints/5/2");
  for (const c of cps) assert.throws(() => git("merge-base", "--is-ancestor", c.ref_sha, "HEAD"));

  const { dir: dir2, git: git2 } = repo();
  const r2 = start(dir2);
  r2.send(runCmd);
  assert.equal(await r2.exited, 0);
  assert.equal(r2.events.filter((e) => e.type === "checkpoint").length, 0);
  assert.equal(git2("for-each-ref", "refs/jm/"), "");
});

test("fake run with resume_session_ref forks and echoes the fork point", async () => {
  const { dir } = repo();
  const r = start(dir);
  r.send({ ...runCmd, resume_session_ref: "fake-7-1/m4" });
  assert.equal(await r.exited, 0);
  const started = r.events.find((e) => e.type === "started");
  assert.match(started.session_id, /^fake-fork-7-/);
  assert.ok(r.events.some((e) => e.type === "assistant_text" && e.text === "Forked session fake-7-1 at m4"));
});
