// The check command of a loop iteration (docs/PROTOCOL.md, "Loops"): run in
// the workspace by the runner itself, so it has exactly the runner's
// sandbox, user and network, after the session ended and its work was
// committed. The server decides what happens next from the `check` event.
import { spawn } from "node:child_process";

// Output kept for the event (the tail; the server trims it further).
export const CHECK_OUTPUT_MAX = 256 * 1024;
// Exit code reported when the timeout killed the command (like timeout(1)).
export const CHECK_TIMEOUT_EXIT = 124;

// Agent credentials never reach the check command (it runs repository code).
const HIDDEN_ENV = ["CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CURSOR_API_KEY", "FLEETPOCKET_MCP_TOKEN"];

export interface CheckResult {
  exit_code: number;
  output: string;
  duration_ms: number;
  timed_out: boolean;
}

export interface CheckOptions {
  cwd: string;
  timeoutMs: number;
  signal?: AbortSignal;
  maxBytes?: number;
  env?: NodeJS.ProcessEnv;
}

export function checkEnv(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const out = { ...env };
  for (const k of HIDDEN_ENV) delete out[k];
  return out;
}

// runCheck runs `sh -c command` with stdout and stderr interleaved, keeps
// the last maxBytes of output and kills the whole process group on timeout
// or abort. It never rejects.
export function runCheck(command: string, o: CheckOptions): Promise<CheckResult> {
  const max = o.maxBytes ?? CHECK_OUTPUT_MAX;
  const started = Date.now();
  return new Promise((resolve) => {
    let buf = Buffer.alloc(0);
    let cut = false;
    let timedOut = false;
    let settled = false;
    const keep = (chunk: Buffer) => {
      buf = Buffer.concat([buf, chunk]);
      if (buf.length > max) {
        buf = buf.subarray(buf.length - max);
        cut = true;
      }
    };
    const done = (code: number) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      o.signal?.removeEventListener("abort", onAbort);
      let output = buf.toString("utf8");
      if (cut) output = "… (earlier output cut)\n" + output.replace(/^[^\n]*\n/, "");
      resolve({ exit_code: code, output, duration_ms: Date.now() - started, timed_out: timedOut });
    };
    let child: ReturnType<typeof spawn>;
    try {
      child = spawn("sh", ["-c", command], { cwd: o.cwd, env: checkEnv(o.env ?? process.env), detached: true, stdio: ["ignore", "pipe", "pipe"] });
    } catch (err) {
      keep(Buffer.from(`check could not start: ${err instanceof Error ? err.message : String(err)}\n`));
      done(127);
      return;
    }
    const kill = () => {
      try {
        if (child.pid) process.kill(-child.pid, "SIGKILL");
      } catch {
        child.kill("SIGKILL");
      }
    };
    const timer = setTimeout(() => {
      timedOut = true;
      keep(Buffer.from(`\ncheck timed out after ${Math.round(o.timeoutMs / 1000)}s\n`));
      kill();
    }, o.timeoutMs);
    const onAbort = () => kill();
    o.signal?.addEventListener("abort", onAbort);
    child.stdout?.on("data", keep);
    child.stderr?.on("data", keep);
    child.on("error", (err) => {
      keep(Buffer.from(`check could not start: ${err.message}\n`));
      done(127);
    });
    child.on("close", (code, sig) => done(timedOut ? CHECK_TIMEOUT_EXIT : (code ?? (sig ? 128 + 9 : 1))));
  });
}
