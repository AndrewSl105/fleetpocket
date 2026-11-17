import type { GitHubConnStatus, Project, Repository, Task } from '@/api/types'
import { addEvent, db, makeTask, setStatus, startRun } from './db'

// Local mode state for the mock (npx jungle-monkey): a fake home directory with
// git work trees, the repositories' branch state and the GitHub connection.

export const HOME = '/Users/demo'

export type MockRepoState = {
  path: string
  name: string
  branch: string
  detached: boolean
  dirty: boolean
  remote: string
  lastCommit: string
  /** Branches in the repository (delivered task branches among them). */
  branches: Set<string>
}

type Folder = { path: string; git?: MockRepoState }

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString()

export const local = {
  folders: new Map<string, Folder>(),
  /** The folder the CLI was started in, when it is a repository. */
  start: null as string | null,
  github: { connected: false, login: '', scopes: [], error: '', device_flow: true, gh_cli: true } as GitHubConnStatus,
  devicePolls: 0,
  deviceStarted: false,
}

function addFolder(path: string, git?: Omit<MockRepoState, 'path' | 'name' | 'branches'>) {
  const name = path.split('/').pop()!
  local.folders.set(path, { path, git: git && { ...git, path, name, branches: new Set([git.branch]) } })
}

export function seedLocalFs() {
  local.folders = new Map()
  local.github = { connected: false, login: '', scopes: [], error: '', device_flow: true, gh_cli: true }
  local.devicePolls = 0
  local.deviceStarted = false
  addFolder(HOME)
  addFolder(`${HOME}/code`)
  addFolder(`${HOME}/code/shop-api`, { branch: 'main', detached: false, dirty: false, remote: 'git@github.com:demo-dev/shop-api.git', lastCommit: ago(35) })
  addFolder(`${HOME}/code/landing`, { branch: 'dev', detached: false, dirty: true, remote: '', lastCommit: ago(60 * 5) })
  addFolder(`${HOME}/code/notes`)
  addFolder(`${HOME}/code/notes/2026`)
  addFolder(`${HOME}/Documents`)
  addFolder(`${HOME}/Documents/GitHub`)
  addFolder(`${HOME}/Documents/GitHub/dotfiles`, { branch: 'master', detached: false, dirty: false, remote: 'https://github.com/demo-dev/dotfiles', lastCommit: ago(60 * 24 * 9) })
  addFolder(`${HOME}/Downloads`)
  local.start = `${HOME}/code/shop-api`
}

export function repoState(path: string): MockRepoState | undefined {
  return local.folders.get(path)?.git
}

export function children(path: string): Folder[] {
  const prefix = path.endsWith('/') ? path : `${path}/`
  return [...local.folders.values()]
    .filter((f) => f.path.startsWith(prefix) && !f.path.slice(prefix.length).includes('/'))
    .sort((a, b) => a.path.localeCompare(b.path))
}

export function localRepos(): Repository[] {
  return db.projects.flatMap((p) => p.repositories).filter((r) => r.source === 'local')
}

export function repoOfTask(task: Task): Repository | undefined {
  return db.projects.flatMap((p) => p.repositories).find((r) => r.id === task.repository_id)
}

export function isLocalTask(task: Task): boolean {
  return repoOfTask(task)?.source === 'local'
}

function slug(s: string) {
  return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '').slice(0, 30).replace(/-$/, '') || 'folder'
}

/** POST /local/repos: a new project named after the folder, or the given one. */
export function addLocalRepo(path: string, projectId?: string): { project: Project; repository: Repository; created: boolean } | string {
  const git = repoState(path)
  if (!git) return 'not a git work tree'
  const existing = localRepos().find((r) => r.path === path)
  if (existing) return { project: db.projects.find((p) => p.id === existing.project_id)!, repository: existing, created: false }
  let project = projectId ? db.projects.find((p) => p.id === projectId) : undefined
  if (projectId && !project) return 'no such project'
  if (!project) {
    let id = slug(git.name)
    for (let n = 2; db.projects.some((p) => p.id === id); n++) id = `${slug(git.name)}-${n}`
    project = newProject(id, git.name)
    db.projects.push(project)
  }
  const isDefault = project.repositories.length === 0
  const repository: Repository = {
    id: db.nextRepoId++,
    project_id: project.id,
    full_name: git.name,
    base_branch: git.branch || 'main',
    installation_id: 0,
    is_default: isDefault,
    source: 'local',
    path,
    remote_url: git.remote,
  }
  project.repositories.push(repository)
  project.updated_at = new Date().toISOString()
  return { project, repository, created: true }
}

function newProject(id: string, name: string): Project {
  const at = new Date().toISOString()
  return {
    id,
    name,
    instructions: '',
    services: [],
    env: {},
    runner_image: null,
    agent: null,
    node: null,
    model: null,
    max_turns: null,
    max_budget_usd: null,
    max_duration_sec: null,
    runner_memory: null,
    runner_cpus: null,
    telegram_thread_id: null,
    archived: false,
    ci_autofix: { enabled: null, max_attempts: null, checks: null, budget_usd: null, allow_ci_changes: null },
    budget: null,
    month_cost_usd: 0,
    month_cost_unknown: false,
    created_at: at,
    updated_at: at,
    repositories: [],
  }
}

/** `jm/<id>-<slug>`, `-2`… when the name is taken. */
export function deliveryBranch(task: Task, git: MockRepoState): string {
  const base = `jm/${task.id}-${slug(task.title)}`
  let name = base
  for (let n = 2; git.branches.has(name); n++) name = `${base}-${n}`
  return name
}

export const DIFFSTAT = '3 files changed, 74 insertions(+), 19 deletions(-)'

/**
 * Local mode in the mock: no server-mode projects or tasks. `demo` adds a
 * project with a delivered branch, a repository whose folder moved and a
 * delivery waiting for it (screenshots, manual checks).
 */
export function seedLocal(demo: boolean) {
  seedLocalFs()
  db.projects = []
  db.tasks = []
  db.events.clear()
  db.runs.clear()
  db.questions.clear()
  db.nodes = []
  db.pairings = new Map()
  db.nextTaskId = 1
  if (!demo) return
  local.github = { ...local.github, connected: true, login: 'demo-dev', scopes: ['repo'] }
  const added = addLocalRepo(`${HOME}/code/shop-api`)
  if (typeof added === 'string') return
  const p = added.project
  const git = repoState(added.repository.path)!

  const t = makeTask({ project_id: p.id, title: 'Add rate limiting to the login endpoint', status: 'queued', created_at: ago(40), repository_id: added.repository.id, cost_usd_total: 0.64 })
  startRun(t.id, 'initial', t.prompt, ago(40))
  addEvent(t.id, 'started', { session_id: 's-l1', model: 'claude-opus-5-5' }, true, ago(40))
  addEvent(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit src/auth/login.ts' }, true, ago(38))
  addEvent(t.id, 'tool_result', { tool: 'Edit', ok: true }, true, ago(38))
  addEvent(t.id, 'committed', { sha: '5be1f0c2d9', files_changed: 3, insertions: 74, deletions: 19 }, true, ago(36))
  addEvent(t.id, 'done', { session_id: 's-l1', summary: 'Added a token bucket per IP to `POST /login`.', no_changes: false, cost_usd_total: 0.64 }, true, ago(36))
  addEvent(t.id, 'run_finished', { run_id: db.runs.get(t.id)![0].id, exit_code: 0 }, false, ago(36))
  const branch = deliveryBranch(t, git)
  git.branches.add(branch)
  t.local_branch = branch
  addEvent(t.id, 'branch_ready', { branch, commits: 1, diffstat: DIFFSTAT, updated: false }, false, ago(35))
  setStatus(t, 'branch_ready')
  Object.assign(t, { updated_at: ago(35), last_activity_at: ago(35) })

  // A second repository whose folder was moved, with a delivery waiting for it.
  const moved = addLocalRepo(`${HOME}/code/landing`, p.id)
  if (typeof moved === 'string') return
  moved.repository.path = `${HOME}/code/landing-old`
  const w = makeTask({ project_id: p.id, title: 'Fix the hero image on mobile', status: 'queued', created_at: ago(20), repository_id: moved.repository.id, cost_usd_total: 0.31 })
  startRun(w.id, 'initial', w.prompt, ago(20))
  addEvent(w.id, 'committed', { sha: '81aa03f7c4', files_changed: 1, insertions: 6, deletions: 2 }, true, ago(15))
  addEvent(w.id, 'done', { session_id: 's-l2', summary: 'Scaled the hero image with `object-fit`.', no_changes: false, cost_usd_total: 0.31 }, true, ago(15))
  setStatus(w, 'pushing', `repository folder not found: ${moved.repository.path}`)
}
