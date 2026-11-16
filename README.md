# Fleetpocket

**Run Claude Code on your GitHub repositories from your phone, and get a pull request back.**

[Website](https://andrewsl105.github.io/fleetpocket/) · [Set it up](docs/DEPLOY.md) · MIT license

<img src="docs/screenshots/demo.gif" alt="Fleetpocket demo: a task is created, Claude works on it, asks a question and opens a pull request" width="320">

Describe a change in Telegram or in the web app. Claude Code does the work in an isolated container on your own server, asks you when it needs a decision, and opens a pull request when it's done. You review it whenever you like.

## How it works

1. **Describe the change.** Type it, dictate a voice message, or attach a screenshot.
2. **Claude works on a new branch.** You can follow every step live.
3. **Answer its questions.** When Claude needs a decision, you get a message with answer buttons.
4. **Get a pull request.** You see the summary, the PR and the CI result, and can send a follow-up if something needs changing.

## Steer from wherever you are

- **Telegram bot**: start tasks and answer Claude from the chat you already have open. Give each project its own topic in a group.
- **Telegram Mini App**: the full task board inside Telegram, with projects, statuses and live progress.
- **Web app**: the same app in any browser, with a sidebar layout on desktop.

## Your keys stay with you

Fleetpocket runs on your server, for one owner: you. Claude works in a sealed container with only the code it was asked to change. It gets no GitHub token, no Docker access and no host network. Your server keeps every key and does the pushing and the pull requests itself. You sign in with a password and a code from your authenticator app.

## Screenshots

<table>
  <tr>
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/tasks-phone-dark.png"><img src="docs/screenshots/tasks-phone-light.png" alt="Task list with running, waiting and paused tasks" width="200"></picture></td>
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-question-phone-dark.png"><img src="docs/screenshots/task-question-phone-light.png" alt="Task waiting on a question, with answer buttons" width="200"></picture></td>
    <td><picture><source media="(prefers-color-scheme: dark)" srcset="docs/screenshots/task-pr-phone-dark.png"><img src="docs/screenshots/task-pr-phone-light.png" alt="Finished task with its summary, pull request and CI result" width="200"></picture></td>
  </tr>
</table>

## Set it up

You need a small Linux server (Ubuntu or Debian), a Claude subscription or an Anthropic, OpenAI or Cursor API key, and a GitHub account. A domain pointing at the server is best; without one the installer offers a free `<ip>.sslip.io` name. Open ports 80 and 443.

**1. Run the installer** on the server:

```sh
curl -fsSL https://raw.githubusercontent.com/AndrewSl105/fleetpocket/main/deploy/install.sh | sh
```

It installs Docker if needed, asks for your domain, which agents to use and their keys, writes `/srv/fleetpocket/.env`, starts everything and prints a setup link.

**2. Open the link** (`https://<domain>/setup?code=…`) and follow the wizard: choose a password, scan the QR code with an authenticator app, click **Create GitHub App** and install it on your repositories, then add your first repository and start a task.

Telegram is optional: add the bot later ([details](docs/DEPLOY.md#6-telegram-bot-and-mini-app)). For unattended installs (cloud-init on Hetzner or DigitalOcean) use [`deploy/cloud-init.yaml`](deploy/cloud-init.yaml). To set everything up by hand instead, see [Manual setup](docs/DEPLOY.md#manual-setup).

**Optional: run tasks on your Mac too.** In Settings → Computers → Add computer, copy one command into the Mac's Terminal and click Connect; then choose per project or per task whether it runs on the server or on your Mac. One login and one task list either way. ([details](docs/DEPLOY.md#11-run-tasks-on-your-own-computer-optional))

To update later, run the installer again: it keeps your `.env`, adds new settings, pulls the new images and restarts. The full [setup guide](docs/DEPLOY.md) also covers backups and automatic deploys with GitHub Actions.

## Docs

- [Setup guide](docs/DEPLOY.md): server, GitHub App, Telegram bot, updates and backups
- [Operations](docs/OPERATIONS.md): health checks, metrics, logs
- [Development](docs/DEVELOPMENT.md): running it locally and contributing

## License

[MIT](LICENSE)
