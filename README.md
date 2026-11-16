<p align="center"><img src="docs/brand/fleetpocket-icon.png" alt="Fleetpocket logo" width="96"></p>

<h1 align="center">Fleetpocket</h1>

<p align="center"><strong>Run coding agents (Claude Code, OpenAI Codex or Cursor) on your GitHub repositories from your phone, and get a pull request back.</strong></p>

<p align="center"><a href="https://andrewsl105.github.io/fleetpocket/">Website</a> · <a href="docs/DEPLOY.md">Set it up</a> · MIT license</p>

Describe a change from your phone. The agent you pick does the work in an isolated container on your own server, asks you when it needs a decision, and opens a pull request when it's done. You review it whenever you like.

## How it works

1. **Describe the change.** Type it and attach screenshots or files.
2. **The agent works on a new branch.** Pick Claude Code, Codex or Cursor per project or per task, and follow every step live.
3. **Answer its questions.** When Claude needs a decision, the task shows its question with answer buttons. Codex and Cursor make their own choices and list open questions in the summary.
4. **Get a pull request.** You see the summary, the PR and the CI result, and can send a follow-up if something needs changing.

## Steer from wherever you are

- **Install it on your phone**: Fleetpocket is a progressive web app. Add it to your home screen and it opens full screen like a native app.
- **Built for mobile first**: the task board, projects, live progress and answer buttons are laid out for a phone, with a taskbar at the bottom for one-handed use.
- **The same app on desktop**: in any browser, with a Start menu sidebar and the task next to the list, all in a Windows XP look.

## Your keys stay with you

Fleetpocket runs on your server, for one owner: you. The agent works in a sealed container with only the code it was asked to change. It gets no GitHub token, no Docker access and no host network. Your server keeps every key and does the pushing and the pull requests itself. You sign in with a password and a code from your authenticator app.

## Set it up

You need a small Linux server (Ubuntu 24.04 with Docker), a domain, a key for at least one agent (a Claude subscription or Anthropic API key, an OpenAI API key for Codex, or a Cursor API key) and a GitHub account. Below, `fleetpocket.example.com` stands for your domain.

**1. Run the installer** on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/AndrewSl105/fleetpocket/main/deploy/install.sh | sh
```

It installs Docker if needed, asks for your domain, which agents to use and their keys, writes `/srv/fleetpocket/.env`, starts everything and prints a setup link.

- **Claude**: run `claude setup-token` on your computer, or use an Anthropic API key. ([details](docs/DEPLOY.md#4-claude-auth))
- **Codex or Cursor** (optional): set `OPENAI_API_KEY` for Codex or `CURSOR_API_KEY` for Cursor, and `DEFAULT_AGENT` to choose the agent for projects that don't pick one.
- **GitHub**: create your own GitHub App and install it on the repositories Fleetpocket may work on. ([details](docs/DEPLOY.md#5-github-app))

**5. Fill in `.env`** with your domain, the values from steps 3 and 4, and two random secrets. Every variable is described in the file. ([details](docs/DEPLOY.md#2-env))

**6. Start it:**

```sh
docker pull ghcr.io/andrewsl105/fleetpocket-agent:latest
docker compose -f docker-compose.deploy.yml up -d
```

Telegram is optional: add the bot later ([details](docs/DEPLOY.md#6-telegram-bot-and-mini-app)). For unattended installs (cloud-init on Hetzner or DigitalOcean) use [`deploy/cloud-init.yaml`](deploy/cloud-init.yaml). To set everything up by hand instead, see [Manual setup](docs/DEPLOY.md#manual-setup).

**Optional: run tasks on your Mac too.** In Settings → Computers → Add computer, copy one command into the Mac's Terminal and click Connect; then choose per project or per task whether it runs on the server or on your Mac. One login and one task list either way. ([details](docs/DEPLOY.md#11-run-tasks-on-your-own-computer-optional))

To update later, run the installer again: it keeps your `.env`, adds new settings, pulls the new images and restarts. The full [setup guide](docs/DEPLOY.md) also covers backups and automatic deploys with GitHub Actions.

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
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-question-phone-dark.png"><img src="docs/screenshots/task-question-phone-light.png" alt="Task waiting on a question, with answer buttons" width="200"></picture></td>
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-pr-phone-dark.png"><img src="docs/screenshots/task-pr-phone-light.png" alt="Finished task with its summary, pull request and CI result" width="200"></picture></td>
  </tr>
  <tr>
    <td colspan="3"><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-running-desktop-dark.png"><img src="docs/screenshots/task-running-desktop-light.png" alt="Desktop layout: Start menu sidebar, task list and a running task with its live timeline" width="640"></picture></td>
  </tr>
</table>
