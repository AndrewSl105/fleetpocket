import { isApiError } from '@/api/errors'
import type { GitHubConnStatus, Project, Repository, TaskView } from '@/api/types'
import type { TimelineItem } from '@/lib/timeline'
import { lc } from '@/i18n/local'

/** github.com remotes in the forms git prints: https, ssh:// and scp-like git@github.com:owner/name. */
export function isGitHubRemote(url: string | null | undefined): boolean {
  if (!url) return false
  return /^(https?:\/\/(www\.)?github\.com\/|ssh:\/\/git@github\.com[:/]|git@github\.com:)[^/\s]+\/[^/\s]+$/i.test(url.trim())
}

export function isLocalRepo(repo: Pick<Repository, 'source'> | null | undefined): boolean {
  return repo?.source === 'local'
}

/** "Push & open PR": a GitHub origin remote and a connected GitHub. */
export function canPushPr(repo: Pick<Repository, 'source' | 'remote_url'> | null | undefined, gh: Pick<GitHubConnStatus, 'connected'> | undefined): boolean {
  return isLocalRepo(repo) && isGitHubRemote(repo?.remote_url) && !!gh?.connected
}

/** Local mode shows onboarding until some project has a local repository. */
export function hasLocalRepo(projects: Project[] | undefined): boolean {
  return !!projects?.some((p) => !p.archived && p.repositories.some((r) => r.source === 'local'))
}

const MISSING_PREFIX = 'repository folder not found'

/** A delivery waiting for a moved folder: `pushing` with "repository folder not found: …". */
export function waitsForFolder(task: Pick<TaskView, 'status' | 'status_reason'>): boolean {
  return task.status === 'pushing' && !!task.status_reason?.toLowerCase().startsWith(MISSING_PREFIX)
}

/** The branch the owner sees: the delivered `local_branch` for local repositories, else the agent branch. */
export function shownBranch(task: Pick<TaskView, 'local_branch' | 'branch'>): string | null {
  return task.local_branch ?? task.branch ?? null
}

export type BranchError = { message: string; files: string[] }

/** A branch action's error as a message to show inline (409 codes from docs/API.md). */
export function branchError(e: unknown, action?: 'merge' | 'checkout' | 'discard' | 'push'): BranchError {
  const errs = lc.branch.errors
  if (isApiError(e)) {
    if (e.code in errs) return { message: errs[e.code as keyof typeof errs], files: e.files ?? [] }
    if (action === 'push' && e.status === 400) return { message: errs.no_remote, files: [] }
    return { message: e.message, files: [] }
  }
  return { message: e instanceof Error ? e.message : String(e), files: [] }
}

/** The breadcrumb of a folder below the home directory: [{name, path}], home first. */
export function breadcrumb(path: string, home: string): Array<{ name: string; path: string }> {
  const out = [{ name: '~', path: home }]
  if (!path.startsWith(home) || path === home) return out
  const rest = path.slice(home.length).split('/').filter(Boolean)
  let cur = home.replace(/\/$/, '')
  for (const part of rest) {
    cur = `${cur}/${part}`
    out.push({ name: part, path: cur })
  }
  return out
}

/** "/Users/me/code/app" → "~/code/app". */
export function tildePath(path: string, home: string | undefined): string {
  if (!home || !path.startsWith(home)) return path
  return `~${path.slice(home.replace(/\/$/, '').length)}`
}

/** Timeline text of a `branch_ready` event. */
export function branchReadyText(item: Extract<TimelineItem, { kind: 'branch_ready' }>): string {
  return item.updated ? lc.timeline.updated(item.branch, item.commits) : lc.timeline.ready(item.branch, item.commits, item.diffstat)
}

/** Timeline text of a `branch_action` event. */
export function branchActionText(item: Extract<TimelineItem, { kind: 'branch_action' }>): string {
  switch (item.action) {
    case 'merged':
      return lc.timeline.merged(item.branch, item.into ?? '', !!item.fastForward)
    case 'checked_out':
      return lc.timeline.checked_out(item.branch)
    case 'discarded':
      return lc.timeline.discarded(item.branch)
    case 'pushed':
      return lc.timeline.pushed(item.branch)
  }
}
