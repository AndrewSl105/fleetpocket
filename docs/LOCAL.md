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

## Your repositories

Local mode works on folders on your disk; GitHub is optional.

1. **Pick a folder.** Run `jungle-monkey` inside a git repository and it is
   added and opened right away. Otherwise the web app suggests the
   repositories it finds in `~/code`, `~/Projects`, `~/projects`, `~/dev`,
   `~/src`, `~/repos`, `~/Documents/GitHub` and `~/work` (3 levels deep), has
   a folder browser limited to your home directory, and
   `jungle-monkey add [path]` adds one from the terminal. Each folder gets a
   project named after it.
2. **Describe a change.** The agent works in a copy of the repository
   (`git clone --local --no-hardlinks` into the data directory). The original
   folder is never mounted into the agent's container. "Start from" picks the
   last commit of the base branch (default) or your current commit plus your
   uncommitted changes and untracked files (committed in the copy as
   `WIP: working tree at start`).
3. **Get a branch back.** When the run ends with commits, they are fetched
   into your repository as `jm/<task id>-<slug>` (`-2`, `-3`… if the name is
   taken) and the task shows `⎇ BRANCH READY`. Your checked-out branch, index
   and working tree are never touched. Then:
   - **Merge into current branch**: a fast-forward when possible, else a merge
     commit. Refused while you have uncommitted changes to tracked files or a
     detached HEAD; a conflict is aborted and the files are listed.
   - **Check out the branch** (refused with uncommitted changes).
   - **Discard**: deletes the `jm/...` branch.
   - **Push & open PR**: when the repository has a GitHub `origin` and GitHub
     is connected. The task then follows the normal PR flow: CI status, CI
     auto-fix if enabled, Merge.
   A follow-up message continues the same branch and updates `jm/...` (or a
   new `-2` branch if you checked it out or committed on it meanwhile).
4. **Moved the folder?** The task says "repository folder not found";
   "Locate…" points it at the new place and a waiting delivery continues.

### GitHub (optional)

Connect GitHub to push and open PRs, from the onboarding or Settings → GitHub:

- **Sign in with GitHub** (OAuth device flow, scope `repo`): enter the code
  shown at github.com/login/device.
- **Use GitHub CLI login**: imports `gh auth token` (asked first).
- **Paste a token**: a fine-grained personal access token with Contents and
  Pull requests read/write (and Checks read for CI status).

The token is kept in the OS keychain (service `jungle-monkey`, a 0600 file
without one), used only by the jungle-monkey process, never given to the
agent's container. Without webhooks, open PRs are checked every minute
(conditional requests; backing off when GitHub fails).

## Commands

| Command | |
|---|---|
| `jungle-monkey` / `start` | start everything, print the URL, open the browser; inside a git repository, add it and open its project. `--no-open`, `--port N` (remembered), `--data-dir DIR`, `--verbose`. Ctrl-C stops cleanly. |
| `add [path]` | add a folder (default: the current one; any folder inside a git repository adds its top level) as a local repository of the running instance, in a new project named after it. Idempotent |
| `open` | open the web app with a fresh one-time login link (60 s, single use) |
| `status` | running?, URL, version, task counts, Docker and Postgres state (exit 3 when not running) |
| `stop` | stop a running instance (e.g. the background service) |
| `doctor` | ✓/✕ checks with a fix line: Docker, disk, agent image, Postgres, agent credentials, local repositories found, GitHub (connection and scopes; "not connected" is fine), keychain, port, clock |
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
- **Uninstall**: `jungle-monkey stop`; `jungle-monkey service uninstall`; delete the data directory;
  remove the keychain entries of service `jungle-monkey` (Keychain Access on
  macOS, Credential Manager on Windows, `secret-tool clear service jungle-monkey`
  on Linux); `docker image rm ghcr.io/andrewsl105/fleetpocket-agent:<version>`;
  `npm uninstall -g jungle-monkey` if you installed it globally (`npx` keeps
  only a cache). Your repositories keep the `jm/...` branches you did not
  merge or discard: `git branch --list 'jm/*'`.
- **Move to a server**: install the server (docs/DEPLOY.md). To keep history,
  dump the local database while jungle-monkey runs:
  `pg_dump -h 127.0.0.1 -p <port> -U fleetpocket fleetpocket` (the Postgres
  port changes per start; find it with `ps` on `postgres -p`, password is
  `db_password` in `config.json`), and restore the dump into the server's
  Postgres before its first start. Projects that used a local GitHub token
  need the server's GitHub App installed on the same repos.

## Owner checklist

1. **GitHub Pages**: Settings → Pages → Source: **GitHub Actions**
   (`.github/workflows/pages.yml` deploys `index.html` and `docs/brand/`).
2. **npm org**: create the npm organization `jungle-monkey` (scope
   `@jungle-monkey`, for the platform packages) and make sure the package
   name `jungle-monkey` is yours.
3. **Publishing**: either configure npm trusted publishing for the six
   packages (workflow `release.yml`, this repository) and set the repository
   variable `NPM_TRUSTED_PUBLISHING=true`, or add an `NPM_TOKEN` secret
   (automation token). Without either the release skips npm publish.
4. **GitHub OAuth App** "Jungle Monkey" with **Device Flow enabled**
   (Settings → Developer settings → OAuth Apps; the callback URL is unused by
   the device flow, no client secret is needed). Put its client id in
   `server/internal/config` `DefaultGitHubClientID`, or build with
   `-ldflags "-X github.com/andrewsl105/fleetpocket/server/internal/config.DefaultGitHubClientID=<id>"`;
   `JM_GITHUB_CLIENT_ID` overrides it at run time. Without it, "Sign in with
   GitHub" is hidden and the `gh` and token options remain.
5. **Release**: push a `vX.Y.Z` tag. `release.yml` builds the binaries for
   every target in `npm/platforms.json`, pushes
   `ghcr.io/andrewsl105/fleetpocket-agent:vX.Y.Z`, creates the GitHub Release
   with the binaries and `checksums.txt`, and publishes the platform packages,
   then `jungle-monkey`. The PR job `npm-smoke` (`npm/smoke.sh`) is the same
   flow as a dry run.
