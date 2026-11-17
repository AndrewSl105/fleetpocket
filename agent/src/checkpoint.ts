// Checkpoints: the working tree after an agent step, committed to a hidden
// ref refs/jm/checkpoints/<run>/<n> (docs/PROTOCOL.md, "Checkpoints").
//
// Only plumbing is used: a temporary index (GIT_INDEX_FILE), write-tree,
// commit-tree with HEAD as parent, update-ref. HEAD, the branch and the real
// index never move, so checkpoint commits are never ancestors of the task
// branch, and the server pushes only HEAD:refs/heads/<branch>, so the refs
// never leave the workspace.
import { execFileSync } from "node:child_process";
import { copyFileSync, existsSync, rmSync } from "node:fs";
import { join } from "node:path";
import type { EventBody } from "./protocol.ts";

export const CHECKPOINT_REF_PREFIX = "refs/jm/checkpoints";

export type CheckpointReason = "turn" | "question";

export interface CheckpointOptions {
  workspace: string;
  runId: string;
  author: { name: string; email: string };
  emit: (ev: EventBody) => void;
  // Called when a checkpoint cannot be taken; the run goes on.
  warn?: (message: string) => void;
}

export function checkpointRef(runId: string, n: number): string {
  return `${CHECKPOINT_REF_PREFIX}/${runId}/${n}`;
}

export class Checkpointer {
  private n = 0;
  private turn = 0;
  private lastTree: string | null = null;
  private lastTurnSnapshotted = -1;
  // sessionRef returns what the agent needs to resume at this point ("" if it cannot).
  sessionRef: () => string = () => "";
  private readonly o: CheckpointOptions;

  constructor(o: CheckpointOptions) {
    this.o = o;
  }

  private git(args: string[], env?: Record<string, string>): string {
    return execFileSync("git", args, {
      cwd: this.o.workspace,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
      env: { ...process.env, ...env },
    }).trim();
  }

  // observe is called after every emitted event: a finished tool step may
  // have changed files. A question is checkpointed before it is emitted (see before()).
  observe(ev: EventBody): void {
    if (ev.type === "tool_result") {
      this.turn++;
      this.snapshot("turn", false);
    }
  }

  before(ev: EventBody): void {
    if (ev.type === "question") this.snapshot("question", true);
  }

  // snapshot takes a checkpoint when the tree changed since the last one
  // (or since HEAD), or always when force is set (questions), at most once
  // per step for forced ones.
  snapshot(reason: CheckpointReason, force: boolean): void {
    try {
      const head = this.headSha();
      if (this.lastTree === null) this.lastTree = head ? this.git(["rev-parse", `${head}^{tree}`]) : "";
      const tree = this.writeTree();
      const changed = tree !== this.lastTree;
      if (!changed && !(force && this.lastTurnSnapshotted !== this.turn)) return;
      const files = this.lastTree ? this.git(["diff-tree", "-r", "--name-only", this.lastTree, tree]).split("\n").filter(Boolean).length : 0;
      const n = this.n + 1;
      const parent = head ? ["-p", head] : [];
      const sha = this.git(
        ["-c", `user.name=${this.o.author.name}`, "-c", `user.email=${this.o.author.email}`, "commit-tree", tree, ...parent, "-m", `Jungle Monkey checkpoint ${this.o.runId}/${n}`],
      );
      this.git(["update-ref", checkpointRef(this.o.runId, n), sha]);
      this.n = n;
      this.lastTree = tree;
      this.lastTurnSnapshotted = this.turn;
      this.o.emit({ type: "checkpoint", n, ref_sha: sha, turn: this.turn, session_ref: safeRef(this.sessionRef), files_changed: files, reason });
    } catch (err) {
      this.o.warn?.(`checkpoint skipped: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  private headSha(): string {
    try {
      return this.git(["rev-parse", "--verify", "-q", "HEAD"]);
    } catch {
      return "";
    }
  }

  // writeTree stages the whole working tree into a throwaway index (a copy
  // of the real one, so unchanged files are not re-hashed) and writes it.
  private writeTree(): string {
    const gitDir = this.git(["rev-parse", "--absolute-git-dir"]);
    const tmp = join(gitDir, `jm-checkpoint-index-${process.pid}`);
    const real = join(gitDir, "index");
    try {
      if (existsSync(real)) copyFileSync(real, tmp);
      const env = { GIT_INDEX_FILE: tmp };
      this.git(["add", "-A"], env);
      return this.git(["write-tree"], env);
    } finally {
      rmSync(tmp, { force: true });
    }
  }
}

function safeRef(f: () => string): string {
  try {
    return f() || "";
  } catch {
    return "";
  }
}

// Claude's session_ref is "<session id>/<message uuid>": the message is the
// last settled transcript entry (a tool result, or an assistant message with
// no pending tool call) at the checkpoint.
export function claudeSessionRef(sessionId: string, uuid: string): string {
  return sessionId && uuid ? `${sessionId}/${uuid}` : "";
}

export function parseClaudeSessionRef(ref: string | undefined): { sessionId: string; at: string } | null {
  if (!ref) return null;
  const i = ref.indexOf("/");
  if (i <= 0 || i === ref.length - 1) return null;
  return { sessionId: ref.slice(0, i), at: ref.slice(i + 1) };
}
