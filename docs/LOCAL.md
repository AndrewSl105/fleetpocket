# Local mode: `npx jungle-monkey`

Jungle Monkey on your own computer: one binary with the server, an embedded
Postgres and the web app, running tasks in your Docker. No domain, VPS,
Postgres install, GitHub App or `.env`.

```sh
npx jungle-monkey
```

Requirements: Docker running (Docker Desktop, OrbStack, Colima or native
Linux Docker), Node ≥ 18 for `npx`, and one agent login or key (Claude
subscription token, Anthropic, OpenAI or Cursor key).

This is the first of three ways to install (README, "Three ways to install").
Local mode stops when the laptop sleeps. To run around the clock and reach it
from the phone, use the server installer or a self-hosted setup
([DEPLOY.md](DEPLOY.md)):

```sh
curl -fsSL https://raw.githubusercontent.com/AndrewSl105/jungle_monkey/main/deploy/install.sh | sh
```

## Status of this first version

Working: start/stop, embedded Postgres and web UI, one-time login link,
Docker discovery, runner image pull, agent keys from the keychain (including
the ones `fleetpocket-node` stored), `doctor`, `service`, `logs`.

Not yet (docs/DECISIONS.md, Server): local repositories (picking a folder on
disk and getting a `jm/...` branch back in it, the `branch_ready` status and
its Merge / Check out / Discard actions, `jungle-monkey add`), connecting
GitHub (device flow, `gh` token or PAT), PR/CI polling, the web onboarding
steps and Settings → Remote access. Until then, task commits stay in the local
workspace.

## Commands

| Command | |
|---|---|
| `jungle-monkey` / `start` | start everything, print the URL, open the browser. `--no-open`, `--port N` (remembered), `--data-dir DIR`, `--verbose`. Ctrl-C stops cleanly. |
| `open` | open the web app with a fresh one-time login link (60 s, single use) |
| `status` | running?, URL, version, task counts, Docker and Postgres state (exit 3 when not running) |
| `stop` | stop a running instance (e.g. the background service) |
| `doctor` | ✓/✕ checks with a fix line: Docker, disk, agent image, Postgres, agent credentials, GitHub, keychain, port, clock |
| `service install` / `uninstall` | start at login: launchd user agent (macOS), `systemd --user` (Linux), Task Scheduler (Windows) |
| `logs [-f]` | print (and follow) the log |
| `--version`, `help` | |

Exit codes: 0 ok, 1 error, 2 Docker is not running, 3 not running (`status`, `open`, `stop`).

On stop, running task containers are stopped too unless `keep_tasks_running`
is `true` in `config.json`.

## Data

| OS | Data directory |
|---|---|
| macOS | `~/Library/Application Support/JungleMonkey` |
| Linux | `$XDG_DATA_HOME/jungle-monkey` (default `~/.local/share/jungle-monkey`) |
| Windows | `%LOCALAPPDATA%\JungleMonkey` |

Override with `--data-dir` or `JM_DATA_DIR`. Inside:

- `config.json`: port, generated secrets (session, database password, CLI control token), defaults. Mode 0600.
- `db/`: Postgres 17 (`data/`, downloaded binaries in `cache/`).
- `workspaces/`, `attachments/`: task checkouts and attached images.
- `logs/jungle-monkey.log`: server and Postgres log.
- `secrets.json`: only when there is no OS keychain (headless Linux), mode 0600; `doctor` says so.
- `bin/<version>/`: the binary, when the npm launcher had to download it.

Secrets (GitHub token, agent keys, and `SECRETS_KEY`, generated on the first
start to encrypt MCP server credentials in the database) live in the OS keychain under the service
`jungle-monkey`; keys stored by `fleetpocket-node` (service `fleetpocket-node`)
are read too. Environment variables (`ANTHROPIC_API_KEY`, …) win over both.

## Security

- Listens on `127.0.0.1` only, on a remembered port (4815 when free). Runners reach the MCP gateway through Docker Desktop's `host.docker.internal`; on Linux, where containers cannot reach the host's loopback, MCP servers need `MCP_GATEWAY_URL` (docs/OPERATIONS.md).
- No password: the CLI opens a one-time link that sets a `SameSite=Strict`, HttpOnly session cookie.
- Requests whose `Host` is not `127.0.0.1:<port>` or `localhost:<port>` are rejected (DNS rebinding); the CSRF header check and "no CORS" stay.
- Agent containers keep every server-mode property: no GitHub token, no Docker socket, no host network. Only the jungle-monkey process (yours) talks to Docker.

## Remote access (phone)

Not built in. Put a tunnel in front of the local port, for example:

```sh
tailscale serve --bg 4815          # your tailnet only
tailscale funnel --bg 4815         # public
cloudflared tunnel --url http://127.0.0.1:4815
```

The Host check rejects the tunnel's hostname today, and a public URL needs a
password + TOTP first; the Settings → Remote access section that sets both is
not built yet (DECISIONS.md). Until then use the server install for phone access.

## Backup, uninstall, moving to a server

- **Backup**: `jungle-monkey stop`, then copy the data directory.
- **Uninstall**: `jungle-monkey service uninstall`; delete the data directory;
  remove the keychain entries of service `jungle-monkey` (Keychain Access on
  macOS, Credential Manager on Windows, `secret-tool clear service jungle-monkey`
  on Linux); `docker image rm ghcr.io/andrewsl105/fleetpocket-agent:<version>`.
- **Move to a server**: install the server (docs/DEPLOY.md). To keep history,
  dump the local database while jungle-monkey runs:
  `pg_dump -h 127.0.0.1 -p <port> -U fleetpocket fleetpocket` (the Postgres
  port changes per start; find it with `ps` on `postgres -p`, password is
  `db_password` in `config.json`), and restore the dump into the server's
  Postgres before its first start. Projects that used a local GitHub token
  need the server's GitHub App installed on the same repos.

## Owner checklist

0. Enable GitHub Pages for the site: Settings → Pages → Source: **GitHub
   Actions** (the Pages workflow is still to be added, see DECISIONS.md).

1. Create the GitHub OAuth App "Jungle Monkey" with **Device Flow enabled**
   (callback URL is unused by the device flow). Put its client id in
   `server/internal/config` `DefaultGitHubClientID`, or pass it at build time
   with `-ldflags "-X github.com/andrewsl105/fleetpocket/server/internal/config.DefaultGitHubClientID=<id>"`.
   No client secret is needed.
2. Create the npm org `jungle-monkey` (scope `@jungle-monkey`) and reserve the package `jungle-monkey`.
3. Configure npm trusted publishing for the five platform packages and the
   main package (or add an `NPM_TOKEN` secret).
4. Add the release workflow (not in this PR, see DECISIONS.md) on a `v*` tag:
   `make jungle-monkey-all JM_VERSION=$TAG` (web build + 5 `CGO_ENABLED=0`
   binaries with `embedweb`), build and push `fleetpocket-agent:$TAG`,
   `node npm/build.mjs $TAG bin`, attach `bin/jungle-monkey-*` and
   `bin/checksums.txt` to the Release, then `npm publish --provenance` each
   `npm/dist/<platform>` and `npm/dist/jungle-monkey` last. Add a PR job that
   runs `npm pack` on them and installs from the tarballs, and a smoke job on
   ubuntu and macOS: install → `jungle-monkey start --no-open` with
   `FLEETPOCKET_RUNNER_FAKE=true` → `/healthz` and `/` → `jungle-monkey stop`.
   On the macOS runner, check the npm-installed binary runs without a
   Gatekeeper prompt (npm does not set the quarantine attribute; the Go linker
   ad-hoc signs darwin/arm64).
5. Push a `vX.Y.Z` tag once the release workflow exists.
