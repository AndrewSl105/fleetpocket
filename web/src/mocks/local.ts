import { http, HttpResponse, delay } from 'msw'
import type { ApiEvent, Task } from '@/api/types'
import { addEvent, db, taskView } from './db'
import { emitStatus } from './simulator'
import { isLocalMock } from './mode'
import { addLocalRepo, children, HOME, local, localRepos, repoOfTask, repoState } from './localdb'

// /local/* and /tasks/{id}/branch/* in mock mode (docs/API.md "Local repositories").
// In server mode (the default mock) every /local endpoint answers 400 local_mode_only.

type Guard = (request: Request) => Response | null
type Err = (status: number, code: string, message: string) => Response

const GITHUB_URL = /^(https?:\/\/(www\.)?github\.com\/|ssh:\/\/git@github\.com[:/]|git@github\.com:)/i

export function localHandlers(API: string, guard: Guard, err: Err, pushEvent: (taskId: number, e: ApiEvent) => void) {
  /** Auth, CSRF, then local mode only. */
  const gate = (request: Request) => guard(request) ?? (isLocalMock() ? null : err(400, 'local_mode_only', 'only in local mode (npx jungle-monkey)'))
  const errFiles = (files: string[]) =>
    HttpResponse.json({ error: { code: 'merge_conflict', message: 'the merge has conflicts; it was aborted', files } }, { status: 409 })
  const findTask = (id: unknown) => db.tasks.find((t) => t.id === Number(id))

  /** Common checks of the branch actions; the task's repository state on success. */
  function branchTarget(task: Task | undefined) {
    if (!task) return { error: err(404, 'not_found', 'No such task') }
    if (task.status !== 'branch_ready' || !task.local_branch) return { error: err(409, 'conflict', 'the task has no delivered branch') }
    const repo = repoOfTask(task)
    const git = repo && repoState(repo.path)
    if (!repo || !git) return { error: err(409, 'repo_missing', `repository folder not found: ${repo?.path ?? ''}`) }
    if (!git.branches.has(task.local_branch)) return { error: err(409, 'branch_gone', `branch ${task.local_branch} no longer exists`) }
    return { repo, git, branch: task.local_branch }
  }

  function action(task: Task, payload: Record<string, unknown>) {
    pushEvent(task.id, addEvent(task.id, 'branch_action', payload))
  }

  return [
    http.get(`${API}/local/repos/suggestions`, async ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      await delay(200)
      const added = new Set(localRepos().map((r) => r.path))
      const items = [...local.folders.values()]
        .filter((f) => f.git)
        .map((f) => ({ name: f.git!.name, path: f.path, branch: f.git!.detached ? '' : f.git!.branch, dirty: f.git!.dirty, last_commit: f.git!.lastCommit, added: added.has(f.path) }))
        .sort((a, b) => b.last_commit.localeCompare(a.last_commit))
      return HttpResponse.json({ items, start: local.start })
    }),
    http.get(`${API}/local/fs`, async ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      const path = (new URL(request.url).searchParams.get('path') || HOME).replace(/\/+$/, '') || '/'
      if (path !== HOME && !path.startsWith(`${HOME}/`)) return err(403, 'outside_home', 'only folders inside the home directory')
      const folder = local.folders.get(path)
      if (!folder) return err(404, 'not_found', 'no such folder')
      await delay(100)
      return HttpResponse.json({
        path,
        parent: path === HOME ? null : path.slice(0, path.lastIndexOf('/')),
        home: HOME,
        is_git: !!folder.git,
        entries: children(path).map((c) => ({ name: c.path.split('/').pop()!, path: c.path, is_git: !!c.git })),
      })
    }),
    http.post(`${API}/local/repos`, async ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      const body = (await request.json()) as { path: string; project_id?: string }
      await delay(250)
      const res = addLocalRepo(body.path, body.project_id)
      if (typeof res === 'string') return err(400, 'invalid', res)
      return HttpResponse.json(res, { status: res.created ? 201 : 200 })
    }),
    http.get(`${API}/local/repos/:repoId/status`, ({ request, params }) => {
      const denied = gate(request)
      if (denied) return denied
      const repo = localRepos().find((r) => r.id === Number(params.repoId))
      if (!repo) return err(404, 'not_found', 'No such repository')
      const git = repoState(repo.path)
      return HttpResponse.json({
        repository_id: repo.id,
        path: repo.path,
        missing: !git,
        branch: git && !git.detached ? git.branch : '',
        detached: !!git?.detached,
        dirty: !!git?.dirty,
      })
    }),
    http.post(`${API}/local/repos/:repoId/locate`, async ({ request, params }) => {
      const denied = gate(request)
      if (denied) return denied
      const repo = localRepos().find((r) => r.id === Number(params.repoId))
      if (!repo) return err(404, 'not_found', 'No such repository')
      const body = (await request.json()) as { path: string }
      const git = repoState(body.path)
      if (!git) return err(400, 'invalid', 'not a git work tree')
      if (localRepos().some((r) => r.id !== repo.id && r.path === body.path)) return err(409, 'conflict', 'another repository uses this folder')
      Object.assign(repo, { path: body.path, full_name: git.name, remote_url: git.remote })
      // Deliveries waiting for the folder resume on the simulator's next tick.
      return HttpResponse.json(repo)
    }),

    // ---- Branch actions ----
    http.post(`${API}/tasks/:id/branch/merge`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = findTask(params.id)
      const target = branchTarget(task)
      if ('error' in target) return target.error
      const { git, branch } = target
      await delay(300)
      if (git.detached) return err(409, 'detached', 'HEAD is detached')
      if (git.branch === branch) return err(409, 'current_branch', 'the task branch is checked out')
      if (git.dirty) return err(409, 'dirty', 'uncommitted changes to tracked files')
      // A deterministic conflict for demos and tests: a task titled "… conflict …".
      if (/conflict/i.test(task!.title)) return errFiles(['src/auth/login.ts', 'src/auth/limits.ts'])
      action(task!, { action: 'merged', branch, into: git.branch, fast_forward: true })
      emitStatus(task!, 'completed', `branch merged into ${git.branch}`)
      return HttpResponse.json(taskView(task!))
    }),
    http.post(`${API}/tasks/:id/branch/checkout`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = findTask(params.id)
      const target = branchTarget(task)
      if ('error' in target) return target.error
      await delay(200)
      if (target.git.dirty) return err(409, 'dirty', 'uncommitted changes to tracked files')
      Object.assign(target.git, { branch: target.branch, detached: false })
      action(task!, { action: 'checked_out', branch: target.branch })
      return HttpResponse.json(taskView(task!))
    }),
    http.post(`${API}/tasks/:id/branch/discard`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = findTask(params.id)
      if (task?.status === 'branch_ready' && task.local_branch) {
        const repo = repoOfTask(task)
        const git = repo && repoState(repo.path)
        if (!git) return err(409, 'repo_missing', `repository folder not found: ${repo?.path ?? ''}`)
        if (git.branch === task.local_branch) return err(409, 'current_branch', 'check out another branch first')
        await delay(200)
        git.branches.delete(task.local_branch)
        action(task, { action: 'discarded', branch: task.local_branch })
        task.local_branch = null
        emitStatus(task, 'completed', 'branch discarded')
        return HttpResponse.json(taskView(task))
      }
      return task ? err(409, 'conflict', 'the task has no delivered branch') : err(404, 'not_found', 'No such task')
    }),
    http.post(`${API}/tasks/:id/branch/push`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = findTask(params.id)
      const target = branchTarget(task)
      if ('error' in target) return target.error
      if (!GITHUB_URL.test(target.repo.remote_url)) return err(400, 'invalid', 'the repository has no GitHub origin remote')
      if (!local.github.connected) return err(503, 'github_disabled', 'GitHub is not connected')
      await delay(400)
      const n = 100 + task!.id
      const name = target.repo.remote_url.replace(GITHUB_URL, '').replace(/\.git$/, '')
      Object.assign(task!, { pr_number: n, pr_url: `https://github.com/${name}/pull/${n}`, ci_status: 'pending' })
      action(task!, { action: 'pushed', branch: task!.branch })
      pushEvent(task!.id, addEvent(task!.id, 'pr_opened', { number: n, url: task!.pr_url }))
      emitStatus(task!, 'pr_open')
      return HttpResponse.json(taskView(task!))
    }),

    // ---- GitHub (optional in local mode) ----
    http.get(`${API}/local/github`, ({ request }) => gate(request) ?? HttpResponse.json(local.github)),
    http.delete(`${API}/local/github`, ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      local.github = { ...local.github, connected: false, login: '', scopes: [], error: '' }
      return HttpResponse.json(local.github)
    }),
    http.post(`${API}/local/github/device`, async ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      if (!local.github.device_flow) return err(502, 'github', 'no OAuth client id configured')
      await delay(200)
      local.devicePolls = 0
      local.deviceStarted = true
      // GitHub's interval is 5 s; the mock polls every second.
      return HttpResponse.json({ user_code: 'WDJB-MJHT', verification_uri: 'https://github.com/login/device', expires_in: 900, interval: 1 })
    }),
    http.post(`${API}/local/github/device/poll`, ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      if (!local.deviceStarted) return HttpResponse.json({ status: 'none', github: local.github })
      local.devicePolls++
      if (local.devicePolls < 3) return HttpResponse.json({ status: 'pending', github: local.github })
      local.deviceStarted = false
      local.github = { ...local.github, connected: true, login: 'demo-dev', scopes: ['repo'], error: '' }
      return HttpResponse.json({ status: 'connected', github: local.github })
    }),
    http.post(`${API}/local/github/gh`, async ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      if (!local.github.gh_cli) return err(400, 'invalid', 'the GitHub CLI is not installed')
      await delay(200)
      local.github = { ...local.github, connected: true, login: 'demo-dev', scopes: ['gist', 'read:org', 'repo'], error: '' }
      return HttpResponse.json(local.github)
    }),
    http.post(`${API}/local/github/token`, async ({ request }) => {
      const denied = gate(request)
      if (denied) return denied
      const { token } = (await request.json()) as { token: string }
      await delay(200)
      if (!/^(github_pat_|ghp_)\w{10,}$/.test(token.trim())) return err(400, 'invalid', 'GitHub did not accept the token')
      local.github = { ...local.github, connected: true, login: 'demo-dev', scopes: token.startsWith('ghp_') ? ['repo'] : [], error: '' }
      return HttpResponse.json(local.github)
    }),
  ]
}
