# Fleetpocket — build prompt for Claude Code

2026-10-14 ·

## 0. Modes

`MODE=server` (default) is everything below. `MODE=local` is the `jungle-monkey` CLI on one user's computer ([LOCAL.md](LOCAL.md)): the same service and scheduler with `hosts.Local`, plus an embedded Postgres and web UI, loopback-only binding, a one-time login link instead of password + TOTP, and computed settings in the data directory. The agent-container properties of sections 3 and 5 are identical in both modes. Local mode adds local repositories as its default (section 3, "Repository sources") and an optional GitHub connection with the owner's own token. Details: DECISIONS.md, Server.

## 1. Context and goal

Build **Fleetpocket**: a small, self-hosted Go service that runs Claude Code sessions in isolated Docker containers against my GitHub repositories and turns the result into a pull request. It is a personal tool with exactly one user (me), and it must work for any repository, not one specific project.

**How to use this document.** Put it in the new repo as `docs/SPEC.md` and tell Claude Code: "Read docs/SPEC.md and implement Fleetpocket phase by phase as described in section 10. Stop after each phase and report." Everything below is written as instructions to the implementing agent.

**Who calls Fleetpocket.**

- A Telegram bot that lives inside this Go service.
- A separate web frontend that also runs as a Telegram Mini App. It lives in another repo and only talks to this service's REST API and WebSocket. Fleetpocket serves no HTML.

**What a task looks like from my side.** I write "Add CSV export to Tables" for project `vatary`. Fleetpocket clones the repo, starts an isolated Claude Code session, streams progress to Telegram and the web, lets me answer Claude's questions, and opens a PR into `dev`. I review and merge it myself.

**Target server.** A dedicated VPS (Contabo, 4 vCPU, 8 GB RAM, 100 GB SSD, Ubuntu 24.04, Docker 29). Nothing else runs on it. It sits behind the provider's firewall with only ports 22, 80 and 443 open.

## 2. Scope, non-goals and repo layout

One repo, `fleetpocket-server`, holds the Go backend and the TypeScript runner, because the stdin/stdout protocol ties them together and they change together.

**In scope for v1**

- Projects and repositories registry (any GitHub repo of my personal account).
- Task queue with a concurrency limit, task lifecycle, resume after pause.
- Runner container per task, with optional sidecar services (Postgres, Redis).
- GitHub App: clone, push branch, open PR.
- Real-time events over WebSocket, Telegram bot, REST API for the web client.
- Auth: password + TOTP for the web, Telegram `initData` for the Mini App.
- GitHub webhooks received and recorded (CI status), retention cleanup, metrics.

**Not in v1** (keep the design open for them, do not build them)

- Automatic CI fixing is opt-in (off by default): see "CI auto-fix" in section 5.
- MCP servers and the egress allowlist are opt-in (none / `open` by default): see "MCP gateway and egress allowlist" in section 5.
- Monthly budgets are opt-in (no caps by default): see "Costs and budgets" in section 5.
- Any web UI or HTML in this repo.
- Multiple users, roles, organisations.
- Docker access from inside the runner, Docker-in-Docker.
- Multi-repo tasks (one task = one repository).
- Running agents on a second host.

**Repo layout**

```
fleetpocket-server/
├── cmd/fleetpocket/      # main.go: wires everything, starts HTTP + bot + scheduler
├── internal/
│   ├── config/             # env parsing and defaults
│   ├── store/              # pgx queries, migrations (embedded)
│   ├── projects/           # project + repository registry
│   ├── tasks/              # task model, state machine, service
│   ├── scheduler/          # queue, concurrency slots
│   ├── workspace/          # git clone/branch/diff/push on the host path
│   ├── githubapp/          # App auth, installation tokens, PRs, webhooks
│   ├── runnermgr/          # Docker: networks, sidecars, runner, stdio attach
│   ├── events/             # in-process pub/sub, persistence, fan-out
│   ├── api/                # REST handlers, WebSocket, middleware
│   ├── auth/               # sessions, password+TOTP, Telegram initData
│   ├── telegram/           # bot
│   └── maintenance/        # retention, reconciliation on startup
├── runner/                 # TypeScript runner + Dockerfile
├── deploy/                 # docker-compose.deploy.yml, .env.example
├── docs/                   # SPEC.md, API.md, openapi.yaml, PROTOCOL.md
└── .github/workflows/      # build images, deploy
```

## 3. Architecture and task lifecycle

The backend owns every credential and every side effect; the runner only sees a folder and a prompt. This split is the core security property: a compromised or confused Claude session cannot push code, read other repos, or touch Docker.

```
Only Fleetpocket touches GitHub and Docker; the runner sees a folder

 ┌──────────────┐      ┌─────────────────────────────┐  tokens   ┌──────────────────────────┐
 │ Telegram bot │─────▶│      Fleetpocket (Go)       │──────────▶│ GitHub                   │
 │ (owner only) │      │ REST, WebSocket, TG bot     │           │ clone, push, PR, webhooks│
 └──────────────┘      │ Scheduler, state machine    │           └──────────────────────────┘
 ┌──────────────┐      │ GitHub App key, Claude token│  stdin / stdout
 │Web / Mini App│─────▶│ Events: persist, fan-out    │◀─────────────────────┐
 │ (separate)   │      └──────┬───────────────┬──────┘   ┌ fleetpocket-task-N ──┐
 └──────────────┘             ▼               ▼          │ ┌──────────────────┐ │
                        ┌──────────┐   ┌──────────────┐  │ │ Runner           │ │
                        │ Postgres │   │ docker-proxy │──┼▶│ Claude Code,     │ │
                        └──────────┘   └──────────────┘  │ │ /workspace       │ │
                                          starts         │ │ no GitHub/Docker │ │
                                                         │ └──────────────────┘ │
                                                         │ ┌──────────────────┐ │
                                                         │ │ Postgres / Redis │ │
                                                         │ └──────────────────┘ │
                                                         └──────────────────────┘
```

Clients reach only Fleetpocket; Fleetpocket alone holds credentials and starts containers through the filtered Docker proxy; each runner lives in its own task network with its sidecars.

**Containers**

| Container | Lifetime | Networks | Holds |
|---|---|---|---|
| `fleetpocket` (Go) | permanent | `proxy` (public via reverse proxy), `internal` | GitHub App key, Claude token, Telegram token |
| `fleetpocket-postgres` | permanent | `internal` | Fleetpocket state |
| `docker-proxy` | permanent | `internal` | read-only `docker.sock`, filtered API |
| `fleetpocket-agent-<task>` | one run | `fleetpocket-task-<task>` | Claude auth only |
| `fleetpocket-svc-<task>-<name>` | one task | `fleetpocket-task-<task>` | throwaway Postgres/Redis |
| `fleetpocket-svc-<task>-egress` | one run (allowlist projects only) | `fleetpocket-task-<task>` (alias `egress-proxy`) + `fleetpocket-egress-<task>` | the egress allowlist and the run's gateway token |
| `fleetpocket-mcp-<task>-<name>` | one run (stdio MCP servers only) | `fleetpocket-mcp-<task>`, on the server's Docker; the runner is never on it | that MCP server's own secrets (env) |

**Key rules**

- Fleetpocket talks to Docker only through `docker-socket-proxy` (`DOCKER_HOST=tcp://docker-proxy:2375`).
- Each task gets its own bridge network. Runner and its sidecars share it; nothing else is on it. It has internet egress (Anthropic API, npm, Go proxy, PyPI) unless the project is in egress `allowlist` mode: then the network is `internal` and the egress proxy sidecar is the only way out.
- Runners never join `internal` or `proxy`.
- Communication with the runner is only via its attached stdin/stdout. No ports. The runner itself may call out to the server's MCP gateway (`/api/mcp-gateway/`, through the public URL) with its run's token; MCP servers' credentials never reach it.
- Workspaces live on the host at `WORKSPACES_DIR` (default `/srv/fleetpocket/workspaces`), bind-mounted into Fleetpocket at the **same path**, so the path Fleetpocket uses is also valid for Docker bind mounts.

**Task lifecycle**

1. `queued`: created via API or Telegram.
2. `preparing`: scheduler took a slot. Mint an installation token for the repo, clone into `WORKSPACES_DIR/<task_id>`, create branch `agent/<task_id>-<slug>` from the base branch, create the task network and sidecars.
3. `running`: start the runner, send `run`, stream events.
4. `waiting_input`: runner emitted `question`. Answer from API or Telegram is written to stdin; back to `running`.
5. `paused`: no answer within the question timeout, runner exited with code 3. Containers removed, workspace and Claude state volume kept. An answer or a follow-up message resumes it as a new run with `resume_session_id`.
6. `pushing`: runner finished with commits. Validate the diff (section 5), push with a fresh token, open or update the PR.
7. `pr_open`: PR exists. Webhooks update its CI status. A follow-up message starts a new run on the same branch.
7b. `branch_ready` (local repositories only, instead of 6–7): the task branch was delivered into the owner's repository as `jm/<task_id>-<slug>`. It mirrors `pr_open`: a follow-up starts a new run on the same branch and re-delivers; Merge or Discard completes the task; "Push & open PR" goes through `pushing` to `pr_open`.
8. Terminal: `completed` (PR merged or closed, branch merged or discarded, or finished with no changes), `failed` (with reason), `cancelled`.

After every run, remove the runner container. The task network and sidecars live until the task is terminal or paused for longer than `SIDECAR_IDLE_TTL` (default 30 min).

**Repository sources**

A repository has `source` `github` (everything above) or `local` (local mode only; the API refuses it in server mode with `400 local_mode_only`). A local repository is a folder on the owner's disk: `path` (absolute work tree), `base_branch` (the branch checked out when it was added) and `remote_url` (its `origin`, informational).

- **Add**: `POST /local/repos {path}` checks a top-level, non-bare work tree with a commit, outside the data directory, and creates the repository and a project for it (idempotent). The CLI adds the repository it is started in and `jungle-monkey add [path]`. Suggestions scan the usual code folders (max depth 3, 200 results, 2 s, cached 60 s); the folder browser is confined to the home directory by real path.
- **Prepare** (`hosts.Local`): `git clone --local --no-hardlinks <path>` into `WORKSPACES_DIR/<task_id>`; the task branch `agent/<task_id>-<slug>` starts from `origin/<base>` or, with `start_from: working_tree`, from the checked-out commit plus `git diff HEAD` and the untracked files that `.gitignore` does not exclude, committed as `WIP: working tree at start`. The original folder is only read and is never mounted into a runner; the runner sees the copy exactly as it sees a GitHub clone.
- **Local publisher** (in `afterRun`, instead of the GitHub publisher while the task has no PR): the same diff validation and secrets scan (`SECRETS_SCAN`), then `git fetch <workspace> <task branch>:jm/<task_id>-<slug>` inside the owner's repository (`-2`, `-3`… when the name exists). It never touches the owner's checked-out branch, index or working tree. The task becomes `branch_ready` with a `branch_ready` event `{branch, commits, diffstat}`. A follow-up re-delivers by fast-forwarding the branch, or delivers to a new name when it is checked out or has the owner's commits.
- **Actions** on `branch_ready`: merge into the current branch (refused on a dirty tree or a detached HEAD; fast-forward or a merge commit; a conflict is aborted and listed), check out (refused on a dirty tree), discard, and "Push & open PR" (GitHub `origin` and GitHub connected): the server process pushes with the owner's token and opens the PR, and the task joins the PR/CI flow.
- A missing folder keeps the delivery waiting in `pushing` ("repository folder not found"); `POST /local/repos/{id}/locate` resumes it.
- Nodes (`hosts.Remote`) refuse local repositories.

**GitHub in local mode** is optional. `githubapp.Client` has two implementations: the GitHub App (server mode, unchanged) and the owner's token (OAuth device flow with scope `repo`, `gh auth token` after asking, or a fine-grained PAT), stored in the OS keychain (0600 file fallback) and used only by the server process. Webhooks are replaced by polling the PRs of `pr_open` tasks (60 s, ETags, backoff), feeding the same code paths as `POST /webhooks/github`, including CI auto-fix.

## 4. Runner (`runner/`)

The runner is a Node.js process in a container that runs exactly one Claude Code session through `@anthropic-ai/claude-agent-sdk` and speaks JSON Lines over stdin/stdout. It knows nothing about GitHub, Telegram, projects or the database.

**Stack.** TypeScript (strict), Node 22+, `@anthropic-ai/claude-agent-sdk`, `zod` for protocol validation. No web framework, no network listeners.

**Auth.** Use `CLAUDE_CODE_OAUTH_TOKEN` (my subscription, created with `claude setup-token`) if set, else `ANTHROPIC_API_KEY`. Fail fast with `error{code:"no_auth"}` if neither exists. Never print either value.

**Protocol.** One JSON object per line. Document it in `docs/PROTOCOL.md` and define it once in `runner/src/protocol.ts`; mirror the types in Go (`internal/runnermgr/protocol.go`).

Commands (stdin):

| type | fields | meaning |
|---|---|---|
| `run` | `task_id`, `prompt`, `append_system_prompt?`, `resume_session_id?`, `model?`, `limits{max_turns, max_budget_usd, max_duration_sec, question_timeout_sec}`, `commit{message, author_name, author_email}` | first line, starts the session |
| `answer` | `question_id`, `text` | reply to a pending question |
| `message` | `text` | extra user message injected into the running session |
| `interrupt` | — | stop the current turn, keep the session |
| `cancel` | — | end the session, exit code 2 |

Events (stdout), every event has `type`, `ts` (RFC 3339) and `seq` (monotonic int):

| type | fields |
|---|---|
| `ready` | `runner_version` |
| `started` | `session_id`, `model` |
| `assistant_text` | `text` |
| `tool_use` | `tool`, `summary` (short, human-readable, e.g. "Edit src/tables/export.ts") |
| `tool_result` | `tool`, `ok`, `summary?` |
| `question` | `question_id`, `text`, `options?` (string array) |
| `usage` | `cost_usd_total`, `input_tokens`, `output_tokens`, `turns` |
| `committed` | `sha`, `files_changed`, `insertions`, `deletions` |
| `done` | `session_id`, `summary`, `no_changes`, `cost_usd_total` |
| `paused` | `session_id`, `reason` (`question_timeout`), `question_id` |
| `error` | `code`, `message`, `session_id?` |
| `heartbeat` | — (every 15 s) |

Exit codes: `0` done, `1` error, `2` cancelled, `3` paused, `4` limit exceeded (turns, budget or duration).

**Behaviour**

1. Read `run`. Work in `/workspace` (already a git checkout on the right branch).
2. Start the session in streaming-input mode so `message` commands can be injected mid-session. Pass `cwd`, `model`, max turns, `resume` when `resume_session_id` is set, and `append_system_prompt` on top of the default Claude Code system prompt. `CLAUDE.md` from the repo must be loaded as usual.
3. Tools: allow Read, Write, Edit, Glob, Grep, Bash, WebSearch, WebFetch, TodoWrite. Accept edits without prompting.
4. A `PreToolUse` hook denies, with a clear reason fed back to Claude: `git push`, `git remote`, `git config --global`, `git credential`, any `docker` command, writes under `.github/`, and reads of `/proc/*/environ`.
5. Questions: when Claude uses its ask-the-user tool, emit `question`, wait for `answer` up to `question_timeout_sec`, and return the answer as the tool result. On timeout emit `paused` and exit 3.
6. Limits: stop with exit 4 when turns, cost or wall time exceed the limits. Use an SDK budget option if one exists; otherwise enforce from the reported cost.
7. Map every SDK message to the events above. Keep `summary` strings short; never dump file contents into events.
8. At the end, if the tree changed: `git add -A`, commit with the given message and author, emit `committed`. Do not push. Then emit `done`.
9. Also append every event to `/workspace/.fleetpocket/events.jsonl`, and add `.fleetpocket/` to `.git/info/exclude`, so Fleetpocket can replay events it missed.
10. Handle SIGTERM like `cancel`.

**Important:** SDK option and message names change between versions. Read the installed package's type definitions and current docs before coding; do not rely on memory.

**Fake mode.** With `FLEETPOCKET_RUNNER_FAKE=1`, do not call Claude: emit a scripted sequence (started, a few tool events, one question, wait for the answer, write a file, commit, done). The backend's integration tests use it.

**Dockerfile**

- Base: the official Playwright image for Ubuntu 24.04 (Chromium and its system libraries included, used from phase 2 onwards).
- Add: git, Go (latest stable), Python 3 with pip and venv, build-essential, ripgrep, jq.
- Non-root user `runner` (uid 1000). Claude state in `/home/runner/.claude` (mounted per task by the backend).
- `ENTRYPOINT ["node", "/app/dist/main.js"]`.
- Tag `ghcr.io/andrewsl105/fleetpocket-agent:<git sha>` and `:latest`.

## 5. Backend (Go)

A single Go binary: REST + WebSocket API, Telegram bot, scheduler, Docker orchestration and GitHub integration. Latest stable Go, standard library first, structured logging with `log/slog` (JSON).

**Libraries** (check each for its current version and API before use)

| Need | Library |
|---|---|
| HTTP routing | `net/http` with Go 1.22+ patterns |
| Postgres | `jackc/pgx/v5` (pgxpool), plain SQL |
| Migrations | `pressly/goose/v3`, SQL files embedded with `embed` |
| Docker | `github.com/docker/docker/client` (Moby client) |
| GitHub API | `google/go-github` + `bradleyfitz/ghinstallation/v2` |
| Git | the `git` CLI via `os/exec` (installed in the image) |
| WebSocket | `coder/websocket` |
| Telegram | `mymmrac/telego` |
| Password, TOTP | `golang.org/x/crypto/bcrypt`, `pquerna/otp` |
| Metrics | `prometheus/client_golang` |

**Configuration (env)**

| Variable | Default | Notes |
|---|---|---|
| `HTTP_ADDR` | `:8080` |  |
| `PUBLIC_BASE_URL` | — | e.g. `https://fleetpocket.example.com`, used in links and PR bodies |
| `DATABASE_URL` | — |  |
| `DOCKER_HOST` | `tcp://docker-proxy:2375` |  |
| `WORKSPACES_DIR` | `/srv/fleetpocket/workspaces` | same path on host and in container |
| `RUNNER_IMAGE` | `ghcr.io/andrewsl105/fleetpocket-agent:latest` | must already be pulled locally |
| `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY` | — | passed to runners only |
| `GITHUB_APP_ID`, `GITHUB_APP_PRIVATE_KEY_PATH`, `GITHUB_WEBHOOK_SECRET` | — |  |
| `TELEGRAM_BOT_TOKEN`, `TELEGRAM_ALLOWED_USER_ID` | — | bot ignores everyone else |
| `WEB_APP_URL` | — | URL of the separate web client / Mini App |
| `ADMIN_PASSWORD_HASH`, `ADMIN_TOTP_SECRET` | — | bcrypt hash; base32 secret |
| `SESSION_SECRET` | — | signs session cookies |
| `CORS_ORIGINS` | empty | only if web is on another origin |
| `MAX_CONCURRENT_TASKS` | `2` |  |
| `RUNNER_MEMORY` / `RUNNER_CPUS` | `3g` / `2` | per runner, hard limits |
| `SIDECAR_MEMORY` | `512m` | per sidecar |
| `DEFAULT_MODEL` | empty (SDK default) |  |
| `DEFAULT_MAX_TURNS` / `DEFAULT_MAX_BUDGET_USD` / `DEFAULT_MAX_DURATION` | `150` / `5` / `2h` |  |
| `QUESTION_TIMEOUT` | `20m` |  |
| `EVENT_RETENTION` | `720h` (30 days) |  |
| `SECRETS_KEY` | — | 32 bytes (64 hex chars or base64); AES-256-GCM key for owner-entered secrets (MCP credentials) in Postgres. `install.sh` generates it, also on upgrade; local mode keeps one in the keychain. Empty: MCP servers are disabled, nothing else changes |
| `MCP_GATEWAY_URL` | `PUBLIC_BASE_URL` (local mode: `http://host.docker.internal:<port>`) | the server's base URL as runners and the egress proxy reach it |
| `EGRESS_PROXY_IMAGE` | `ghcr.io/andrewsl105/fleetpocket-server:latest` (compose: the server's own tag) | image of the egress proxy sidecar (`fleetpocket egress-proxy`) |

Project-level settings override defaults (section 6).

**Scheduler.** Postgres is the queue. A loop claims the oldest `queued` task with `SELECT … FOR UPDATE SKIP LOCKED` when fewer than `MAX_CONCURRENT_TASKS` tasks are in `preparing`, `running` or `pushing`. Resumes of paused tasks go through the same slots. Wake the loop on new tasks via a channel and also poll every 5 s.

**Runner manager (Docker)**

- Create network `fleetpocket-task-<id>` (bridge, labelled).
- Start sidecars from the project's `services` list. Supported: `postgres` (image `postgres:17-alpine`, random password) and `redis` (`redis:7-alpine`). Wait for health. Expose them to the runner as `DATABASE_URL` and `REDIS_URL`.
- Create volume `fleetpocket-claude-<id>` mounted at `/home/runner/.claude` so sessions can resume in a new container.
- Start the runner with `OpenStdin`, `AttachStdin/Stdout/Stderr`, `StdinOnce=false`, memory and CPU limits, `PidsLimit` 1024, `no-new-privileges`, all capabilities dropped, bind mount `WORKSPACES_DIR/<id>` to `/workspace`, project env plus auth env.
- Labels on everything: `fleetpocket.task_id`, `fleetpocket.project`, `fleetpocket.run_id`, `fleetpocket.kind` (`runner` or `sidecar`). Cleanup and reconciliation select by label.
- Read stdout line by line, validate, assign to the run, persist, publish. Stderr goes to logs. On exit, read the code and map it to the state machine.

**Reconciliation on startup.** List containers with `fleetpocket.kind=runner`. Running ones: re-attach, replay `events.jsonl` from the last persisted `seq`, continue. Exited ones: replay, then apply the exit code. Tasks in `running` with no container: replay what exists, then mark `failed{reason:"lost"}`. Remove orphaned networks and sidecars.

**GitHub App and workspace**

- One private App on my personal account. Permissions: Contents RW, Pull requests RW, Metadata R, Checks R, Actions R. Events: `pull_request`, `check_suite`, `workflow_run`.
- Mint an installation token per operation, scoped to the single repository. Never write tokens to disk or `.git/config`; pass them via a credential helper or an `http.extraHeader` for that one command only.
- Clone with `--filter=blob:none`, base branch from the repository record, branch `agent/<id>-<slug>` (slug from the title, ASCII, max 40 chars). Commits are authored as `fleetpocket[bot]`.
- Before push, validate the diff against the base: reject changes under `.github/`; reject files over 5 MB; reject total diff over 20 000 changed lines; run a secrets scan (e.g. `gitleaks` binary in the Fleetpocket image) and reject on findings. A rejection sets the task to `failed` with the reason and keeps the workspace for inspection.
- PR: title from the task, body with the task prompt, Claude's summary, files changed, cost, turns, and a link to the task. Create it once; later runs push to the same branch and add a PR comment with the new summary.
- Webhooks at `POST /webhooks/github`: verify `X-Hub-Signature-256`, store CI conclusions on the task, notify via Telegram, mark the task `completed` when its PR is merged or closed. `OnCIFailed` (and `OnCI`, `OnPRClosed`) drive CI auto-fix.

**CI auto-fix** (`internal/autofix` decides, `internal/cifix` acts; off unless enabled)

- Settings, per project (`projects.ci_autofix`, null fields inherit) over a global default (settings key `ci_autofix`, `GET/PUT /api/v1/settings/ci-autofix`): `enabled` (default false), `max_attempts` (2, at most 5), `checks` (check names; empty = all), `budget_usd` per attempt (default: the project's task budget ÷ 2), `allow_ci_changes` (false).
- Jungle Monkey records the commit it pushes (`tasks.last_pushed_sha`, the runner's last `committed` sha, written before the push). A failed `check_suite`/`workflow_run` on that commit, for a PR Jungle Monkey opened, while the task is `pr_open` and the loop is not stopped, makes the server list the commit's check runs with the installation token. It acts only when no check is still running; each head commit is handled once (`autofix_handled_sha`).
- For each failed check in scope it downloads the GitHub Actions job log (pre-signed URL, fetched without the token; other apps' checks give their output text), strips ANSI, keeps the last 400 lines plus error-pattern lines and names the failed steps. The failure signature is a hash of the first failing check's name and its first normalised error lines.
- Guards in a fixed order: off, not our PR, loop stopped, check not listed, failing commit ≠ last system push (a human push), run active → skip; attempts used up, same signature as the previous attempt, budget → "needs you" with a notification that says why and links the checks' logs.
- An attempt saves the trimmed logs as a task attachment and queues a `followup` run on the same branch (`runs.autofix_attempt`, `runs.max_budget_usd`) whose prompt names the failing checks, the signature and the attachment and says: fix the cause, keep the change minimal, do not skip, delete or weaken tests, do not edit CI configuration. The runner gets no token.
- Before an attempt's commits are pushed, the scheduler inspects `git diff origin/<branch>...HEAD`: changes under `.github/workflows/` or other CI config, deleted test files, added skip markers, coverage threshold changes (or a diff too large to read). Unless `allow_ci_changes`, it does not push: the loop is `held`, the task stays `pr_open` with an open question "The fix changes CI or tests. Push anyway?" (`Push anyway` / `Discard`; changes under `.github/` can only be discarded). Discard removes the workspace (the next run re-clones the branch).
- After the push the loop waits for CI. All checks passing ends it as `green` ("✓ CI green after N fixes"). A fix run with no changes, a run that hits a limit, or a failed run ends it as `needs_you`. New checks on a commit Jungle Monkey did not push, a follow-up message from the owner, or the PR closing cancel it. Every step is an `autofix` timeline event and a Telegram notification.

**MCP gateway and egress allowlist** (`internal/mcpgateway`, `internal/egress`; nothing changes for a project with no MCP servers and egress `open`)

- *Registry.* `mcp_servers` rows belong to a project or are global (`project_id` NULL); a project turns global ones on (`project_id`'s rows in `project_mcp_globals`). Kinds: `remote` (Streamable HTTP or SSE URL; secrets are header values or a bearer token), `stdio` (an npm or pip package run in the agent image, or any Docker image; secrets are its environment), `postgres` (the bundled server: tools `query` and `list_tables`; secret `DATABASE_URL`). Per server: tool list (`all`, `allow` or `deny` with names or `*` patterns), `read_only` (default on for `postgres`), `max_calls` per run, enabled flag, last use and status.
- *Secrets.* One JSON object per server, sealed with AES-256-GCM under `SECRETS_KEY` (`internal/secretbox`, random nonce, the row id as additional data). Never logged, never returned: the API shows `secret_hints` (last characters). They are opened only by the gateway in memory, or put into a stdio sidecar's environment.
- *Gateway.* Mounted on the main listener at `/api/mcp-gateway/` (outside `/api/v1`: no session, no CSRF), so the existing reverse-proxy route serves it and runners on nodes reach it through the public URL. Each run that has MCP servers (or an allowlist) gets a random token; its SHA-256, expiry (the run's maximum duration + 15 min) and server ids are stored on `runs`; `FinishRun` clears it. A request with a missing, expired, finished or foreign token is `401`; a server outside the run's scope is `404`. The gateway connects to the upstream with the decrypted credentials (Go MCP SDK client), exposes only the allowed tools to the runner (stateless Streamable HTTP), counts calls against `max_calls`, and writes an `mcp_call` event per call (server, tool, duration, ok/error, argument and result sizes; never arguments, results or secrets). A denied tool or the limit is an `mcp_call` with `denied`.
- *Read-only Postgres.* Each call opens its own connection with `default_transaction_read_only=on`, runs one statement through the extended protocol (no multi-statement strings) inside `BEGIN READ ONLY`, and always rolls back; a read-only database role is still recommended.
- *Stdio servers* run as sidecars on the server's own Docker (also for tasks on nodes), on a per-task network the runner is never on, with `CapDrop ALL`, `no-new-privileges` and the sidecar memory limit. The gateway talks to them only over their attached stdin/stdout; they start on first use in a run and are removed when it ends.
- *Agents.* The `run` command's `mcp` block (PROTOCOL.md) carries the gateway URLs and the token: Claude gets `mcpServers` (HTTP) and `mcp__<name>` in `allowedTools`; Codex gets `mcp_servers.<name>.url` with `bearer_token_env_var`; Cursor gets `mcpServers`.
- *Egress* (`projects.egress`, NULL = `open`): in `allowlist` mode the task network is created `internal` (an existing network of the other mode is recreated with its sidecars), and an egress proxy sidecar — the server image's `fleetpocket egress-proxy` — joins it as `egress-proxy` plus its own bridge network. The runner gets `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` (and `NODE_USE_ENV_PROXY=1`). The proxy allows `CONNECT` and plain HTTP only to the project's hosts (presets: the task's agent API, npm, PyPI, Go proxy, crates, Docker Hub, GitHub; plus listed `host`, `*.domain` = subdomains only, `host:port`; no port = 80/443) and the gateway, refuses names that resolve to private addresses (except the gateway), and reports each refused host once per run: an `egress_blocked` event, a Telegram message with "Allow for this project" and `POST /projects/{id}/egress/allow`, which takes effect on the next run. The proxy is recreated every run. Tools that ignore the proxy variables cannot reach the network. A task on a node in allowlist mode fails with an explanation instead of running open.

**Costs and budgets** (`internal/costs`; with no monthly caps nothing changes except that more is recorded and shown)

- *Per run* (`runs`): agent, model (from `started`), tokens (`input_tokens` includes cache reads and writes; `cache_read_tokens`, `cache_write_tokens`), turns, `cost_usd`, `cost_source` and `auth`. Claude reports its cost (`reported`; its session-cumulative cost and tokens minus what earlier runs of the session recorded). Codex and Cursor report tokens only: the server prices them from the price table (`estimated`), or records `unknown` with cost 0 when no row matches. `auth` comes from the runner's `started.auth` (Claude SDK `apiKeySource`: `none` = claude.ai OAuth = `subscription`); a subscription run's cost is labelled "API-equivalent".
- *Price table* (settings key `costs`, `GET/PUT /api/v1/settings/costs`): rows `{agent, model, input_usd, cached_input_usd, cache_write_usd, output_usd}` per million tokens, matched by agent and model (`*` = any model of the agent), with an "updated" date. The built-in table holds OpenAI's API prices for the Codex models (checked 2026-11-17); Codex's `default` and Cursor's `auto` route to different models and have no row. Used only when a run reports tokens but no cost; estimates are always shown with `≈`.
- *Task totals* kept with `cost_usd_total`: `autofix_cost_usd` (runs with `autofix_attempt`), `cost_estimated`, `cost_api_equivalent`, `cost_unknown` (some run has no price). Projects carry `month_cost_unknown`, `/stats` `cost_unknown_today`/`_month`, the report's budgets `month_cost_unknown` and its sums `unknown_runs`. Every place shows one format (`costs.Format`, web `usd()`): `$0.84`, `≈$0.84`, `$?` (nothing known), `$0.84+?` (part unknown); the PR body and the Telegram card too.
- *Budgets*: the per-run `max_budget_usd` (unchanged), a monthly cap per project (`projects.budget`) and a global one (settings `costs.monthly_budget_usd`); a per-schedule cap is planned as another scope of the same gate. A month is the calendar month in the server's local time, by run start; estimated costs count. At 80% of a cap a `budget` event (`warning`) goes on the task whose run crossed it, at 100% one more (`reached`), each once per scope and month (`budget_alerts`), each also a Telegram message. From 100% the scheduler's claim query skips queued tasks of that scope (a running run is never stopped for a monthly cap); they stay `queued` with `budget_wait` (`project` or `global`), status reason `waiting: budget` and a `budget` event (`waiting`), sent to Telegram with **Start anyway**. The owner's override (`POST /api/v1/tasks/{id}/budget-override` or the button; `budget_override_at`) lets that task start runs until the month ends. Raising or removing a cap, or a new month, releases the waiting tasks on the next claim.
- *Report*: `GET /api/v1/costs` sums runs by total, project, agent, day/week/month and day × project × agent, with this month's spend against each cap. Metrics: `fleetpocket_budget_held_total{project,scope}`, `fleetpocket_budget_alerts_total{scope,level}`, `fleetpocket_budget_overrides_total{project}`.

**Checkpoints** (off by default: settings key `checkpoints`, `GET/PUT /api/v1/settings/checkpoints {enabled}`; off, the run command and every push are exactly as before)

- *Taking them.* With the setting on, the scheduler adds `checkpoints: {run_id}` to the run command. After every step that changed files and right before every question the runner commits the whole working tree to `refs/jm/checkpoints/<run>/<n>` with plumbing only (temporary index, `commit-tree` on `HEAD`, `update-ref`; HEAD, the branch and the index never move) and emits `checkpoint {n, ref_sha, turn, session_ref, files_changed, reason}` (PROTOCOL.md). The server stores each in `checkpoints` (with its timeline event). The refs are never pushed (the push refspec is `HEAD:refs/heads/<branch>` only), so they live as long as the workspace: `WORKSPACE_RETENTION`, after which restore is disabled with that reason.
- *Restoring.* `POST /api/v1/tasks/{id}/checkpoints/{cid}/restore {instruction?}` (task not busy, PR not closed, workspace not cleaned) records `checkpoint_restored` and queues a `restore` run (`runs.restore_checkpoint_id`). Before the runner starts, the scheduler resets the workspace on the task's host (`hosts.Host.RestoreCheckpoint`, also over the node wire): `reset --hard <checkpoint>^` (the branch commit the checkpoint was taken on), `clean -fd` (ignored files such as `.fleetpocket/` stay), `read-tree -u --reset <checkpoint>`; the checkpoint's files are uncommitted on the task branch, so the run's commit records them and checkpoint commits never join the branch. Claude with a `session_ref` gets `resume_session_ref` (resume + `resumeSessionAt` + `forkSession`); Codex, Cursor or a checkpoint without one get a new session whose prompt summarises the task and the timeline up to the checkpoint (owner messages and answers, the agent's text, tool summaries; the last 80 lines) plus the instruction. Later events stay in the timeline; clients show the events between the checkpoint and `checkpoint_restored` as an abandoned branch.
- *Pushing after a restore.* When the task has run a restore and `origin/<branch>` is no longer an ancestor of HEAD (`hosts.Host.Diverged`), `afterRun` pushes even without new commits, with `--force-with-lease=refs/heads/<branch>:<what this workspace last pushed>` (`hosts.Host.ForcePush`), only to branches Jungle Monkey created (`agent/<id>` or `agent/<id>-<slug>`); any other branch fails the task with a clear reason and nothing is pushed. A remote that moved meanwhile makes the lease fail (the task fails, nothing is overwritten). A forced push adds a note to the PR comment, a `branch_force_pushed` event and a Telegram message.
- *Preview.* `GET /api/v1/tasks/{id}/checkpoints/{cid}` counts the later runs, steps, messages and commits and returns `git diff <checkpoint> HEAD` from the host (capped at 512 KB); a checkpoint whose commit is gone comes back unavailable.

**Workspace cleanup.** Delete the workspace and the Claude state volume 7 days after the task becomes terminal.

## 6. Database schema

Seven tables carry the whole state; everything else is derived. Use UUIDv7 or `bigserial` ids, `timestamptz` everywhere, and `jsonb` only where the shape is genuinely open. The schema below is the starting point; adjust names if needed but keep the relations.

```sql
CREATE TABLE projects (
  id              text PRIMARY KEY,            -- slug, e.g. 'vatary'
  name            text NOT NULL,
  instructions    text NOT NULL DEFAULT '',    -- appended to Claude's system prompt
  services        text[] NOT NULL DEFAULT '{}',-- 'postgres', 'redis'
  env             jsonb NOT NULL DEFAULT '{}', -- non-secret env for runners
  runner_image    text,                        -- NULL = RUNNER_IMAGE
  model           text,
  max_turns       int,
  max_budget_usd  numeric(8,2),
  max_duration    interval,
  runner_memory   text,
  runner_cpus     numeric(4,2),
  telegram_thread_id bigint,                   -- forum topic for this project
  archived        boolean NOT NULL DEFAULT false,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE repositories (
  id              bigserial PRIMARY KEY,
  project_id      text NOT NULL REFERENCES projects(id),
  full_name       text NOT NULL,               -- 'andrewsl105/vatary-server'
  base_branch     text NOT NULL DEFAULT 'dev',
  installation_id bigint NOT NULL,
  is_default      boolean NOT NULL DEFAULT false,
  source          text NOT NULL DEFAULT 'github', -- 'github' | 'local' (00017, local mode)
  path            text NOT NULL DEFAULT '',        -- local: absolute work tree
  remote_url      text NOT NULL DEFAULT '',        -- local: origin, informational
  UNIQUE (project_id, full_name)
);
-- tasks (00017): local_branch text (the delivered jm/... branch),
-- start_from text NOT NULL DEFAULT 'last_commit' ('last_commit' | 'working_tree').

CREATE TABLE tasks (
  id              bigserial PRIMARY KEY,       -- short numbers read well in Telegram
  project_id      text NOT NULL REFERENCES projects(id),
  repository_id   bigint NOT NULL REFERENCES repositories(id),
  title           text NOT NULL,
  prompt          text NOT NULL,
  status          text NOT NULL,               -- section 3 states
  status_reason   text,
  branch          text,
  session_id      text,                        -- latest Claude session
  pr_number       int,
  pr_url          text,
  ci_status       text,                        -- pending | success | failure
  cost_usd_total  numeric(10,4) NOT NULL DEFAULT 0,
  source          text NOT NULL,               -- 'web' | 'telegram' | 'api'
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now(),
  finished_at     timestamptz
);
CREATE INDEX ON tasks (status, created_at);

CREATE TABLE runs (                            -- one runner container lifetime
  id              bigserial PRIMARY KEY,
  task_id         bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  kind            text NOT NULL,               -- 'initial' | 'resume' | 'followup'
  input           text NOT NULL,               -- prompt, answer or follow-up text
  container_id    text,
  exit_code       int,
  last_seq        bigint NOT NULL DEFAULT 0,
  cost_usd        numeric(10,4) NOT NULL DEFAULT 0,
  started_at      timestamptz,
  finished_at     timestamptz
);

CREATE TABLE events (
  id              bigserial PRIMARY KEY,
  task_id         bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id          bigint REFERENCES runs(id) ON DELETE CASCADE,
  seq             bigint,                      -- runner seq; NULL for backend events
  type            text NOT NULL,
  payload         jsonb NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, seq)
);
CREATE INDEX ON events (task_id, id);

CREATE TABLE questions (
  id              text PRIMARY KEY,            -- runner question_id, prefixed with run id
  task_id         bigint NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
  run_id          bigint NOT NULL REFERENCES runs(id),
  text            text NOT NULL,
  options         jsonb,
  answer          text,
  answered_via    text,                        -- 'web' | 'telegram'
  telegram_message_id bigint,
  created_at      timestamptz NOT NULL DEFAULT now(),
  answered_at     timestamptz
);

CREATE TABLE auth_sessions (
  id              text PRIMARY KEY,            -- random 32 bytes, base64url
  kind            text NOT NULL,               -- 'web' | 'telegram'
  expires_at      timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  last_seen_at    timestamptz NOT NULL DEFAULT now()
);
```

Skill library and diagram outputs (migration `00015_skills_artifacts.sql`): `skills (id, slug unique, name, description, body, files jsonb, enabled_by_default, created_at, updated_at)`, `project_skills (project_id, skill_id)`, `tasks.skills` (slugs the last run used) with the per-task overrides `tasks.skills_add`/`tasks.skills_remove`, `tasks.output` (`pr` default | `artifacts` | `pr_and_artifacts`), `projects.default_output` (nullable → `pr`) and `task_artifacts (id, task_id, run_id, path, kind, title, summary, confidence, sources jsonb, content, sha256, version, created_at)`, unique per `(task_id, path, version)`. The scheduler resolves a run's skills as project skills + `enabled_by_default` skills + task additions − task removals and sends them in the run command (PROTOCOL.md). Diagram collection is not live yet (DECISIONS.md, Server).

Checkpoints (migration `00016_checkpoints.sql`): `checkpoints (id, task_id, run_id, event_id → events ON DELETE SET NULL, n, ref_sha, turn, session_ref, files_changed, reason, created_at)`, unique per `(run_id, n)`, and `runs.restore_checkpoint_id` (nullable; the checkpoint a `restore` run starts from). The setting is the `settings` row `checkpoints` (`{enabled}`; missing = off).

Backend-generated events (status changes, PR opened, CI result) go into `events` with `seq` NULL, so the web client reads one timeline per task. A first answer wins: answering an already answered question returns `409`.

## 7. REST API, WebSocket and auth

The web client is built later from `docs/API.md` and `docs/openapi.yaml` alone, so keep both complete and in sync with the code. JSON everywhere, errors as `{"error":{"code","message"}}`, cursor pagination (`?cursor=&limit=`) on lists, all routes under `/api/v1`.

**Endpoints**

| Method and path | Purpose |
|---|---|
| `POST /api/v1/auth/login` | `{password, totp}` → session cookie |
| `POST /api/v1/auth/telegram` | `{init_data}` from the Mini App → session cookie |
| `POST /api/v1/auth/logout` |  |
| `GET /api/v1/me` | session check |
| `GET /api/v1/projects` · `POST` | list, create |
| `GET /api/v1/projects/{id}` · `PATCH` · `DELETE` (archive) |  |
| `POST /api/v1/projects/{id}/repositories` · `DELETE …/{repoId}` | manage repos |
| `GET /api/v1/github/repositories` | repos the App can see, for the "add repo" picker |
| `GET /api/v1/tasks?project=&status=` · `POST` | list, create `{project_id, repository_id?, title?, prompt}` |
| `GET /api/v1/tasks/{id}` | task with latest run, open question, PR info |
| `GET /api/v1/tasks/{id}/events?after_id=` | timeline, paginated |
| `GET /api/v1/tasks/{id}/diff` | `git diff base...branch` from the workspace, capped at 1 MB |
| `POST /api/v1/tasks/{id}/answer` | `{question_id, text}` |
| `POST /api/v1/tasks/{id}/message` | follow-up or mid-session message `{text}` |
| `POST /api/v1/tasks/{id}/interrupt` · `/cancel` · `/retry` | control |
| `GET /api/v1/stats` | running and queued counts, cost today and this month |
| `GET /api/v1/ws` | WebSocket |
| `POST /webhooks/github` | GitHub (signature auth, no session) |
| `GET /healthz` · `GET /metrics` | unauthenticated, `/metrics` only from the internal network |

`message` decides by state: while `running` it is injected into the session; in `paused`, `pr_open`, `branch_ready` or `completed` it starts a new run on the same branch with the saved session; otherwise `409`.

**WebSocket.** After connect, the client sends `{"op":"subscribe","task_id":123}` or `{"op":"subscribe","scope":"tasks"}` (status changes of all tasks, for the list view). Server pushes `{"op":"event","task_id","event":{…}}` and `{"op":"task","task":{…}}`. On reconnect the client reloads missed events through `GET …/events?after_id=`. Ping every 30 s.

**Auth**

- One owner. Web login needs the password (bcrypt) **and** a TOTP code. Rate limit: 5 failed attempts per IP per 15 minutes, then `429`.
- Session = random id in an `HttpOnly`, `Secure`, `SameSite=Lax` cookie, 30-day sliding expiry, stored in `auth_sessions`.
- Mini App: verify `init_data` with the bot token per Telegram's documented HMAC algorithm, reject if `auth_date` is older than 1 hour, and require the user id to equal `TELEGRAM_ALLOWED_USER_ID`.
- State-changing requests require the `X-Requested-With: fleetpocket` header (cheap CSRF guard alongside SameSite).
- Add a CLI helper: `fleetpocket hash-password` and `fleetpocket totp-setup` (prints the secret and an `otpauth://` URI).

**Same origin.** The reverse proxy serves the web client at `/` and routes `/api`, `/webhooks`, `/healthz` to Fleetpocket on the same domain, so no CORS is needed. Keep `CORS_ORIGINS` for local development.

## 8. Telegram bot

The bot runs as a goroutine in the same process, uses long polling (no public webhook needed) and calls the same task service as the REST API, never the HTTP endpoints. It answers only `TELEGRAM_ALLOWED_USER_ID`; every other update is dropped silently and logged.

**Where it talks.** Two modes, both supported:

- Private chat with the bot: commands name the project explicitly.
- A forum supergroup with one topic per project: the topic decides the project (`projects.telegram_thread_id`). `/bind <project>` inside a topic links it.

**Commands**

| Command | Action |
|---|---|
| `/new <project> <prompt>` | create a task (in a bound topic: `/new <prompt>`, or just send plain text) |
| `/tasks` | active tasks with status and inline buttons |
| `/task <id>` | details, last events, PR link |
| `/msg <id> <text>` | follow-up or mid-session message |
| `/cancel <id>` · `/retry <id>` | control |
| `/projects` | list projects |
| `/app` | button that opens the Mini App (`WEB_APP_URL`) |

**Messages**

- One status message per task, edited in place as it moves through states, with the latest one-line activity ("Editing src/tables/export.ts"). Throttle edits to one per 3 s per message and coalesce.
- Separate messages, which notify, only for: a question, PR opened, task failed, CI failed, budget or time limit hit.
- A question shows its text plus inline buttons for `options` and a "Reply with text" hint; a reply to the question message is taken as the answer. After answering, edit the message to show the answer and who answered (web or Telegram).
- Every task message has buttons: Open in app (Mini App deep link to the task), Cancel, and PR (when it exists).
- Never send diffs or long logs to Telegram; link to the app instead. Respect Telegram's 4096-character limit and escape text for the chosen parse mode.

## 9. Deployment, CI/CD and observability

Deployment follows the pattern I already use: GitHub Actions builds images, pushes them to GHCR, and the server only holds `/srv/fleetpocket/docker-compose.deploy.yml` and `.env`. No source code lives on the server.

**Fleetpocket image.** Multi-stage build, final stage Debian slim or Alpine with `git`, `ca-certificates` and `gitleaks`; runs as a non-root user that can write to `WORKSPACES_DIR`. Tag `ghcr.io/andrewsl105/fleetpocket-server:<sha>` and `:latest`.

**`deploy/docker-compose.deploy.yml`** (adapt names, keep the structure)

```yaml
services:
  fleetpocket:
    image: ghcr.io/andrewsl105/fleetpocket-server:${FLEETPOCKET_TAG:-latest}
    restart: unless-stopped
    env_file: .env
    environment:
      DOCKER_HOST: tcp://docker-proxy:2375
      WORKSPACES_DIR: /srv/fleetpocket/workspaces
    volumes:
      - /srv/fleetpocket/workspaces:/srv/fleetpocket/workspaces
      - /srv/fleetpocket/secrets/github-app.pem:/secrets/github-app.pem:ro
    networks: [proxy, internal]
    depends_on: [fleetpocket-postgres, docker-proxy]

  fleetpocket-postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_USER: fleetpocket
      POSTGRES_PASSWORD: ${PG_PASSWORD}
      POSTGRES_DB: fleetpocket
    volumes: [pgdata:/var/lib/postgresql/data]
    networks: [internal]

  docker-proxy:
    image: tecnativa/docker-socket-proxy
    restart: unless-stopped
    environment:
      CONTAINERS: 1
      NETWORKS: 1
      VOLUMES: 1
      IMAGES: 1
      EXEC: 0
      POST: 1
    volumes: [/var/run/docker.sock:/var/run/docker.sock:ro]
    networks: [internal]

networks:
  proxy:    { external: true }   # shared with the reverse proxy
  internal: {}

volumes:
  pgdata:
```

No service publishes ports. The reverse proxy (Caddy or Nginx Proxy Manager on the same host, in the `proxy` network) terminates TLS. WebSocket upgrade must be enabled for `/api/v1/ws`.

**Workflows**

- `ci.yml` on pull requests: `go vet`, `go test ./...` (with a Postgres service), `golangci-lint`, runner `npm ci && npm run typecheck && npm test`.
- `release.yml` on push to `main`: build and push both images, then SSH to the server (secrets `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`), run `docker pull` for **both** the Fleetpocket and runner images (Fleetpocket starts runners from the local image and does not pull), then `docker compose -f docker-compose.deploy.yml up -d`.
- Migrations run automatically on Fleetpocket startup.

**Observability**

- `slog` JSON logs with `task_id`, `run_id`, `project` on every task-related line.
- `/metrics`: tasks by status, queue length, run duration histogram, cost total per project, runner exits by code, Telegram and GitHub API errors.
- `/healthz`: DB reachable and Docker reachable.
- Back up `fleetpocket-postgres` daily with `pg_dump` (document the cron line in the README; no need to implement it in Go).

## 10. Implementation phases and working rules

Build in eight phases. Each phase ends with working, tested code, updated docs, and a short report to me; stop and wait for my go-ahead before the next phase.

1. **Runner core.** Protocol types, stdin/stdout loop, fake mode, `events.jsonl`, exit codes, unit tests for parsing and the fake sequence, Dockerfile. Done when `docker run -i` with fake mode completes a full scripted session from a JSON file piped in.
2. **Backend skeleton.** Config, logging, Postgres + migrations, projects and repositories CRUD, tasks CRUD without execution, auth (password + TOTP, sessions, CLI helpers), `docs/API.md` + `docs/openapi.yaml` started. Done when the API works end to end with `curl`.
3. **Execution with the fake runner.** Scheduler, runner manager (networks, sidecars, labels, limits, stdio attach), state machine, event persistence, answers, pause and resume, cancel, startup reconciliation. A local `git init` repo stands in for GitHub. Done when an integration test drives a task from `queued` to a local commit via the fake runner.
4. **GitHub App.** Installation tokens, clone, branch, diff validation, push, PR create and update, webhooks with signature check. Done when a task against a real test repo opens a PR.
5. **Real Claude.** Agent SDK integration in the runner: streaming input, hooks, question interception, limits, resume. Done when a real task on a small test repo opens a sensible PR and a question round-trips.
6. **Real-time and Telegram.** WebSocket hub, Telegram bot (commands, status message, questions, notifications), Mini App `initData` login.
7. **Operations.** Retention and workspace cleanup, metrics, healthz, stats endpoint, README with setup steps (GitHub App creation, TOTP setup, reverse proxy, backups).
8. **CI/CD.** `ci.yml`, `release.yml`, `deploy/` files, `.env.example` with every variable from section 5 and a comment for each.

**Working rules**

- Verify library and SDK APIs against the installed versions and their docs before writing code. Never invent option names or endpoints.
- When the spec is ambiguous or seems wrong, ask me instead of guessing. Record every decision that deviates from this spec in `docs/DECISIONS.md` with one line of reasoning.
- Keep it small: no frameworks, ORMs or abstractions beyond what a section asks for. Prefer clear code over clever code.
- Security properties in sections 3 and 5 are requirements, not suggestions: the runner gets no GitHub token, no Docker access, no host network; tokens never touch disk.
- File ownership: Fleetpocket and the runner both run as uid 1000 so workspaces written by one are writable by the other; document that `/srv/fleetpocket/workspaces` must be owned by uid 1000.
- Tests: unit tests for the state machine, protocol parsing, diff validation, `initData` and webhook signature checks; integration tests with the fake runner against a real Docker daemon, skipped when Docker is unavailable.
- Small, focused commits with clear messages. Keep `docs/PROTOCOL.md`, `docs/API.md` and `docs/openapi.yaml` updated in the same commit as the code they describe: the web client will be built from them.

