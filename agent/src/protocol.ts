// JSON Lines protocol between the Fleetpocket server (Go) and the runner.
// Mirrored in internal/runnermgr/protocol.go; documented in docs/PROTOCOL.md.
import { z } from "zod";

export const RUNNER_VERSION = "0.1.0";

export const ExitCode = {
  Done: 0,
  Error: 1,
  Cancelled: 2,
  Paused: 3,
  LimitExceeded: 4,
} as const;

// ---- Commands (stdin) ----

export const AGENTS = ["claude", "codex", "cursor"] as const;
export type AgentKind = (typeof AGENTS)[number];

export const OUTPUTS = ["pr", "artifacts", "pr_and_artifacts"] as const;
export type Output = (typeof OUTPUTS)[number];

export const SkillFile = z.object({ path: z.string().min(1), content: z.string() });
export const Skill = z.object({
  slug: z.string().regex(/^[a-z][a-z0-9-]{1,63}$/),
  files: z.array(SkillFile),
});
export type Skill = z.infer<typeof Skill>;

// A subagent the lead Claude agent may delegate to (Claude only; Codex and
// Cursor ignore the list). tools are a subset of the runner's own tools.
export const Subagent = z.object({
  name: z.string().regex(/^[a-z][a-z0-9-]{1,31}$/),
  description: z.string().min(1),
  prompt: z.string().min(1),
  model: z.string().min(1).optional(),
  tools: z.array(z.string().min(1)),
});
export type Subagent = z.infer<typeof Subagent>;

export const RunCommand = z.object({
  type: z.literal("run"),
  task_id: z.union([z.string(), z.number()]).transform(String),
  prompt: z.string().min(1),
  append_system_prompt: z.string().optional(),
  resume_session_id: z.string().optional(),
  agent: z.enum(AGENTS).default("claude"),
  model: z.string().optional(),
  limits: z.object({
    max_turns: z.number().int().positive(),
    max_budget_usd: z.number().positive(),
    max_duration_sec: z.number().int().positive(),
    question_timeout_sec: z.number().int().positive(),
  }),
  commit: z.object({
    message: z.string().min(1),
    author_name: z.string().min(1),
    author_email: z.string().min(1),
    // Commit even when nothing changed (an empty commit), so a PR is opened
    // anyway (a schedule with "open a PR only if there are changes" off).
    allow_empty: z.boolean().optional(),
  }),
  // The run's MCP servers, all behind Jungle Monkey's gateway. The token is
  // the run's gateway token (no third-party credential): it expires when the
  // run ends and reaches only these servers.
  mcp: z
    .object({
      token: z.string().min(1),
      servers: z.array(z.object({ name: z.string().regex(/^[a-z][a-z0-9_-]{0,31}$/), url: z.string().url() })),
    })
    .optional(),
  skills: z.array(Skill).default([]),
  output: z.enum(OUTPUTS).default("pr"),
  // Present only when checkpoints are on: after each step that changed files
  // and before each question, the runner commits the working tree to
  // refs/jm/checkpoints/<run_id>/<n> and emits `checkpoint`.
  checkpoints: z.object({ run_id: z.union([z.string().min(1), z.number()]).transform(String) }).optional(),
  // A checkpoint's session_ref (restore): the agent resumes that
  // conversation at that point as a new fork (Claude only; replaces
  // resume_session_id, which the server then leaves out).
  resume_session_ref: z.string().min(1).optional(),
  // A loop iteration: after a `done` session (and its commit) the runner
  // runs this command in /workspace and emits `check` before `done`.
  check: z
    .object({
      command: z.string().min(1).max(2000),
      timeout_sec: z.number().int().positive().max(3600).default(600),
    })
    .optional(),
  // Subagents of the lead agent (≤ 5); absent or empty: none.
  subagents: z.array(Subagent).max(5).optional(),
  // A pipeline build subtask's file ownership: repository-relative globs
  // ("**" spans directories). Claude's policy denies writes elsewhere; the
  // server checks every provider's diff after the run.
  owned_paths: z.array(z.string().min(1)).max(50).optional(),
});

export type McpConfig = NonNullable<z.infer<typeof RunCommand>["mcp"]>;

export const AnswerCommand = z.object({
  type: z.literal("answer"),
  question_id: z.string().min(1),
  text: z.string(),
});

export const MessageCommand = z.object({
  type: z.literal("message"),
  text: z.string().min(1),
});

export const InterruptCommand = z.object({ type: z.literal("interrupt") });
export const CancelCommand = z.object({ type: z.literal("cancel") });

export const Command = z.discriminatedUnion("type", [
  RunCommand,
  AnswerCommand,
  MessageCommand,
  InterruptCommand,
  CancelCommand,
]);

export type RunCommand = z.infer<typeof RunCommand>;
export type Command = z.infer<typeof Command>;

export function parseCommand(line: string): { ok: true; command: Command } | { ok: false; error: string } {
  let raw: unknown;
  try {
    raw = JSON.parse(line);
  } catch {
    return { ok: false, error: "invalid JSON" };
  }
  const res = Command.safeParse(raw);
  if (!res.success) return { ok: false, error: res.error.issues.map((i) => `${i.path.join(".") || "(root)"}: ${i.message}`).join("; ") };
  return { ok: true, command: res.data };
}

// ---- Events (stdout) ----
// Every event also carries `ts` (RFC 3339) and `seq` (monotonic int), added by the emitter.

export type EventBody =
  | { type: "ready"; runner_version: string }
  // auth: how the agent is authenticated, when the runner knows (Claude):
  // "subscription" (OAuth token) makes the reported cost API-equivalent.
  | { type: "started"; session_id: string; model: string; auth?: "api_key" | "subscription" }
  // subagent: the subagent that produced the event (absent: the lead agent).
  | { type: "assistant_text"; text: string; subagent?: string }
  | { type: "tool_use"; tool: string; summary: string; subagent?: string }
  | { type: "tool_result"; tool: string; ok: boolean; summary?: string; subagent?: string }
  | { type: "question"; question_id: string; text: string; options?: string[] }
  // input_tokens includes the cache reads and writes; cache_*_tokens count
  // them on their own when the agent reports them.
  | {
      type: "usage";
      cost_usd_total: number;
      input_tokens: number;
      output_tokens: number;
      turns: number;
      cache_read_tokens?: number;
      cache_write_tokens?: number;
    }
  | { type: "committed"; sha: string; files_changed: number; insertions: number; deletions: number }
  // ref_sha is the commit at refs/jm/checkpoints/<run>/<n>; session_ref is
  // "" when the agent cannot resume at that point (Codex, Cursor).
  | {
      type: "checkpoint";
      n: number;
      ref_sha: string;
      turn: number;
      session_ref: string;
      files_changed: number;
      reason: "turn" | "question" | "iteration";
    }
  // The loop check: output is the tail of stdout+stderr (≤ 256 KiB);
  // exit_code 124 with timed_out when the timeout killed it.
  | { type: "check"; command: string; exit_code: number; output: string; duration_ms: number; timed_out: boolean }
  | { type: "done"; session_id: string; summary: string; no_changes: boolean; cost_usd_total: number }
  | { type: "paused"; session_id: string; reason: "question_timeout"; question_id: string }
  | { type: "error"; code: string; message: string; session_id?: string }
  | { type: "heartbeat" };

export type RunnerEvent = EventBody & { ts: string; seq: number };

export type EventType = EventBody["type"];
