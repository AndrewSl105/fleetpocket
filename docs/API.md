# Fleetpocket API

Everything a client (the web app / Telegram Mini App) needs. The machine-readable contract is [`openapi.yaml`](openapi.yaml); this file explains behaviour.

## Conventions

- Base path `/api/v1`. JSON request and response bodies (`Content-Type: application/json`). Unknown request fields are rejected. `POST /tasks` and `POST /tasks/{id}/message` also accept `multipart/form-data` to attach files (see [Attachments](#attachments)).
- **Auth**: a session cookie `fleetpocket_session` (`HttpOnly`, `Secure`, `SameSite=Lax`, 30-day sliding expiry). Every endpoint except `auth/login`, `auth/telegram`, `auth/logout` and `setup/*` requires it; without it you get `401 unauthorized`. **Telegram sessions** may instead send `Authorization: Bearer <token>` (the `token` from `auth/telegram`), because Telegram Web embeds the Mini App in a cross-site iframe where a `SameSite=Lax` cookie is never sent. Browser (`web`) sessions are cookie-only.
- **CSRF**: every non-GET request must send `X-Requested-With: fleetpocket`, otherwise `403 csrf`.
- **Errors**: `{"error": {"code": "<machine code>", "message": "<human text>"}}`.

  | HTTP | code | when |
  |---|---|---|
  | 400 | `invalid_json`, `invalid` | malformed body, validation failure |
  | 401 | `unauthorized`, `bad_credentials` | no session; wrong login |
  | 403 | `csrf`, `forbidden`, `bad_setup_code` | missing header; `/metrics` from outside; wrong setup code |
  | 404 | `not_found` | unknown id or endpoint |
  | 409 | `conflict`, `already_answered`, `in_use`, `not_running`, `no_workspace`, `not_mergeable`, `setup_required`, `setup_done`, `setup_out_of_order` | action not allowed in the current state |
  | 410 | `gone` | the attachment was removed |
  | 429 | `rate_limited` | 5 failed logins per IP per 15 minutes |
  | 503 | `github_disabled`, `telegram_disabled` | integration not configured |
  | 500 | `internal` | bug; details are in the server log |

- **Pagination**: list endpoints take `?limit=` (default 50, max 200) and `?cursor=`; responses are `{"items": [...], "next_cursor": "..."}`. An empty `next_cursor` means the last page. Pass it back unchanged (for `GET /tasks/{id}/events?before_id=`, pass it back as `before_id`).
- Times are RFC 3339. Ids: projects use a slug (`vatary`); everything else is an integer.

## Auth

| | |
|---|---|
| `POST /auth/login` `{password, totp}` | password **and** current 6-digit TOTP code → sets the cookie. `{"ok":true,"kind":"web"}` |
| `POST /auth/telegram` `{init_data}` | Mini App: pass `Telegram.WebApp.initData` verbatim. Valid for 1 hour after `auth_date`, only for the owner's Telegram user id → sets the cookie and returns `{"ok":true,"kind":"telegram","token":"…"}`. Keep `token` in memory only (never `localStorage`) and send it as `Authorization: Bearer`; it is the same session as the cookie |
| `POST /auth/logout` | deletes the session (cookie or bearer token) and clears the cookie |
| `GET /setup/status` | `{"setup_required":bool,"github_configured":bool}`, no session needed |
| `POST /setup/owner` `{code, password}` | setup mode only. `code` is the one-time setup code the server logs at WARN on start (with the link `${PUBLIC_BASE_URL}/setup?code=…`; a restart makes a new one); `password` ≥ 12 characters. Returns `{"otpauth_uri","secret"}`; nothing is saved yet. Wrong code → `403 bad_setup_code` (counts toward the login rate limit); owner exists → `409 setup_done` |
| `POST /setup/owner/confirm` `{code, totp}` | verifies the first TOTP code of the pending owner, saves it, leaves setup mode and logs in like `auth/login`. `409 setup_out_of_order` before `setup/owner` |
| `POST /setup/github/manifest` `{organization?}` | session required. Returns `{"action_url","manifest"}`: POST the manifest (JSON text, form field `manifest`) to `action_url` (GitHub, personal account or `organizations/<org>`). `409 github_configured` if an App exists |
| `GET /setup/github/callback?code&state` | GitHub's `redirect_url`. Checks `state` (one use, this session, 1 hour), converts `code` (`POST /app-manifests/{code}/conversions`), saves App ID + webhook secret, writes the key to `GITHUB_APP_PRIVATE_KEY_PATH`, redirects (303) to the App's installation page and restarts the server to enable it. Failures redirect to `/setup?step=github&github_error=<reason>` |
| `GET /me` | `{"ok":true,"kind":"web"\|"telegram","agents":["claude","codex"],"default_agent":"claude","mode":"server"\|"local"}`; use it as the session check on app start. `mode` is `local` for `npx jungle-monkey` (local repositories, optional GitHub). `agents` are the coding agents with credentials on the server (`claude`, `codex`, `cursor`), the ones a task may use |

**Setup mode.** When no owner login exists (neither `ADMIN_PASSWORD_HASH` + `ADMIN_TOTP_SECRET` in the server configuration nor one saved by the wizard), every route except `/healthz`, `/webhooks/*` and `/api/v1/setup/*` answers `409 setup_required`.

## Projects and repositories

A **project** groups repositories and holds run settings. All override fields are nullable: `null` means "use the server default". In `PATCH`, an omitted field is unchanged; `""` or `0` resets an override to the default.

```json
{
  "id": "vatary", "name": "Vatary",
  "instructions": "appended to Claude's system prompt",
  "services": ["postgres", "redis"],
  "env": {"NODE_ENV": "test"},
  "runner_image": null, "agent": null, "node": null, "model": null, "max_turns": null, "max_budget_usd": null,
  "max_duration_sec": null, "runner_memory": null, "runner_cpus": null,
  "telegram_thread_id": null, "archived": false,
  "ci_autofix": {"enabled": null, "max_attempts": null, "checks": null, "budget_usd": null, "allow_ci_changes": null},
  "budget": null, "month_cost_usd": 12.34,
  "created_at": "…", "updated_at": "…",
  "repositories": [{"id": 1, "project_id": "vatary", "full_name": "andrewsl105/vatary-server",
                    "base_branch": "dev", "installation_id": 123, "is_default": true,
                    "source": "github", "path": "", "remote_url": ""}]
}
```

| | |
|---|---|
| `GET /projects?archived=true` | list (archived hidden unless asked), ordered by id |
| `POST /projects` | create; body = project fields plus `id` (lowercase slug, max 40). `409` if the id exists |
| `GET /projects/{id}` | one project with repositories |
| `PATCH /projects/{id}` | update any writable field |
| `DELETE /projects/{id}` | archive (sets `archived: true`; nothing is deleted) |
| `POST /projects/{id}/repositories` `{full_name, installation_id, base_branch?, is_default?}` | add a repo. `base_branch` defaults to `dev`. The first repo, or one with `is_default`, becomes the default |
| `PATCH /projects/{id}/repositories/{repoId}` `{base_branch?, is_default?}` | omitted fields are unchanged; returns the repository. `is_default: true` makes it the project's only default; `is_default: false` on the current default is `409 conflict` (make another repo the default instead). Changing `base_branch` is `409 in_use` while tasks that are not `completed`/`failed`/`cancelled` use the repo |
| `DELETE /projects/{id}/repositories/{repoId}` | remove; `409 in_use` while tasks reference it |
| `GET /github/repositories` | repos the GitHub App can see, for the "add repo" picker: `{"items":[{full_name, default_branch, private, installation_id}]}` |

`base_branch` must be a plain git branch name (letters, digits, `._/-`, not starting with `-` or `.`, no `..`); otherwise `400 invalid`.

`agent` is `claude`, `codex` or `cursor` (`null`: the server's `default_agent`); it may name an agent without credentials, but creating a task with it fails. `model` is a model of the project's agent and applies only to tasks that run with that agent; a task that overrides the agent uses that agent's default model (Claude: the server's `DEFAULT_MODEL`).

`node` is the computer the project's tasks run on (a node id, see [Computers](#computers-nodes)); `null`, `""` or `"server"` is the server. An unknown id is `400 invalid`; removing the node resets it to `null`.

`ci_autofix` holds the project's CI auto-fix overrides (see [CI auto-fix](#ci-auto-fix)); a `PATCH` replaces the object as a whole and `null` fields inherit the global default.

`budget` is the project's monthly cap, `{"monthly_usd": 50}` or `null` (none; `monthly_usd` `0` or `null` in a `PATCH` removes it); see [Costs and budgets](#costs-and-budgets). `month_cost_usd` (read-only) is the cost of the project's runs started this calendar month; `month_cost_unknown` says some of them have no price.

`services` accepts `postgres` and `redis`. `env` keys must match `[A-Z_][A-Z0-9_]*`; auth and service variables (`DATABASE_URL`, `REDIS_URL`, Claude, OpenAI and Cursor keys, `CODEX_HOME`, …) are reserved.

## Tasks

```json
{
  "id": 12, "project_id": "vatary", "repository_id": 1,
  "title": "Add CSV export to Tables", "prompt": "…",
  "status": "running", "status_reason": null,
  "branch": "agent/12-add-csv-export-to-tables", "session_id": "…",
  "pr_number": null, "pr_url": null, "ci_status": null,
  "cost_usd_total": 0.42, "turns": 17, "source": "web", "agent": "claude", "node": null,
  "created_at": "…", "updated_at": "…", "finished_at": null, "paused_at": null,
  "last_activity": "Edit src/tables/export.ts", "last_activity_at": "…",
  "queued_message": null,
  "autofix_cost_usd": 0, "cost_estimated": false, "cost_api_equivalent": true, "cost_unknown": false,
  "budget_wait": null, "budget_override_at": null,
  "schedule_id": null, "mcp_servers": null, "run_budget_usd": null,
  "loop": null, "loop_state": null, "loop_iteration": 0,
  "local_branch": null, "start_from": "last_commit"
}
```

- `local_branch`, `start_from`: local repositories only (see [Local repositories](#local-repositories)). `local_branch` is the branch delivered into the owner's repository (`null` before delivery and after Discard); `start_from` is `last_commit` (default) or `working_tree`.
- `agent`: the coding agent, fixed when the task is created (the request's `agent`, else the project's, else the server default), so follow-ups and resumes continue the same session.
- `node`: the computer the task runs on (`null`: the server), fixed when the task is created: its workspace, agent session and containers live there. While that node is offline the task waits (`queued` tasks stay queued; a live task keeps its status and its stream resumes when the node reconnects; actions that need the workspace answer `503 host_offline`).
- `cost_usd_total`: the sum of the runs' `cost_usd`: reported by Claude, estimated from the price table for Codex and Cursor (see [Costs and budgets](#costs-and-budgets)). `cost_estimated` is true when some run's cost is an estimate (show `≈$0.84`); `cost_api_equivalent` when some Claude run used a subscription (OAuth) login, so the cost is what the API would charge, not a bill. `autofix_cost_usd` is the part spent by CI auto-fix runs. `cost_unknown` is true when some run reported tokens but its model has no price (`cost_source: unknown`, counted as 0): the total is a lower bound. Clients show one format everywhere: `$0.84`, `≈$0.84` (estimate included), `$?` (nothing known), `$0.84+?` / `≈$0.84+?` (part unknown).
- `source`: `web`, `telegram`, `api`, or `schedule` (fired by the schedule `schedule_id`, which is `null` again once that schedule is deleted). A scheduled task carries what its schedule gives it: `mcp_servers` (names; `null`: all of the project's servers) and `run_budget_usd` (each run's budget; `null`: the project's).
- `loop`: `{check_command, max_iterations, budget_usd, timeout_sec?}` when the task loops until a check passes (see [Loops](#loops)), else `null`. `loop_state`: `running`, or why the loop ended (`passed`, `max_iterations`, `budget`, `no_changes`, `stopped`); `loop_iteration`: the current iteration (0 without a loop).
- `budget_wait`: `global`, `project` or `schedule` while a queued task waits because that monthly cap is used up (`status_reason` is `waiting: budget`), else `null`. `budget_override_at`: when the owner let it start anyway.
- `turns`: agent turns across all runs of the task (sum of each run's last reported `usage.turns`).
- `last_activity`: one line (max 120 characters) describing the latest thing that happened: the latest `tool_use` summary, or the latest status change as `<status>` / `<status>: <reason>`, whichever came last. `last_activity_at` is when it was set. Both are `null` only for tasks that predate the field and have no surviving events.
- `queued_message`: text sent with `POST …/message` while a run was active (several messages are joined by a blank line), or `null`. It becomes the input of the next run as soon as the current run ends.

`GET /tasks/{id}` and all task actions return this object plus `repository`, `latest_run` (a run as in `GET /tasks/{id}/runs`, or `null`) and `open_question` (`{id, text, options, created_at, …}` or `null`).

### Status

| status | meaning | actions available |
|---|---|---|
| `queued` | waiting for a slot on its host (or for its node to come online, or for a monthly budget: `budget_wait`) | message (queued), cancel, budget override (when `budget_wait` is set) |
| `preparing` | cloning, creating branch and sidecars | message (queued), cancel |
| `running` | Claude is working | message (queued), interrupt, cancel |
| `waiting_input` | Claude asked a question (`open_question`) | answer, message (queued), interrupt, cancel |
| `paused` | question not answered in time; containers removed | answer (resumes), message (resumes), cancel |
| `pushing` | validating diff, pushing, opening the PR | message (queued), cancel |
| `pr_open` | PR exists; `ci_status` follows CI. With CI auto-fix, a held fix leaves an `open_question` here | merge, message (new run on the same branch), answer (the auto-fix question), cancel |
| `branch_ready` | local repositories (local mode): the task branch was delivered into the owner's repository as `local_branch` (`jm/<id>-<slug>`); mirrors `pr_open` | merge into current branch, check out, discard, push & open PR (GitHub remote + GitHub connected), message (new run on the same branch, re-delivered), cancel |
| `completed` | PR merged/closed, branch merged or discarded, or finished with no changes | message (new run) |
| `failed` | see `status_reason` | retry |
| `cancelled` | | retry |

### Endpoints

| | |
|---|---|
| `GET /tasks?project=&status=` | newest first. `status` takes a comma-separated list |
| `POST /tasks` `{project_id, prompt, repository_id?, title?, agent?, node?, output?, skills_add?, skills_remove?, loop?}` | `loop` (`{check_command, max_iterations?, budget_usd?, timeout_sec?}`; in multipart a JSON string) loops the task until the check passes ([Loops](#loops)). `output` (`pr`, `artifacts`, `pr_and_artifacts`) defaults to the project's `default_output`, else `pr`; values other than `pr` are refused with `400` until diagram collection ships. `skills_add`/`skills_remove` (slugs; comma-separated in multipart) change the project's skills for this task. `repository_id` defaults to the project's default repo; `title` to the first line of the prompt (80 chars); `agent` to the project's agent; `node` (`"server"` or a node id) to the project's host. `400 invalid` when the agent has no credentials on that host (for a node: the agents it reported when it last connected) or the node is unknown. As multipart, add images in `files[]` |
| `GET /tasks/{id}` | task view (see above) |
| `GET /tasks/{id}/events?after_id=` | timeline, oldest first; `after_id` (or `cursor`) is the last event id you have. Heartbeats are not returned |
| `GET /tasks/{id}/events?before_id=` | timeline backwards, **newest first**: events older than `before_id`; `before_id=0` starts at the newest event. Pass `next_cursor` back as `before_id` (not as `cursor`, which means `after_id`). Combining `before_id` with `after_id`/`cursor` is `400 invalid` |
| `GET /tasks/{id}/runs` | the task's runs, oldest first, including a queued one (`started_at: null`): `{id, task_id, kind, input, container_id, exit_code, last_seq, cost_usd, turns, autofix_attempt, loop_iteration, agent, model, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_source, auth, started_at, finished_at}`. `kind` is `initial`, `resume`, `followup`, `retry`, `restore` (a run from a checkpoint) or `loop` (the next loop iteration after a failed check); `loop_iteration` is the iteration a run belongs to (`null` without a loop); `cost_usd` and the token counts are the run's own (not the session's); `cost_source` is `reported`, `estimated`, `unknown` (tokens but no price, cost 0) or `null` (no usage yet, or a run from before it was recorded); `auth` is `subscription` (Claude OAuth: API-equivalent cost), `api_key` or `null`. Paginated with `limit`/`cursor` |
| `GET /tasks/{id}/diff` | `{"diff", "truncated", "base", "branch"}`: `git diff base...branch` from the workspace, capped at 1 MB. `409 no_workspace` before cloning or after cleanup; `503 host_offline` while the task's node is offline |
| `POST /tasks/{id}/answer` `{question_id, text}` | the first answer wins; a second gets `409 already_answered`. Answering a `paused` task resumes it. The CI auto-fix question (`…-autofix_guard`): `Push anyway` pushes the held fix, anything else discards it |
| `POST /tasks/{id}/message` `{text}` (multipart: plus `files[]`) | `queued`/`preparing`/`running`/`waiting_input`/`pushing`: **queued** (a run handles one prompt; nothing is injected into a live session). The text is kept in `queued_message` and starts the next run (same branch, same session) as soon as the current run ends: on `pr_open`/`branch_ready`/`completed` as a follow-up, on `paused` as a resume. If the task fails or is cancelled first, the text is dropped and a `message_dropped` event shows it. `paused`/`pr_open`/`branch_ready`/`completed`: starts a new run right away. Otherwise `409` |
| `POST /tasks/{id}/interrupt` | `running`/`waiting_input`: stops Claude and **ends the run** ("Interrupted by the user."); what it committed is pushed as for any finished run, and the session is kept. A queued message then starts the next run; otherwise send a follow-up message |
| `POST /tasks/{id}/cancel` | ends the task (`cancelled`), stops the runner |
| `POST /tasks/{id}/retry` | `failed`/`cancelled` only: queues the original prompt again on the same branch |
| `POST /tasks/{id}/budget-override` | `queued` only: the task starts although a monthly cap holds it, and may start runs until the calendar month ends; records a `budget` event (`override`, `via: "web"`). Otherwise `409` |
| `POST /tasks/{id}/merge` | `pr_open` only: squash-merges the PR (commit title `<task title> (#<number>)`), deletes its branch, records `pr_closed {merged: true}` and completes the task ("PR merged"). `409 not_mergeable` with GitHub's reason when GitHub refuses (conflicts, required checks or reviews); `503 github_disabled` without the GitHub App |
| `GET /tasks/{id}/attachments/{attachmentId}` | the image or file (`?thumb=1`: JPEG, longest side ≤ 480 px, images only); `410 gone` once removed |
| `GET /stats` | `{running, queued, waiting_input, cost_usd_today, cost_usd_month, cost_unknown_today, cost_unknown_month}` (server-local days; `cost_unknown_*`: some runs have no price) |
| `GET /settings/ci-autofix` | the global CI auto-fix default `{enabled, max_attempts, checks, budget_usd, allow_ci_changes}` (null fields: built-in default) |
| `PUT /settings/ci-autofix` | replaces it; `400 invalid` for `max_attempts` outside 1–5 or a negative budget |
| `GET /settings/costs` | `{monthly_budget_usd, price_table: {updated, prices: [{agent, model, input_usd, cached_input_usd, cache_write_usd, output_usd}]}, price_table_is_default}` |
| `PUT /settings/costs` `{monthly_budget_usd, price_table}` | replaces both; `monthly_budget_usd` `null`/`0` removes the global cap, `price_table: null` restores the built-in table. `400 invalid` for an unknown agent, an empty model, a negative price or a model listed twice |
| `GET /costs?from=&to=&period=` | the cost report, see [Costs and budgets](#costs-and-budgets) |
| `GET /settings/checkpoints` | `{enabled}`: whether runners take checkpoints (default `false`) |
| `PUT /settings/checkpoints` `{enabled}` | turns them on or off for the runs that start from now on |
| `GET /tasks/{id}/checkpoints` | `{items}`, see [Checkpoints](#checkpoints) |
| `GET /tasks/{id}/checkpoints/{checkpointId}` | the restore preview: the checkpoint plus `{discarded: {runs, steps, messages, commits}, diff, truncated}` |
| `POST /tasks/{id}/checkpoints/{checkpointId}/restore` `{instruction?}` | rolls the task back to the checkpoint and continues; returns the task view (`queued`). `409` while the task is busy, once its PR is closed, or after `WORKSPACE_RETENTION` |

### Timeline events

`GET …/events` items:

```json
{"id": 57, "task_id": 12, "run_id": 3, "seq": 14, "type": "tool_use",
 "payload": {"type": "tool_use", "tool": "Edit", "summary": "Edit src/tables/export.ts", "ts": "…", "seq": 14},
 "created_at": "…"}
```

Every event type has a typed schema in `openapi.yaml`: `Event` is a `oneOf` discriminated by the envelope's `type` (`ToolUseEvent`, `StatusEvent`, …), each with its own `payload` schema (`ToolUsePayload`, `StatusPayload`, …). Generate client types from it; `switch (event.type)` narrows the payload.

- **Runner events** have `seq` (and `run_id`) set and `payload` is the runner event exactly as described in [PROTOCOL.md](PROTOCOL.md), including its own `type`, `ts` and `seq`: `ready`, `started`, `assistant_text`, `tool_use`, `tool_result`, `question`, `usage`, `committed`, `checkpoint`, `check`, `done`, `paused`, `error`. A `check` payload's `output` is trimmed by the server (like CI logs) and carries `iteration` and `max_iterations`. In a `question` payload, `question_id` is the runner-local id (`q1`); the API question id (for `/answer`) is `<run_id>-<question_id>`, as in `open_question.id`.
- **Backend events** have `seq: null`; their payload has no `type`, `ts` or `seq` (use the envelope's `type` and `created_at`):

  | type | payload |
  |---|---|
  | `created` | `{attachments}`: the task was created, with the files attached to it (`[]` when none). Tasks created before this event existed have none |
  | `status` | `{status, reason?}` |
  | `answer` | `{question_id, text, answered_via}` (`web` or `telegram`) |
  | `message` | `{text, delivery: "queued"\|"new_run", attachments?}` (`"injected"` only in timelines recorded before messages were queued; `attachments` only when files were sent) |
  | `message_dropped` | `{text, reason: "failed"\|"cancelled"}`: a queued message that never started a run |
  | `interrupt` | `{}` |
  | `run_started` | `{run_id, kind}` (`initial`, `resume`, `followup`, `retry`, `restore`, `loop`) |
  | `run_finished` | `{run_id, exit_code, cost_usd?, cost_source?, auth?}` (the cost fields once the run reported usage) |
  | `pr_opened` | `{number, url}` |
  | `pr_updated` | `{number, url}` (a later run pushed to the same PR) |
  | `pr_closed` | `{number, merged}` |
  | `ci` | `{status: "pending"\|"success"\|"failure", name, url}` |
  | `diff_rejected` | `{reason}` |
  | `autofix` | `{phase, …}`: one CI auto-fix step, see below |
  | `question` | only for the CI auto-fix question: `{question_id: "autofix_guard", text, options}` with the attempt's `run_id` |
  | `integrations` | `{servers, egress, warning?}`: the MCP servers and egress mode a run got (only when it has servers or an allowlist) |
  | `mcp_call` | `{server, tool, ok, duration_ms, args_bytes, result_bytes, denied?, error?}`: one call through the MCP gateway (`denied`: `denied` = tool not allowed, `limit` = `max_calls` reached; `tool: ""` = connection failed). Never arguments, results or credentials |
  | `egress_blocked` | `{host}`: the egress proxy refused a host (once per host and run); also sent to Telegram with an "Allow for this project" button |
  | `loop` | `{phase: "iteration"\|"stopped", iteration, max_iterations?, check_command?, run_id?, exit_code?, timed_out?, state?, reason?, spent_usd?}`: see [Loops](#loops) |
  | `budget` | `{phase, scope?, project_id?, schedule_id?, schedule?, level?, spent_usd?, limit_usd?, via?}`: a monthly budget step, see [Costs and budgets](#costs-and-budgets) |
  | `checkpoint_restored` | `{checkpoint_id, checkpoint_event_id?, n, run_id, restore_run_id, ref_sha, mode: "fork"\|"summary", instruction, via}`: the owner rolled back; the events between `checkpoint_event_id` and this one are the abandoned branch |
  | `branch_ready` | `{branch, commits, diffstat, updated, previous?}`: the task branch reached the owner's local repository (`updated`: a follow-up fast-forwarded it; `previous`: the update went to a new branch because the old one was checked out or had moved) |
  | `branch_action` | `{action: "merged"\|"checked_out"\|"discarded"\|"pushed", branch, into?, fast_forward?}`: what the owner did with a local task branch |
  | `branch_force_pushed` | `{branch, from, reason: "restore", number}`: the push after a restore rewrote the task branch with `--force-with-lease` (also a PR comment and a Telegram message) |

### CI auto-fix

Off by default. When it is on (globally via `PUT /settings/ci-autofix` or per project via `ci_autofix`) and a check fails on the commit Jungle Monkey last pushed to a task's PR, the server fetches the failed checks' logs, attaches them (trimmed) to the task and queues a follow-up run that fixes the cause (`latest_run.autofix_attempt` is set). Task fields: `autofix_state` (`fixing`, `held`, `approved`, `waiting`, `green`, `needs_you`, `cancelled`, or `null`), `autofix_attempts`, `autofix_max_attempts`.

`autofix` event phases: `started` `{attempt, max_attempts, run_id, checks: [{name, url, conclusion}], signature, head_sha, attachments}`; `pushed` `{attempt, max_attempts}`; `held` `{attempt, max_attempts, reasons}` (the fix changes CI or tests; an open question asks "Push anyway?"); `discarded`; `green` `{fixes}`; `needs_you` `{reason, attempt, max_attempts, checks?, signature?}`; `cancelled` `{reason}` (a human push, a follow-up message, the PR closed).

### Costs and budgets

Every run records its agent, model, tokens (`input_tokens` includes cache reads and writes) and `cost_source`. Claude reports its cost (`reported`); Codex and Cursor report tokens only, so their cost is `estimated` from the price table (`GET/PUT /settings/costs`; matched by agent and model, `*` for any model of an agent) or `unknown` (cost 0) when no row matches. The built-in table lists OpenAI's API prices for Codex models; Codex's `default` model and Cursor's `auto` route to different models, so they need a row the owner adds. Estimates count toward budgets.

Budgets, all off by default: the per-run `max_budget_usd` (as before), a monthly cap per project (`budget`), per schedule (the schedule's `monthly_budget_usd`, holding only the tasks it fired) and a global monthly cap (`monthly_budget_usd`). Months are calendar months in the server's local time, by run start. When a scope reaches 80% of its cap this month, a `budget` event (`warning`) goes on the task whose run crossed it and to Telegram; at 100% (`reached`) running runs **finish** (a monthly cap never stops a run) and new runs of that scope wait: their queued tasks get `budget_wait` and a `budget` event (`waiting`), sent to Telegram with a "Start anyway" button. Each notice is sent once per scope, month and level. `POST /tasks/{id}/budget-override` (or the button) lets that task start anyway (`override`). Raising or removing a cap, or a new month, releases waiting tasks.

`GET /costs?from=YYYY-MM-DD&to=YYYY-MM-DD&period=day|week|month` (default: the first of this month to today, by day; at most 400 days) returns:

```json
{"from": "2026-11-01", "to": "2026-11-17", "period": "day", "month_start": "2026-11-01",
 "budget": {"monthly_usd": 100, "month_cost_usd": 41.2, "month_cost_unknown": false},
 "projects": [{"project_id": "vatary", "name": "Vatary", "monthly_usd": 50, "month_cost_usd": 30.5, "month_cost_unknown": false}],
 "total": {"cost_usd": 41.2, "estimated_usd": 3.1, "autofix_usd": 1.9, "api_equivalent_usd": 38.1,
           "runs": 57, "unknown_runs": 1, "input_tokens": 9100000, "output_tokens": 410000},
 "by_project": [{"key": "vatary", "cost_usd": 30.5, …}], "by_agent": [{"key": "claude", …}],
 "by_period": [{"start": "2026-11-01", "cost_usd": 2.4, …}, …],
 "rows": [{"day": "2026-11-01", "project_id": "vatary", "agent": "claude", "cost_usd": 2.4, …}]}
```

Every sum has the fields of `total`. `by_period` lists every day, week (from Monday) or month of the range, empty ones included; `rows` is one row per day, project and agent (the web client's CSV export).

### Loops

A task created with `loop` runs in iterations (SPEC-server, "Loops"). Each iteration is an agent run; when it ends `done`, the runner commits, takes an `iteration` checkpoint (when checkpoints are on) and runs `check_command` in the same container (`check` event: `exit_code`, the trimmed `output`, `iteration`, `max_iterations`). Exit code 0 passes. Otherwise the next iteration is queued as a `loop` run that continues the session with the check output as its prompt (`loop` event, phase `iteration`). The loop stops (`loop` event, phase `stopped`, and `loop_state`) when the check passes (`passed`), after `max_iterations` (default 3, max 10: `max_iterations`), when the loop's runs cost `budget_usd` (`budget`; each iteration's run budget is what is left), after two iterations in a row without file changes (`no_changes`), or on a run limit, a missing check result, a failure or a cancel (`stopped`). Nothing is pushed between iterations: the PR is opened (or updated) once, when the loop stops. A retry starts the loop over; follow-ups after it ended are ordinary runs without a check.

### Checkpoints

Off by default (`PUT /settings/checkpoints {"enabled": true}`). While on, every run commits the working tree to hidden refs in the task workspace after each step that changed files and before each question; each shows in the timeline as a `checkpoint` runner event and in `GET /tasks/{id}/checkpoints`:

```json
{"items": [{"id": 4, "task_id": 12, "run_id": 3, "event_id": 57, "n": 2, "ref_sha": "…", "turn": 5,
            "files_changed": 2, "reason": "turn", "created_at": "…",
            "ref": "refs/jm/checkpoints/3/2", "mode": "fork", "available": true}]}
```

`mode` is how the agent continues after a restore: `fork` (Claude resumes its session at that point as a new fork) or `summary` (Codex, Cursor: a new session whose prompt summarises the timeline up to the checkpoint). `available` is `false`, with `unavailable_reason`, while the task is busy, once its PR is closed, and after `WORKSPACE_RETENTION` removed the workspace and the refs with it (the preview also reports a checkpoint whose commit is gone).

`POST …/restore` queues a `restore` run: the scheduler resets the workspace to the checkpoint on the task branch (HEAD back to the commit the checkpoint was taken on, its files uncommitted), then the agent continues with `instruction`. Later events stay in the timeline; clients show the events between the checkpoint and the `checkpoint_restored` event as an abandoned branch. If commits after the checkpoint were already pushed, the push at the end of the run rewrites the branch with `--force-with-lease` (only `agent/<id>…` branches Jungle Monkey created; any other branch fails the task instead), comments on the PR and records `branch_force_pushed`.

### Attachments

Images and other files go with a task's prompt or with a message. Send the request as `multipart/form-data`: the same fields as the JSON body (`project_id`, `repository_id`, `title`, `prompt`, or `text`) plus up to 10 files in `files[]`. The text is still required.

- Any file is accepted, at most 40 MB per file and 100 MB per request. Images are PNG, JPEG, WebP and GIF detected from the content (name and declared type are ignored), at most 50 megapixels; an image that cannot be decoded, and animated GIF and WebP, are `400 invalid` with a message naming the file. Other content is an ordinary file.
- Image metadata is removed before storing: JPEG, PNG and GIF are re-encoded (a JPEG's EXIF orientation is applied to the pixels first); WebP loses its EXIF and XMP chunks. `size`, `width` and `height` describe the stored file. Other files are stored exactly as uploaded, with `mime` sniffed from the content (informational only), `width` and `height` 0 and no thumbnail; clients tell images from files by `mime` (the four image types).
- Each attachment is `{id, name, mime, size, width, height}`. Task attachments are listed in the task's `created` event, message attachments in its `message` event.
- Download with `GET /tasks/{id}/attachments/{attachmentId}` (session required like every endpoint; `?thumb=1` for an image's thumbnail). Files that are not images are served as `application/octet-stream` with `Content-Disposition: attachment`. Telegram Web clients send the bearer token, so fetch the image and show it from a blob URL rather than an `<img src>`.
- The agent sees the files of the input it is working on (the prompt, or the messages a run starts with) at `/attachments/<id>.<ext>`, plus the task's earlier files. A file's `<ext>` is its name's extension (lowercase alphanumeric, at most 10 characters) or `bin`. They are removed (`410 gone`, files deleted) when the task becomes `completed`, and with the workspace for `failed` and `cancelled` tasks (`WORKSPACE_RETENTION`), so Retry and follow-ups still have them until then. Files of a queued message that is dropped (`message_dropped`) are removed with it.

## Local repositories

Local mode only (`npx jungle-monkey`, `MODE=local`). A repository with
`source: "local"` is a folder on the owner's disk: `path` is its absolute work
tree, `base_branch` the branch detected when it was added, `remote_url` its
`origin` remote (informational) and `installation_id` 0. In server mode the API
refuses local repositories (`400 local_mode_only`).

A task on a local repository works in a copy (`git clone --no-hardlinks
--local`); the original folder is never mounted into the runner. When a run
ends with commits, the branch is fetched into the original repository as
`jm/<id>-<slug>` (`-2`, `-3`… when the name is taken) and the task becomes
`branch_ready` (event `branch_ready`). The owner's checked-out branch, index and
working tree are never touched by delivery.

| | |
|---|---|
| `GET /local/repos/suggestions` | `{items: [{name, path, branch, dirty, last_commit, added}], start}`: git work trees under `~/code`, `~/Projects`, `~/projects`, `~/dev`, `~/src`, `~/repos`, `~/Documents/GitHub`, `~/work` and the CLI's start directory (`start`, when it is a repository), 3 levels deep, skipping `node_modules`, hidden folders and symlink loops. At most 200 within 2 s, newest commit first, cached 60 s. `dirty` = uncommitted changes to tracked files |
| `GET /local/fs?path=` | folder browser: `{path, parent, home, is_git, entries: [{name, path, is_git}]}`, subfolders only. `path` defaults to the home directory; its real path must stay inside it (`403 outside_home`) |
| `POST /local/repos` `{path, project_id?}` | adds a work tree (top level, not bare, has a commit, not inside the data directory; else `400 invalid`) as a repository, in a new project named after the folder or in `project_id`. Idempotent: `200` with `created: false` when already added, `201` otherwise; body `{project, repository, created}`. Also takes the CLI's control token (`jungle-monkey add`) |
| `POST /projects/{id}/repositories` `{source: "local", path}` | the same, into project `id`; returns the repository |
| `GET /local/repos/{repoId}/status` | `{repository_id, path, missing, branch, detached, dirty}` for the project page; `missing` offers "Locate…" |
| `POST /local/repos/{repoId}/locate` `{path}` | the folder moved: points the repository at it; deliveries waiting for it (`pushing`, reason `repository folder not found: …`) resume |
| `POST /tasks/{id}/branch/merge` | `branch_ready` only: merges `local_branch` into the owner's current branch (fast-forward when possible, else a merge commit) and completes the task. `409`: `dirty`, `detached`, `current_branch`, `merge_conflict` (aborted; `error.files` lists the files), `git_refused` (e.g. untracked files would be overwritten; nothing changed), `branch_gone`, `repo_missing` |
| `POST /tasks/{id}/branch/checkout` | checks `local_branch` out (`409 dirty` on uncommitted changes); the task stays `branch_ready` |
| `POST /tasks/{id}/branch/discard` | deletes `local_branch` (`409 current_branch` while it is checked out) and completes the task |
| `POST /tasks/{id}/branch/push` | "Push & open PR": needs a GitHub `origin` remote (`400 no_github_remote`) and GitHub connected (`503 github_disabled`). Pushes `agent/<id>-<slug>` with the owner's token (held by the server process, never the runner), opens the PR, and the task becomes `pr_open` with the usual CI and auto-fix flow. On failure it returns to `branch_ready` |

| `GET /local/github` | `{connected, login, scopes, error, device_flow, gh_cli}`: GitHub is optional in local mode (only "Push & open PR" and the PR flow after it need it) |
| `POST /local/github/device` | starts the OAuth device flow (scope `repo`): `{user_code, verification_uri, expires_in, interval}`; `502 github` without a client id or when GitHub refuses |
| `POST /local/github/device/poll` | `{status: pending\|connected\|expired\|denied\|none, github}`; poll every `interval` seconds (the server also enforces it and `slow_down`) |
| `POST /local/github/gh` | imports `gh auth token` (the web app asks first) |
| `POST /local/github/token` `{token}` | stores a pasted fine-grained PAT after `GET /user` accepts it |
| `DELETE /local/github` | forgets the token |

The token lives in the OS keychain (service `jungle-monkey`, a 0600 file when there is none) and is used only by the server process; runners never get it. Without webhooks, the PRs of `pr_open` tasks are polled every 60 s (conditional requests with ETags, backing off to 15 min on errors) and fed into the same code as `POST /webhooks/github`: CI status, CI auto-fix, PR merged or closed.

`GET /auth/local?code=…&next=/projects/<id>` (the CLI's one-time link) lands on `next` when it is an app path.

`POST /tasks` takes `start_from`: `last_commit` (default) or `working_tree` (local repositories only): the copy starts from the commit checked out in the folder plus its uncommitted changes and untracked, not ignored files, committed as `WIP: working tree at start`.

A follow-up on a `branch_ready` task continues the same task branch and re-delivers it: `local_branch` is fast-forwarded, or, when it is checked out or the owner committed on it, the update goes to a new `-2`… branch (`branch_ready.previous`). Server mode answers every `/local` endpoint and `source: "local"` with `400 local_mode_only`.

## Integrations (MCP servers and egress)

MCP servers reach the agent only through the server's MCP gateway; their credentials never reach the runner. They need `SECRETS_KEY` (encryption of the stored secrets): without it, writes answer `503 secrets_disabled` and runs get no MCP servers; everything else works.

| endpoint | |
|---|---|
| `GET /mcp-servers?project=<id>` | `{items}`: the project's servers, or the global ones without `?project` |
| `POST /mcp-servers` | create; body `MCPServerInput` (below), `201` with the server |
| `GET /mcp-servers/{id}` | one server |
| `PUT /mcp-servers/{id}` | replace its settings; `secrets` merge (`null` removes a key, keys not sent are kept) |
| `DELETE /mcp-servers/{id}` | `204` |
| `POST /mcp-servers/{id}/test` | connect as the gateway would and list tools: `{ok, error?, tools: [{name, description, allowed, read_only}]}` |
| `GET /projects/{id}/integrations` | `{secrets_enabled, egress, egress_supported, presets, servers, global_servers, global_server_ids, active, agents, gateway_url}`; `active` is what the next run gets |
| `PUT /projects/{id}/integrations` | `{egress?, global_server_ids?}`; fields left out are kept; applies from the next run |
| `POST /projects/{id}/egress/allow` `{host}` | add a host to the allowlist ("Allow for this project" on a blocked request); returns the egress policy |

A server: `{id, project_id (null: global), name, kind, transport, url, runtime, package, command, args, secret_hints, tools_mode, tools, read_only, max_calls, enabled, last_used_at, last_status, last_error, created_at, updated_at}`.

- `kind: "remote"`: `url` with `transport` `http` (Streamable HTTP, default) or `sse`; `secrets` are HTTP headers (`{"Authorization": "…"}`) or `{"bearer": "<token>"}`.
- `kind: "stdio"`: `runtime` `npm` (`npx -y <package> <args>`), `pip` (installed in a venv, runs `command` or the package name) or `image` (a Docker image with `args`); `secrets` become the sidecar's environment. It runs as a sidecar on the server's Docker, reachable only by the gateway over its stdio.
- `kind: "postgres"`: the bundled Postgres server (tools `query`, `list_tables`) with secret `DATABASE_URL`; `read_only` defaults to `true` and is enforced (one statement, read-only transaction, always rolled back).
- `secret_hints` maps each stored key to its last characters (`…abcd`); secret values are never returned. `tools_mode` `all`, `allow` or `deny` with `tools` (names or `*` patterns); `read_only` on other servers exposes only tools annotated read-only; `max_calls` caps calls per run.

Egress: `{mode: "open"|"allowlist", presets, hosts}`. `open` (default) is today's behaviour. In `allowlist` mode the task network is internal and the runner's `HTTP(S)_PROXY` points at an egress proxy that allows only the presets' hosts, `hosts` (`host`, `*.domain` = subdomains only, `host:port`; without a port 80 and 443) and the gateway. Presets: `agent` (the task's agent API), `npm`, `pypi`, `go`, `crates`, `dockerhub`, `github`. Tasks on nodes cannot use allowlist mode (they fail with an explanation). Tools that ignore the proxy variables cannot reach the network.

The gateway itself (`/api/mcp-gateway/mcp/<server>`, `/api/mcp-gateway/egress/blocked`) is for runners and egress proxies only; it takes the run's token, no session (PROTOCOL.md, "MCP servers").

## Computers (nodes)

A **node** is a computer that runs tasks next to the server, with its own Docker and its own agent credentials (`fleetpocket-node`, see docs/design/remote-nodes.md). Everything else (auth, history, the task list, GitHub) stays on the server.

```json
{"id": "macbook", "name": "MacBook Pro", "max_concurrent": 2, "agents": ["claude", "codex"],
 "platform": "darwin/arm64", "version": "1.4.0", "last_seen_at": "…", "created_at": "…", "updated_at": "…",
 "online": true, "active_tasks": 1}
```

| | |
|---|---|
| `GET /nodes` | `{"items": [node…]}` |
| `POST /nodes` `{id, name?}` | registers a node; `id` is a slug (`[a-z0-9-]`, max 32, not `server`). Returns `201 {"node", "token"}`: the token (`fpn_<id>.<secret>`) is shown only here. `409` if the id exists |
| `PATCH /nodes/{id}` `{name?, max_concurrent?}` | `max_concurrent` 1–32 (default 2): tasks the node runs at once. The server's own limit stays `MAX_CONCURRENT_TASKS` |
| `POST /nodes/{id}/token` | new token; the old one stops working for new connections |
| `DELETE /nodes/{id}` | `409 conflict` while unfinished tasks run on it; projects that used it go back to the server |
| `GET /node/connect` | the node's WebSocket, `Authorization: Bearer <token>`; not for clients |

**Pairing** (what `fleetpocket-node setup` uses instead of a copied token):

| | |
|---|---|
| `GET /node/install` | the install script: `curl -fsSL <server>/api/v1/node/install | sh` downloads `fleetpocket-node` (`NODE_DOWNLOAD_URL`) and runs its setup. No session |
| `POST /node/pair` `{hostname, platform, agents}` | the computer asks for a code → `201 {code, poll_id, expires_in, approve_url}`. No session; 5 waiting codes per IP, 20 in total (`429 rate_limited`); a code lives 10 minutes |
| `GET /node/pair/{poll_id}` | the computer waits: `{"status":"pending"}`, then once `{"status":"approved", node_id, name, token, runner_image}`; afterwards `404` |
| `GET /nodes/pair/{code}` | owner: the waiting computer `{code, hostname, platform, agents, ip, expires_at}`, shown before connecting it |
| `POST /nodes/pair` `{code, name?}` | owner: registers the node (id from the name, `-2`… when taken) and hands its token to the computer → `201` node. Codes ignore case and the dash |

`agents` and `platform`/`version` are what the node reported when it last connected. A node's tasks count only against its `max_concurrent`.

## Schedules

Recurring tasks (SPEC-server, "Schedules"). None exist by default, so nothing fires. Each firing creates an ordinary task (`source: "schedule"`, `schedule_id`, title = the schedule's name) with a `schedule` event naming the schedule and trigger, and a Telegram message; questions, CI auto-fix, costs, checkpoints and loops work as for any task.

| | |
|---|---|
| `GET /schedules` | `{items}`: every schedule by name: `{id, name, project_id, repository_id, prompt, agent, skills, mcp_servers, cron, timezone, run_budget_usd, monthly_budget_usd, concurrency, pr_only_if_changes, ends_at, loop, enabled, next_run_at, created_at, updated_at, month_spend_usd, busy, last_firing}` |
| `POST /schedules` `{name, project_id, prompt, cron, timezone?, repository_id?, agent?, skills?, mcp_servers?, run_budget_usd?, monthly_budget_usd?, concurrency?, pr_only_if_changes?, ends_at?, loop?, enabled?}` | `201` the schedule. `cron`: five fields or `@hourly`/`@daily`/`@weekly`/`@monthly`/`@yearly`, read in `timezone` (IANA, default `UTC`); `400` for anything else. `repository_id` `null`: the project's default; `agent` `null`: the project's; `skills`: slugs added to the project's; `mcp_servers` (names) `null`: all of the project's servers; `run_budget_usd`: each run's budget (`null`: the project's); `monthly_budget_usd`: a monthly cap for its tasks (budget scope `schedule`); `concurrency` `skip` (default) or `queue`; `pr_only_if_changes` (default `true`; `false`: a run without changes makes an empty commit so a PR is opened anyway); `ends_at`: no firings after it; `loop`: each task loops ([Loops](#loops)) |
| `GET /schedules/{id}` | the schedule |
| `PUT /schedules/{id}` | replaces it (same body as `POST`); the next run is computed from now |
| `DELETE /schedules/{id}` | `{ok: true}`; its tasks stay with `schedule_id: null` |
| `POST /schedules/{id}/enable` · `/disable` | resume (next run: the first firing after now; paused firings are not made up) or pause (`next_run_at: null`) |
| `POST /schedules/{id}/run` | fires it now (also when paused) with its concurrency policy; `201` the firing |
| `POST /schedules/{id}/duplicate` | `201` a paused copy named `<name> (copy)` |
| `GET /schedules/{id}/firings?limit=` | `{items}`, newest first: `{id, schedule_id, scheduled_for, trigger: "cron"\|"manual", outcome, reason, task_id, created_at, task_status, task_cost_usd, task_pr_url, loop_state}` |

Firing outcomes: `started` (a task was created), `queued` (policy `queue` while a task of the schedule is still running, i.e. `queued`, `preparing`, `running`, `waiting_input`, `paused` or `pushing`; it starts when that task leaves those states; at most one waits; a timed one is skipped if the schedule was paused meanwhile), `skipped` (policy `skip`, or a firing already waits), `missed` (the server was down for more than one interval at that time), `failed` (the task could not be created, e.g. the project was archived). The server checks every 20 seconds. After downtime it runs at most one late firing per schedule, and only if it is less than one interval late; then the next run is the first firing after now. Times in DST gaps fire right after the gap; repeated times fire once.

## Skills

A **skill** is a `SKILL.md` folder (Claude Agent Skills format) the runner writes into a task's workspace. A task gets the skills its project enables, plus those `enabled_by_default`, plus the task's `skills_add`, minus its `skills_remove`; `Task.skills` lists the slugs its last run used.

```json
{
  "id": 3, "slug": "db-schema", "name": "db-schema", "description": "Draw the database schema …",
  "body": "---\nname: db-schema\ndescription: …\n---\n# …", "files": [{"path": "ref/notes.md", "content": "…"}],
  "enabled_by_default": false, "projects": ["vatary"], "created_at": "…", "updated_at": "…"
}
```

| | |
|---|---|
| `GET /skills` | `{items}`, by slug |
| `POST /skills` `{slug, body, files?, enabled_by_default?}` | `name` and `description` come from the frontmatter (both required). `400 invalid`: slug not `^[a-z][a-z0-9-]{1,63}$`, more than 50 files or 1 MB in total (SKILL.md included), a binary file, a path that is absolute, contains `..` or a backslash or is under `.git`, or a secrets-scan finding (the publisher's gitleaks). `409 conflict` when the slug is taken |
| `GET /skills/{id}` · `PUT /skills/{id}` · `DELETE /skills/{id}` | `PUT` takes the same body as `POST`. Delete also removes it from every project |
| `POST /skills/{id}/duplicate` | copy as `<slug>-copy` (`-copy-2`, …), not enabled by default |
| `POST /skills/import` (multipart `file`, `slug?`) | a `SKILL.md`, or a `.zip` with `SKILL.md` at the root or in one folder (that folder's name is the slug unless `slug` is given). At most 2 MB. Same validation as `POST` |
| `GET /skills/{id}/export` | `application/zip`: `<slug>/SKILL.md` and the extra files |

Projects: `PATCH /projects/{id}` takes `skills` (slugs; replaces the list, unknown slugs ignored) and `default_output`; `Project` returns both.

`subagents` (boolean, default `true`) on `Project` and in `POST`/`PATCH /projects`: Claude runs of the project get the default subagents (`explorer`, `test-runner`, `reviewer`; SPEC-server "Subagents"). It has no effect on Codex or Cursor. Runner events from a subagent (`assistant_text`, `tool_use`, `tool_result`) carry `subagent: "<name>"` in their payload.

Seeded once on first start (deletable, off by default): `db-schema`, `system-design-c4`, `sequence-flow`, `infra-map`.

## Artifacts and pipelines

`GET /tasks/{id}/artifacts` → `{items: TaskArtifact[]}`: the latest version of each file the task's runs left in `.fleetpocket/out/` (listed in `manifest.json`, or any `.md .mmd .json .sql .dbml .d2 .puml .svg`; ≤ 50 files, ≤ 1 MiB each). Output `artifacts` tasks never push: they end `completed` with reason "n artifacts". The timeline gets an `artifacts_collected` event `{count, added, warnings}`.

Pipelines (SPEC-server "Pipelines"): `GET /pipeline-templates` → `{items, plan_schema}`; `POST /pipelines {template, project_id, repository_id?, title?, prompt, budget_usd?, max_parallel?, stages?: {<key>: {provider?, model?, node?}}}` → `201 Pipeline`; `GET /pipelines?project=` (without stages); `GET /pipelines/{id}` (with stages and `cost_usd`); `POST /pipelines/{id}/{action}` with `approve`, `plan {plan}` (edit while `awaiting_approval`; `400 invalid_plan` with `errors[]`), `redesign {note}`, `retry {stage_id, note?}`, `skip {stage_id}`, `cancel`, `budget {budget_usd}`; `409` when the pipeline or stage is not in a state that allows it. A stage's task has `pipeline_id`, `stage_id`, `base_ref`, `push_branch` and `model`; output `branch` (stage tasks only) pushes without a PR and ends `branch_ready`.

## WebSocket

`GET /api/v1/ws` (session cookie required; same origin). Without a cookie (Telegram Web), first `POST /api/v1/ws/ticket` with the bearer token → `{"ticket":"…","expires_in":30}`, then connect to `/api/v1/ws?ticket=…`. A ticket works once and only within `expires_in` seconds, so fetch a new one for every (re)connect. After connecting, send one or more subscriptions:

```json
{"op": "subscribe", "task_id": 123}
{"op": "subscribe", "scope": "tasks"}
{"op": "unsubscribe", "task_id": 123}
```

The server pushes:

```json
{"op": "event", "task_id": 123, "event": { /* timeline event as above */ }}
{"op": "task", "task": { /* task object */ }}
{"op": "node", "node": { /* node object */ }}
{"op": "error", "message": "…"}
```

- `task_id` subscriptions receive that task's `event` and `task` messages.
- `scope: "tasks"` receives `task` messages for every task (for the list view) and `node` messages (a node connected, disconnected or changed), no events.
- A `task` message is sent on every status change and also whenever `turns`, `last_activity`, `last_activity_at` or `queued_message` change (each `tool_use` and `usage` event, each queued message), so the list view stays live without event subscriptions.
- The server pings every 30 s. Messages to a slow client may be dropped. **On (re)connect, reload missed events with `GET /tasks/{id}/events?after_id=<last id>`**, then rely on pushes.

## Other

| | |
|---|---|
| `POST /webhooks/github` | GitHub only; authenticated by `X-Hub-Signature-256` |
| `GET /healthz` | `{"ok": true, "checks": {"database": "ok", "docker": "ok"}}`, `503` when a check fails |
| `GET /metrics` | Prometheus; only from the internal network (not through the reverse proxy) |
