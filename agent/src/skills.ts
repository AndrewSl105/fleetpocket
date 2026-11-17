// Writes the run's skills into the workspace before the session starts and
// keeps them out of commits via .git/info/exclude (like .fleetpocket/).
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, join, normalize } from "node:path";
import type { AgentKind, Skill } from "./protocol.ts";

// Where each agent discovers project skills: Claude reads .claude/skills
// (settingSources ["project"]), the Cursor SDK reads .cursor/skills. Codex
// has none and gets the skills in its instructions (skillsInstructions).
export function skillsDir(agent: AgentKind): string | null {
  if (agent === "claude") return ".claude/skills";
  if (agent === "cursor") return ".cursor/skills";
  return null;
}

export interface WriteSkillsResult {
  written: string[];
  skipped: string[]; // slugs the repository already has; its copy wins
}

export function writeSkills(workspace: string, agent: AgentKind, skills: Skill[]): WriteSkillsResult {
  const res: WriteSkillsResult = { written: [], skipped: [] };
  const base = skillsDir(agent);
  if (!base) return res;
  const excludes: string[] = [];
  for (const s of skills) {
    const rel = `${base}/${s.slug}`;
    const dir = join(workspace, rel);
    if (existsSync(dir)) {
      res.skipped.push(s.slug);
      continue;
    }
    for (const f of s.files) {
      if (!safePath(f.path)) continue;
      const target = join(dir, f.path);
      mkdirSync(dirname(target), { recursive: true });
      writeFileSync(target, f.content);
    }
    excludes.push(`/${rel}/`);
    res.written.push(s.slug);
  }
  appendExcludes(workspace, excludes);
  return res;
}

// The server validates skills too; this keeps a bad path from escaping the folder.
function safePath(p: string): boolean {
  if (isAbsolute(p) || p.includes("\\")) return false;
  const n = normalize(p);
  return !n.split("/").some((seg) => seg === ".." || seg === ".git");
}

export function appendExcludes(workspace: string, lines: string[]): void {
  if (lines.length === 0 || !existsSync(join(workspace, ".git"))) return;
  const infoDir = join(workspace, ".git", "info");
  mkdirSync(infoDir, { recursive: true });
  const file = join(infoDir, "exclude");
  let current = existsSync(file) ? readFileSync(file, "utf8") : "";
  const have = new Set(current.split("\n").map((l) => l.trim()));
  const add = lines.filter((l) => !have.has(l));
  if (add.length === 0) return;
  if (current !== "" && !current.endsWith("\n")) current += "\n";
  writeFileSync(file, current + add.join("\n") + "\n");
}

// skillsInstructions renders skills as plain instructions for agents without
// a skills mechanism (Codex): name, description and body of each SKILL.md.
export function skillsInstructions(skills: Skill[]): string {
  const parts = skills.map((s) => {
    const md = s.files.find((f) => f.path === "SKILL.md")?.content ?? "";
    const { name, description, body } = splitFrontmatter(md);
    return `## Skill: ${name || s.slug}\n${description ? description + "\n\n" : ""}${body.trim()}`;
  });
  return parts.length ? `# Skills\nUse these skills when the task calls for them.\n\n${parts.join("\n\n")}` : "";
}

export function splitFrontmatter(md: string): { name: string; description: string; body: string } {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(md);
  if (!m) return { name: "", description: "", body: md };
  const field = (k: string) => {
    const v = new RegExp(`^${k}:\\s*(.*)$`, "m").exec(m[1]!)?.[1]?.trim() ?? "";
    return v.replace(/^["']|["']$/g, "");
  };
  return { name: field("name"), description: field("description"), body: m[2]! };
}
