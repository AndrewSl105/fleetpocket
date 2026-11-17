<p align="center"><img src="docs/brand/jungle-monkey-icon-192.png" alt="Jungle Monkey logo: a monkey's head in a square ink frame" width="96"></p>

<h1 align="center">Jungle Monkey</h1>

<p align="center"><strong>Send a coding agent (Claude Code, OpenAI Codex or Cursor) into your GitHub repo from your phone. Get a pull request back.</strong></p>

<p align="center"><a href="https://andrewsl105.github.io/FleetOps/">Website</a> · <a href="docs/DEPLOY.md">Set it up</a> · MIT license</p>

Describe a change from your phone. The agent you pick works in a sealed container on your own server, asks you when it needs a decision, and opens a pull request when it's done. Review it whenever you like.

## How it works

1. **Describe the change.** Type it and attach screenshots or files.
2. **The agent works on a new branch.** Pick Claude Code, Codex or Cursor per project or per task, and follow every step live.
3. **Answer its questions.** When Claude needs a decision, the task shows its question with answer buttons. Codex and Cursor make their own choices and list open questions in the summary.
4. **Get a pull request.** Read the summary, the PR and the CI result, and send a follow-up if something needs changing.
5. **Get it back green** (optional). Turn on CI auto-fix and a failing check starts a minimal fix from the trimmed CI logs, up to a few attempts. A fix that touches CI config or tests waits for your OK.

## Steer from anywhere

- **Phone**: Jungle Monkey is a progressive web app. Add it to your home screen and it opens full screen. The task list, live progress and answer buttons are laid out for one hand.
- **Desktop**: any browser, with a narrow rail, the task list and the open task side by side, plus a canvas of live terminals.
- **Telegram** (optional): the same app as a Telegram Mini App, with a bot that pings you when an agent asks something.
- **Your Mac** (optional): run tasks on your own computer as well as the server.

## Your keys stay with you

Jungle Monkey runs on your server, for one owner: you. The agent works in a sealed container with only the code it was asked to change. It gets no GitHub token, no Docker access and no host network. Your server keeps every key and does the pushing and the pull requests itself. You sign in with a password and a code from your authenticator app.

## Quick start: on your computer

```sh
npx jungle-monkey
```

You need Docker running, a key or subscription for one agent (Claude, Codex or Cursor) and a GitHub account. The server, its database and the web app start on your machine and the browser opens, logged in. No domain, server or `.env`. Commands, data locations and uninstall: [docs/LOCAL.md](docs/LOCAL.md).

Local or server? Local stops when your laptop sleeps. A server runs tasks around the clock and is reachable from your phone.

## Run it 24/7 on a server

You need a small Linux server (Ubuntu 24.04 with Docker), a domain, a key for at least one agent (a Claude subscription or Anthropic API key, an OpenAI API key for Codex, or a Cursor API key) and a GitHub account.

Run the installer on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/AndrewSl105/FleetOps/main/deploy/install.sh | sh
```

It installs Docker if needed, asks for your domain, which agents to use and their keys, writes `/srv/fleetpocket/.env`, starts everything and prints a setup link.

- **Claude**: run `claude setup-token` on your computer, or use an Anthropic API key. ([details](docs/DEPLOY.md#4-claude-auth))
- **Codex or Cursor** (optional): set `OPENAI_API_KEY` for Codex or `CURSOR_API_KEY` for Cursor, and `DEFAULT_AGENT` to choose the agent for projects that don't pick one.
- **GitHub**: create your own GitHub App and install it on the repositories Jungle Monkey may work on. ([details](docs/DEPLOY.md#5-github-app))

Telegram is optional: add the bot later ([details](docs/DEPLOY.md#6-telegram-bot-and-mini-app)). For unattended installs (cloud-init on Hetzner or DigitalOcean) use [`deploy/cloud-init.yaml`](deploy/cloud-init.yaml). To set everything up by hand, see [Manual setup](docs/DEPLOY.md#manual-setup).

**Optional: run tasks on your Mac too.** In Settings → Computers → Add computer, copy one command into the Mac's Terminal and click Connect. Then choose per project or per task whether a task runs on the server or on your Mac. ([details](docs/DEPLOY.md#11-run-tasks-on-your-own-computer-optional))

**Updating**: run the installer again. It keeps your `.env`, adds new settings, pulls the new images and restarts. Internal names (`fleetpocket-*` images, `/srv/fleetpocket`, `FLEETPOCKET_*` variables) are unchanged by the rename, so nothing needs to be moved by hand.

## Docs

- [Setup guide](docs/DEPLOY.md): server, GitHub App, updates and backups
- [Operations](docs/OPERATIONS.md): health checks, metrics, logs
- [Development](docs/DEVELOPMENT.md): running it locally and contributing

## License

[MIT](LICENSE)

## Screenshots

<table>
  <tr>
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/tasks-phone-dark.png"><img src="docs/screenshots/tasks-phone-light.png" alt="Task list with running, waiting and paused tasks" width="200"></picture></td>
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-question-phone-dark.png"><img src="docs/screenshots/task-question-phone-light.png" alt="A task waiting on a question, with answer buttons" width="200"></picture></td>
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-pr-phone-dark.png"><img src="docs/screenshots/task-pr-phone-light.png" alt="A finished task with its summary, pull request and CI result" width="200"></picture></td>
  </tr>
  <tr>
    <td colspan="3"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-running-desktop-dark.png"><img src="docs/screenshots/task-running-desktop-light.png" alt="Desktop: rail, task list and a running task with its live timeline" width="640"></picture></td>
  </tr>
</table>
