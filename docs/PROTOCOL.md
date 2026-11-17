# Runner protocol

The Fleetpocket server (Go) and the runner (`runner/`, Node) talk over the runner container's attached stdin/stdout using JSON Lines: one JSON object per line, UTF-8, `\n`-terminated. There are no ports. Stderr is free-form diagnostics and is only logged.

Source of truth: `agent/src/protocol.ts`. Go mirror: `server/internal/runnermgr/protocol.go`.

## Lifecycle

1. The runner starts and emits `ready`.
2. The backend writes exactly one `run` command. The runner works in `/workspace` (a git checkout on the task branch).
3. While the session runs the backend may write `answer`, `message`, `interrupt` or `cancel`.
4. The runner ends with one of the exit codes below. Before exiting with `0` it commits (never pushes) and emits `committed` (only when the tree changed, or with `commit.allow_empty`), then for a loop iteration `checkpoint` (reason `iteration`, only with `checkpoints`) and `check`, and then `done`.

## Commands (stdin)

| type | fields | meaning |
|---|---|---|
| `run` | `task_id` (string or number), `prompt`, `append_system_prompt?`, `resume_session_id?`, `agent?` (`claude` default, `codex`, `cursor`), `model?`, `limits{max_turns, max_budget_usd, max_duration_sec, question_timeout_sec}`, `commit{message, author_name, author_email, allow_empty?}`, `skills?` (`[{slug, files: [{path, content}]}]`, SKILL.md included; default `[]`), `output?` (`pr` default, `artifacts`, `pr_and_artifacts`), `mcp?{token, servers[{name, url}]}`, `checkpoints?{run_id}`, `resume_session_ref?`, `check?{command, timeout_sec}` (`timeout_sec` default 600, max 3600), `subagents?` (`[{name, description, prompt, model?, tools}]`, ≤ 5, name `^[a-z][a-z0-9-]{1,31}$`) | first line, starts the session |
| `answer` | `question_id`, `text` | reply to a pending question. An answer that arrives before its question is kept and used when the question is asked. |
| `message` | `text` | extra user message injected into the running session. A run handles one prompt: input closes after the first turn's result, so a message that arrives after that is dropped. Codex and Cursor cannot take input mid-turn: a message that arrives during the turn runs as one more turn after it. **Fleetpocket does not send it**: it queues messages and starts them as the next run (API.md). |
| `interrupt` | — | stop the current turn, keep the session. The run then ends as `done` (summary "Interrupted by the user.") with whatever was committed; send a follow-up as a new run with `resume_session_id`. |
| `cancel` | — | end the session, exit code `2` |

### Skills and output

- Before the session starts the runner writes each skill folder where the agent discovers project skills: Claude `/workspace/.claude/skills/<slug>/<path>` (`settingSources: ["project"]`), Cursor `/workspace/.cursor/skills/<slug>/<path>` (`local.settingSources: ["project"]`). Each written folder is added to `.git/info/exclude`, so skills never reach a commit. When the repository already has that folder, the repository's copy wins: the runner skips the skill and emits `assistant_text` noting the skip. Codex has no skills input in `@openai/codex-sdk`: the runner appends each skill's name, description and body to its `developer_instructions`.
- `output: "artifacts"`: the Claude policy denies `Write`/`Edit`/`MultiEdit`/`NotebookEdit` outside `.fleetpocket/out/`, and the runner skips its final commit (`done.no_changes` is `true`). Codex and Cursor have no hook; the server is to discard repository changes after the run (not live yet, so the API refuses outputs other than `pr`; DECISIONS.md).

### Subagents

`subagents` (Claude only; Codex and Cursor ignore it) lets the lead agent delegate to named subagents. When it is non-empty the runner adds the SDK's subagent tool (`Agent` in `@anthropic-ai/claude-agent-sdk` 0.3.299) to `tools` and `allowedTools` and passes the SDK's `agents` option: per entry `{description, prompt, model?, tools, disallowedTools: ["AskUserQuestion", "Agent"]}`. `tools` is filtered to the runner's pre-approved tools (`Read Write Edit Glob Grep Bash WebSearch WebFetch TodoWrite`) and the run's MCP servers (`mcp__<name>…`); only the lead agent asks the owner and subagents cannot start subagents. `model` is an SDK model alias or id; absent inherits the lead agent's model. The `PreToolUse` policy hook runs for subagent tool calls too (they carry `agent_id`), so every rule above, artifacts mode included, applies to them. Absent or empty: the run is unchanged.

### Checkpoints

`checkpoints` is present only when the owner turned checkpoints on (`PUT /settings/checkpoints`); without it the runner takes none, exactly as before. With it (and `output` other than `artifacts`) the runner takes a checkpoint:

- after every step (`tool_result`) and every `usage` when the working tree differs from the previous checkpoint of the run (or from `HEAD` for the first one), reason `turn`;
- right before every `question` (so also before a pause), even when nothing changed, reason `question`.

A checkpoint is a commit of the whole working tree (`.gitignore` and `.git/info/exclude` apply, so `.fleetpocket/` and skill folders stay out) whose parent is `HEAD`, at the hidden ref `refs/jm/checkpoints/<run_id>/<n>`. The runner builds it with plumbing only (a temporary `GIT_INDEX_FILE`, `write-tree`, `commit-tree`, `update-ref`): `HEAD`, the branch and the real index never move, so checkpoint commits are never part of the task branch's history. The runner never pushes; the server pushes only `HEAD:refs/heads/<branch>`, so the refs never leave the workspace and disappear with it (`WORKSPACE_RETENTION`). A failure to take a checkpoint is logged on stderr and the run goes on.

`resume_session_ref` (restore): a checkpoint's `session_ref`. The server sends it instead of `resume_session_id`; the agent resumes that conversation at that point as a new fork. Claude: `session_ref` is `<session id>/<message uuid>` (the last transcript entry that leaves no tool call open at the checkpoint), and the runner passes `resume`, `resumeSessionAt` and `forkSession: true` (installed `@anthropic-ai/claude-agent-sdk`). Codex and Cursor cannot resume at a point (`resumeThread(id)` / `Agent.resume(id)` only), so their `session_ref` is `""` and the server starts a new session whose prompt carries a summary of the timeline up to the checkpoint. The server resets the workspace to the checkpoint before it starts the run (SPEC-server, "Checkpoints").

### Loops

`check` is present only for a run of a task with an active loop (SPEC-server, "Loops"); without it nothing changes. After a session that ends `done` (not on a limit, pause, cancel or error) the runner commits as usual, takes a checkpoint with reason `iteration` when `checkpoints` is present (even when nothing changed since the last one), then runs `sh -c <command>` in `/workspace` as itself: the same container, user, sandbox and network as the agent, no new privileges. Its environment is the runner's minus the agent credentials (`CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `CURSOR_API_KEY`, `FLEETPOCKET_MCP_TOKEN`). stdout and stderr are kept interleaved, the last 256 KiB. After `timeout_sec` the whole process group is killed and `exit_code` is `124` with `timed_out: true`. A `cancel` during the check kills it and the runner exits `2` without `check` or `done`. Then the runner emits `check` and `done`. What the command leaves in the working tree is not committed by this run (the next iteration's commit includes it). The server trims `output` like CI logs before it stores the event and decides whether another iteration runs.

`commit.allow_empty`: when the tree is clean the runner makes an empty commit (and `committed`), so the server opens a PR anyway. Ignored for runs with `check` (a loop needs to see when an iteration changed nothing) and when the session did not end `done`.

An invalid line produces an `error` event with `code: "bad_command"`; the runner keeps running. A second `run` is also `bad_command`.

## Events (stdout)

Every event has `type`, `ts` (RFC 3339, UTC, milliseconds) and `seq` (monotonic integer).

`seq` continues across runs of the same task: a new runner process reads `/workspace/.fleetpocket/events.jsonl` and continues after its last `seq`. The backend can therefore replay the file by selecting `seq > last persisted seq`.

| type | fields |
|---|---|
| `ready` | `runner_version` |
| `started` | `session_id`, `model`, `auth?` (`api_key` \| `subscription`) |
| `assistant_text` | `text`, `subagent?` |
| `tool_use` | `tool`, `summary` (short, human-readable, e.g. "Edit src/tables/export.ts"), `subagent?` |
| `tool_result` | `tool`, `ok`, `summary?`, `subagent?` |
| `question` | `question_id`, `text`, `options?` (string array) |
| `usage` | `cost_usd_total`, `input_tokens`, `output_tokens`, `turns`, `cache_read_tokens?`, `cache_write_tokens?` |
| `committed` | `sha`, `files_changed`, `insertions`, `deletions` |
| `checkpoint` | `n` (1-based within the run), `ref_sha`, `turn` (the step of the run), `session_ref` (`""` when the agent cannot resume there), `files_changed` (since the previous checkpoint), `reason` (`turn` \| `question` \| `iteration`). Only with `checkpoints`. |
| `check` | `command`, `exit_code` (`0` passes; `124` on timeout), `output` (tail of stdout+stderr, ≤ 256 KiB), `duration_ms`, `timed_out`. Only with `check`. |
| `done` | `session_id`, `summary`, `no_changes`, `cost_usd_total` |
| `paused` | `session_id`, `reason` (`question_timeout`), `question_id` |
| `error` | `code`, `message`, `session_id?` |
| `heartbeat` | — (every 15 s) |

`subagent` names the subagent behind a `tool_use`/`tool_result` (SDK frames with `parent_tool_use_id`, mapped through the lead agent's `Agent` call's `subagent_type`); absent for the lead agent. The SDK forwards only subagent tool calls by default (`forwardSubagentText` is not set), so subagent text is not streamed; the subagent's report reaches the lead agent as the `Agent` tool result. `usage` already covers subagents: the SDK's result `modelUsage` and `total_cost_usd` include every model call of the query, subagents included.

Error codes: `no_auth`, `bad_command`, `commit_failed`, `limit_exceeded`, `sdk_error`, `internal`.

## Agents

`agent` picks the session the runner starts; every agent emits the same events.

| agent | SDK | session id (`resume_session_id`) | `append_system_prompt` | questions | limits enforced |
|---|---|---|---|---|---|
| `claude` | `@anthropic-ai/claude-agent-sdk` | Claude session id | appended to the `claude_code` system prompt | `AskUserQuestion` → `question` | turns, budget, duration |
| `codex` | `@openai/codex-sdk` | Codex thread id (state in `$CODEX_HOME` = `~/.claude/codex`) | Codex `developer_instructions` | none | duration |
| `cursor` | `@cursor/sdk` (local agent) | Cursor agent id (state in `~/.cursor` → `~/.claude/cursor`) | prepended to the first prompt of a new agent | none (`askQuestion` disallowed) | duration |

- `model` is passed to the agent as is; without it Codex uses its default (`started.model` is `default`) and Cursor uses `auto`.
- Codex and Cursor report tokens but no cost: their `usage` and `done` carry `cost_usd_total: 0`, and `turns` counts model turns (one per input). Their token counts cover this run only; the server estimates the cost from its price table (SPEC-server, "Costs and budgets").
- `input_tokens` counts all input, cache reads and writes included; `cache_read_tokens` and `cache_write_tokens` count those on their own (Claude: `cacheReadInputTokens`/`cacheCreationInputTokens` of `modelUsage`; Codex: `cached_input_tokens`/`cache_write_input_tokens`, which OpenAI counts inside `input_tokens`; Cursor: `cacheReadTokens`/`cacheWriteTokens`). Older runners omit them.
- `started.auth` is set by the Claude runner from the SDK's `apiKeySource` (`"none"`, a claude.ai OAuth login, is `subscription`; anything else `api_key`). With a subscription the reported cost is what the same tokens would cost on the API ("API-equivalent"). Absent for Codex, Cursor, the fake runner and older runners.
- Codex and Cursor get the images listed under "Attached images" as image inputs. Commands they run see no auth variables (Codex: `shell_environment_policy`; Cursor: the key is removed from the runner's environment and passed to the SDK). The tool policy below (`PreToolUse`) exists only for Claude.
- Both run without their own sandbox and without approvals (Codex `danger-full-access` and `approval_policy = "never"`, Cursor `sandboxOptions.enabled: false`): the container is the sandbox.

## MCP servers

`mcp` is present only when the project has MCP servers (absent: the agent gets none, exactly as before). Every `url` is an endpoint of Jungle Monkey's MCP gateway (`<base>/api/mcp-gateway/mcp/<name>`, Streamable HTTP); `token` is the run's gateway token, sent as `Authorization: Bearer <token>`. It is not a third-party credential: it reaches only these servers and stops working when the run ends. The real servers' credentials never reach the runner.

| agent | mechanism (installed SDK) |
|---|---|
| `claude` | `options.mcpServers[name] = {type: "http", url, headers: {Authorization}}`; `mcp__<name>` added to `allowedTools` (no approval needed) |
| `codex` | `--config mcp_servers.<name>.url` and `mcp_servers.<name>.bearer_token_env_var = "FLEETPOCKET_MCP_TOKEN"`; the token is in the CLI's environment, hidden from commands by `shell_environment_policy` |
| `cursor` | `AgentOptions.mcpServers[name] = {type: "http", url, headers: {Authorization}}` |

In fake mode with `mcp`, the runner lists each server's tools (`tool_use`/`tool_result` `mcp__<name>__list`, summary `tools: a,b`) and calls the tools its prompt names as `mcp-call <server> <tool> <json args>` (`mcp__<server>__<tool>`); `egress-probe <url>` in the prompt runs `curl` and reports `egress <url> <status>`.

Notes for the backend:

- `question_id` is unique within one run (`q1`, `q2`, …) and restarts in the next run; the backend prefixes it with the run id. One `AskUserQuestion` call with several questions produces several `question` events, answered one after another.
- After a `paused` run, the pending question has been withdrawn from the Claude session. The resume run must carry the answer in its `prompt` (with `resume_session_id`), e.g. `The answer to your question "…" is: …`.
- `cost_usd_total` (in `usage` and `done`) and Claude's token counts are cumulative for the Claude **session**: a resumed session starts from the total its transcript saved, so the first `usage` of a resume run already includes earlier runs. Per-run cost (and tokens) = this run's last total minus what the earlier runs of the same session recorded.
- `limits.max_budget_usd` is passed to the SDK (`maxBudgetUsd`), which counts only spend since the run started.
- `tool_result.summary` is set only when `ok` is false (first non-empty line of the error, ≤120 chars); successful results never carry content.

## Exit codes

| code | meaning | last events |
|---|---|---|
| `0` | done | `committed?`, `done` |
| `1` | error | `error` |
| `2` | cancelled (`cancel` command or SIGTERM) | — |
| `3` | paused (question not answered within `question_timeout_sec`) | `paused` |
| `4` | limit exceeded (turns, budget or duration) | `committed?`, `error{code:"limit_exceeded"}` |

On exit `4` the runner still commits work in progress so a follow-up run can continue from it.

## Attachments

When the task has files attached (API.md, "Attachments"), Fleetpocket bind-mounts the task's attachment folder **read-only** at `/attachments`: `/attachments/<id>.<ext>` (images: PNG, JPEG, WebP or GIF, metadata removed; other files as uploaded, `<ext>` from the name or `bin`), plus image thumbnails in `/attachments/.thumbs/`. Without attachments nothing is mounted and `/attachments` does not exist. There is no stdin command for attachments: the `run` command's `prompt` ends with a block that lists them:

```
Attached images (open each with the Read tool before you start):
- /attachments/12.png (mock.png, 1200x600)

Attached files (open the ones this request needs before you start):
- /attachments/13.pdf (spec.pdf, 48213 bytes)

Earlier attachments of this task (still available; Read them if they matter for this request):
- /attachments/7.jpg (photo.jpg, 3024x4032)
- /attachments/9.csv (data.csv, 5120 bytes)
The folder /attachments is read-only; copy a file into the repository only if the change needs it.
```

For `codex` and `cursor` the headings read "Attached images (included with this message):" and "…; open them if they matter for this request):", and the runner sends the "Attached images" files to the model as image inputs.

"Attached images" and "Attached files" are those sent with this run's input (the task prompt, the messages a follow-up carries, or, for a retry, the task's own attachments again), split by kind; only "Attached images" are sent to Codex and Cursor as image inputs. "Earlier attachments" are the task's other attachments still kept. Each section is left out when empty. The runner passes `/attachments` to the SDK as an additional directory when it exists, and its `PreToolUse` policy refuses writes there (Write/Edit, Bash writes and redirections) with an explanation; reads, and copies out of it, are allowed.

## Event log

Every event is also appended to `/workspace/.fleetpocket/events.jsonl`. The runner adds `.fleetpocket/` to `.git/info/exclude` so the log is never committed.

## Environment

| variable | meaning |
|---|---|
| `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY` | Claude auth; the OAuth token wins. Missing both → `error{code:"no_auth"}`, exit `1`. |
| `OPENAI_API_KEY` | Codex auth (`agent: "codex"`); missing → `no_auth`. |
| `CURSOR_API_KEY` | Cursor auth (`agent: "cursor"`); missing → `no_auth`. |
| `FLEETPOCKET_RUNNER_FAKE=1` | scripted session, no Claude: `started`, tool events, one question (`q1`, options `hello`/`hi`), waits for the answer, writes `FLEETPOCKET_FAKE.md`, commits, `done`. Its `session_ref` is `<session id>/m<k>`; with `resume_session_ref` it starts a new session `fake-fork-…` and first emits `assistant_text` "Forked session <id> at <m>". An `interrupt` while it waits ends the run as `done` ("Interrupted by the user.", no changes), like a real session. |
| `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` (and lower case), `NODE_USE_ENV_PROXY=1` | set only for projects in egress allowlist mode: the task network has no route out except the egress proxy sidecar (`http://egress-proxy:3128`). Tools that ignore the proxy variables cannot reach the internet. |
| `FLEETPOCKET_WORKSPACE` | workspace path, default `/workspace` (tests only). |
| `FLEETPOCKET_STATE_DIR` | per-task agent state, default `~/.claude` (the volume Fleetpocket mounts; tests only). |
| `FLEETPOCKET_ATTACHMENTS` | attachment folder, default `/attachments` (tests only). In fake mode, when it exists, the runner reads every file in it (a `tool_use` "Read /attachments/<file> (<n> bytes)" each) and tries to write `.write-probe` there (`tool_result` `ok: false`, "EROFS: … read-only" on the read-only mount) before asking its question. |

## Example

```
→ {"type":"run","task_id":1,"prompt":"Say hello","limits":{"max_turns":10,"max_budget_usd":1,"max_duration_sec":60,"question_timeout_sec":30},"commit":{"message":"agent: say hello","author_name":"fleetpocket[bot]","author_email":"fleetpocket@example.com"}}
← {"type":"ready","runner_version":"0.1.0","ts":"…","seq":1}
← {"type":"started","session_id":"…","model":"…","ts":"…","seq":2}
← {"type":"question","question_id":"q1","text":"Which greeting should I write?","options":["hello","hi"],"ts":"…","seq":8}
→ {"type":"answer","question_id":"q1","text":"hello"}
← {"type":"committed","sha":"…","files_changed":1,"insertions":3,"deletions":0,"ts":"…","seq":12}
← {"type":"done","session_id":"…","summary":"…","no_changes":false,"cost_usd_total":0.01,"ts":"…","seq":13}
```
