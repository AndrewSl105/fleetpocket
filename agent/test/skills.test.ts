import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { writeSkills, skillsInstructions } from "../src/skills.ts";
import { commitAll } from "../src/git.ts";
import { checkTool } from "../src/policy.ts";
import { parseCommand } from "../src/protocol.ts";

const skill = (slug: string) => ({
  slug,
  files: [
    { path: "SKILL.md", content: `---\nname: ${slug}\ndescription: Draws ${slug}\n---\nDo the ${slug} thing.\n` },
    { path: "ref/notes.md", content: "notes" },
  ],
});

function repo(): string {
  const dir = mkdtempSync(join(tmpdir(), "fp-skills-"));
  const git = (...a: string[]) => execFileSync("git", a, { cwd: dir, stdio: "ignore" });
  git("init", "-q");
  writeFileSync(join(dir, "README.md"), "hi\n");
  git("add", "-A");
  git("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "init");
  return dir;
}

test("skills are written for Claude and never reach a commit", () => {
  const ws = repo();
  const res = writeSkills(ws, "claude", [skill("db-schema"), skill("infra-map")]);
  assert.deepEqual(res.written, ["db-schema", "infra-map"]);
  assert.ok(existsSync(join(ws, ".claude/skills/db-schema/SKILL.md")));
  assert.ok(existsSync(join(ws, ".claude/skills/db-schema/ref/notes.md")));
  assert.match(readFileSync(join(ws, ".git/info/exclude"), "utf8"), /\/\.claude\/skills\/db-schema\//);

  writeFileSync(join(ws, "change.txt"), "x\n");
  const c = commitAll(ws, "m", "a", "a@b");
  assert.ok(c);
  const files = execFileSync("git", ["show", "--name-only", "--format=", "HEAD"], { cwd: ws, encoding: "utf8" }).trim().split("\n");
  assert.deepEqual(files, ["change.txt"]);
});

test("the repository's own skill wins", () => {
  const ws = repo();
  mkdirSync(join(ws, ".claude/skills/db-schema"), { recursive: true });
  writeFileSync(join(ws, ".claude/skills/db-schema/SKILL.md"), "repo copy");
  const res = writeSkills(ws, "claude", [skill("db-schema")]);
  assert.deepEqual(res.skipped, ["db-schema"]);
  assert.equal(readFileSync(join(ws, ".claude/skills/db-schema/SKILL.md"), "utf8"), "repo copy");
});

test("Cursor gets .cursor/skills, Codex gets instructions", () => {
  const ws = repo();
  writeSkills(ws, "cursor", [skill("infra-map")]);
  assert.ok(existsSync(join(ws, ".cursor/skills/infra-map/SKILL.md")));
  assert.deepEqual(writeSkills(ws, "codex", [skill("x1")]).written, []);
  const text = skillsInstructions([skill("infra-map")]);
  assert.match(text, /## Skill: infra-map\nDraws infra-map/);
  assert.match(text, /Do the infra-map thing\./);
});

test("unsafe skill paths are not written", () => {
  const ws = repo();
  writeSkills(ws, "claude", [{ slug: "bad", files: [{ path: "../../escape.md", content: "x" }, { path: "/etc/x", content: "x" }] }]);
  assert.ok(!existsSync(join(ws, ".claude/escape.md")));
  assert.ok(!existsSync(join(ws, "escape.md")));
});

test("artifacts mode denies repository writes outside .fleetpocket/out", () => {
  const ws = "/workspace";
  const opts = { artifactsOnly: true };
  for (const tool of ["Write", "Edit", "MultiEdit", "NotebookEdit"]) {
    const d = checkTool(tool, { file_path: "src/main.go", notebook_path: "src/main.go" }, ws, opts);
    assert.equal(d.allow, false, tool);
    if (!d.allow) assert.match(d.reason, /\.fleetpocket\/out/);
  }
  assert.equal(checkTool("Write", { file_path: ".fleetpocket/out/er.mmd" }, ws, opts).allow, true);
  assert.equal(checkTool("Write", { file_path: "/workspace/.fleetpocket/out/a/manifest.json" }, ws, opts).allow, true);
  assert.equal(checkTool("Write", { file_path: ".fleetpocket/out/../../x" }, ws, opts).allow, false);
  assert.equal(checkTool("Write", { file_path: "src/main.go" }, ws).allow, true);
});

test("run command defaults skills to [] and output to pr", () => {
  const base = {
    type: "run",
    task_id: 1,
    prompt: "p",
    limits: { max_turns: 1, max_budget_usd: 1, max_duration_sec: 1, question_timeout_sec: 1 },
    commit: { message: "m", author_name: "a", author_email: "a@b" },
  };
  const r = parseCommand(JSON.stringify(base));
  assert.ok(r.ok && r.command.type === "run");
  if (r.ok && r.command.type === "run") {
    assert.deepEqual(r.command.skills, []);
    assert.equal(r.command.output, "pr");
  }
  const r2 = parseCommand(JSON.stringify({ ...base, output: "artifacts", skills: [skill("db-schema")] }));
  assert.ok(r2.ok && r2.command.type === "run" && r2.command.output === "artifacts" && r2.command.skills.length === 1);
  assert.equal(parseCommand(JSON.stringify({ ...base, output: "nope" })).ok, false);
});
