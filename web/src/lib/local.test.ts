import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/errors'
import type { ApiEvent, Project } from '@/api/types'
import { lc } from '@/i18n/local'
import { buildTimeline } from './timeline'
import { currentActivity, classify } from './activity'
import { STATUS, GROUPS, composerMode } from './status'
import {
  branchActionText,
  branchError,
  branchReadyText,
  breadcrumb,
  canPushPr,
  hasLocalRepo,
  isGitHubRemote,
  shownBranch,
  tildePath,
  waitsForFolder,
} from './local'

let id = 0
function ev(type: string, payload: Record<string, unknown>): ApiEvent {
  id++
  return { id, task_id: 1, run_id: null, seq: null, type, payload, created_at: '2026-11-17T10:00:00Z' } as unknown as ApiEvent
}

describe('branch_ready status', () => {
  it('has its own olive-outline stamp with the ⎇ glyph and sits with the PRs', () => {
    expect(STATUS.branch_ready).toMatchObject({ label: 'Branch ready', tone: 'branch', glyph: '⎇' })
    expect(GROUPS.pr_open).toContain('branch_ready')
    expect(composerMode('branch_ready')).toBe('followup')
  })

  it('maps to a finished activity naming the branch', () => {
    const a = currentActivity([], { status: 'branch_ready', local_branch: 'jm/4-fix-login', title: 'Fix login' })
    expect(a).toMatchObject({ kind: 'done', verb: 'BRANCH READY', target: 'jm/4-fix-login' })
    expect(currentActivity([], { status: 'branch_ready', local_branch: null, title: 'Fix login' }).target).toBe('Fix login')
  })
})

describe('branch timeline rows', () => {
  it('maps branch_ready and branch_action events', () => {
    const items = buildTimeline([
      ev('branch_ready', { branch: 'jm/4-fix', commits: 2, diffstat: '3 files changed', updated: false }),
      ev('branch_ready', { branch: 'jm/4-fix-2', commits: 3, diffstat: '', updated: false, previous: 'jm/4-fix' }),
      ev('branch_action', { action: 'merged', branch: 'jm/4-fix-2', into: 'main', fast_forward: true }),
      ev('branch_action', { action: 'discarded', branch: 'jm/4-fix' }),
    ])
    expect(items.map((i) => i.kind)).toEqual(['branch_ready', 'branch_ready', 'branch_action', 'branch_action'])
    const [ready, moved, merged, discarded] = items
    if (ready.kind !== 'branch_ready' || moved.kind !== 'branch_ready') throw new Error('kind')
    if (merged.kind !== 'branch_action' || discarded.kind !== 'branch_action') throw new Error('kind')
    expect(branchReadyText(ready)).toBe('Branch jm/4-fix ready · 2 commits · 3 files changed')
    expect(moved.previous).toBe('jm/4-fix')
    expect(branchActionText(merged)).toBe('Merged jm/4-fix-2 into main (fast-forward)')
    expect(branchActionText(discarded)).toBe('Discarded jm/4-fix')
    expect(classify(ready)).toMatchObject({ kind: 'git', target: 'jm/4-fix' })
  })

  it('says when a follow-up updated the branch', () => {
    const [item] = buildTimeline([ev('branch_ready', { branch: 'jm/4-fix', commits: 1, diffstat: '', updated: true })])
    if (item.kind !== 'branch_ready') throw new Error('kind')
    expect(branchReadyText(item)).toBe('Branch jm/4-fix updated · 1 commit')
  })
})

describe('local helpers', () => {
  it('recognises GitHub remotes', () => {
    expect(isGitHubRemote('git@github.com:me/app.git')).toBe(true)
    expect(isGitHubRemote('https://github.com/me/app')).toBe(true)
    expect(isGitHubRemote('ssh://git@github.com/me/app.git')).toBe(true)
    expect(isGitHubRemote('https://gitlab.com/me/app')).toBe(false)
    expect(isGitHubRemote('')).toBe(false)
  })

  it('offers Push & open PR only for a GitHub remote with GitHub connected', () => {
    const repo = { source: 'local' as const, remote_url: 'git@github.com:me/app.git' }
    expect(canPushPr(repo, { connected: true })).toBe(true)
    expect(canPushPr(repo, { connected: false })).toBe(false)
    expect(canPushPr(repo, undefined)).toBe(false)
    expect(canPushPr({ ...repo, remote_url: '' }, { connected: true })).toBe(false)
    expect(canPushPr({ ...repo, source: 'github' }, { connected: true })).toBe(false)
  })

  it('needs onboarding until a project has a local repository', () => {
    const p = (source: 'github' | 'local', archived = false) => ({ archived, repositories: [{ source }] }) as unknown as Project
    expect(hasLocalRepo(undefined)).toBe(false)
    expect(hasLocalRepo([p('github')])).toBe(false)
    expect(hasLocalRepo([p('local', true)])).toBe(false)
    expect(hasLocalRepo([p('github'), p('local')])).toBe(true)
  })

  it('spots a delivery waiting for a moved folder', () => {
    expect(waitsForFolder({ status: 'pushing', status_reason: 'repository folder not found: /x' })).toBe(true)
    expect(waitsForFolder({ status: 'pushing', status_reason: null })).toBe(false)
    expect(waitsForFolder({ status: 'failed', status_reason: 'repository folder not found: /x' })).toBe(false)
  })

  it('shows the delivered branch when there is one', () => {
    expect(shownBranch({ local_branch: 'jm/1-a', branch: 'agent/1-a' })).toBe('jm/1-a')
    expect(shownBranch({ local_branch: null, branch: 'agent/1-a' })).toBe('agent/1-a')
  })

  it('explains branch action errors, with the conflict files', () => {
    const conflict = branchError(new ApiError(409, 'merge_conflict', 'x', undefined, ['a.ts', 'b.ts']), 'merge')
    expect(conflict).toEqual({ message: lc.branch.errors.merge_conflict, files: ['a.ts', 'b.ts'] })
    expect(branchError(new ApiError(409, 'dirty', 'x')).message).toBe('Your working tree has uncommitted changes. Commit or stash them, then try again.')
    expect(branchError(new ApiError(400, 'invalid', 'no remote'), 'push').message).toBe(lc.branch.errors.no_remote)
    expect(branchError(new ApiError(503, 'github_disabled', 'x'), 'push').message).toBe(lc.branch.errors.github_disabled)
    expect(branchError(new ApiError(409, 'conflict', 'not branch_ready')).message).toBe('not branch_ready')
  })

  it('builds the folder breadcrumb below home', () => {
    expect(breadcrumb('/Users/me', '/Users/me')).toEqual([{ name: '~', path: '/Users/me' }])
    expect(breadcrumb('/Users/me/code/app', '/Users/me')).toEqual([
      { name: '~', path: '/Users/me' },
      { name: 'code', path: '/Users/me/code' },
      { name: 'app', path: '/Users/me/code/app' },
    ])
    expect(tildePath('/Users/me/code/app', '/Users/me')).toBe('~/code/app')
    expect(tildePath('/opt/x', '/Users/me')).toBe('/opt/x')
  })
})
