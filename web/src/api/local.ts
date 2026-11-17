// Local mode hooks (docs/API.md "Local repositories"): folders on the owner's
// disk, branch delivery actions and the optional GitHub connection. A module of
// their own, like checkpoints.ts and costs.ts.
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, CSRF } from './client'
import { keys } from './keys'
import { upsertTask } from './cache'
import type { GitHubConnStatus, TaskView } from './types'

export const localKeys = {
  suggestions: ['local', 'suggestions'] as const,
  folder: (path: string) => ['local', 'fs', path] as const,
  repoStatus: (repoId: number) => ['local', 'repo-status', repoId] as const,
  repoStatusAll: ['local', 'repo-status'] as const,
  github: ['local', 'github'] as const,
}

/** Git work trees found on this computer, plus the folder the CLI started in. */
export function useLocalSuggestions(enabled = true) {
  return useQuery({
    queryKey: localKeys.suggestions,
    queryFn: ({ signal }) => call(api.GET('/api/v1/local/repos/suggestions', { signal })),
    enabled,
    staleTime: 60_000,
  })
}

/** One folder of the browser; '' is the home directory. */
export function useLocalFolder(path: string, enabled = true) {
  return useQuery({
    queryKey: localKeys.folder(path),
    queryFn: ({ signal }) => call(api.GET('/api/v1/local/fs', { params: { query: path ? { path } : {} }, signal })),
    enabled,
    staleTime: 30_000,
    placeholderData: keepPreviousData,
  })
}

export function useAddLocalRepo() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { path: string; project_id?: string }) =>
      call(api.POST('/api/v1/local/repos', { params: { header: CSRF }, body })),
    onSuccess: (res) => {
      qc.setQueryData(keys.projects.detail(res.project.id), res.project)
      void qc.invalidateQueries({ queryKey: keys.projects.all })
      void qc.invalidateQueries({ queryKey: localKeys.suggestions })
    },
  })
}

/** Branch and dirty state of a local repository's folder (`missing` when it moved). */
export function useLocalRepoStatus(repoId: number | undefined, enabled = true) {
  return useQuery({
    queryKey: localKeys.repoStatus(repoId ?? 0),
    queryFn: ({ signal }) => call(api.GET('/api/v1/local/repos/{repoId}/status', { params: { path: { repoId: repoId! } }, signal })),
    enabled: enabled && !!repoId,
    staleTime: 10_000,
  })
}

/** "Locate…": points a moved repository at its folder; waiting deliveries resume. */
export function useLocateRepo() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ repoId, path }: { repoId: number; path: string }) =>
      call(api.POST('/api/v1/local/repos/{repoId}/locate', { params: { path: { repoId }, header: CSRF }, body: { path } })),
    onSuccess: (repo) => {
      void qc.invalidateQueries({ queryKey: keys.projects.all })
      void qc.invalidateQueries({ queryKey: localKeys.repoStatus(repo.id) })
      void qc.invalidateQueries({ queryKey: keys.tasks.all })
    },
  })
}

export type BranchActionKind = 'merge' | 'checkout' | 'discard' | 'push'

const BRANCH_PATH = {
  merge: '/api/v1/tasks/{id}/branch/merge',
  checkout: '/api/v1/tasks/{id}/branch/checkout',
  discard: '/api/v1/tasks/{id}/branch/discard',
  push: '/api/v1/tasks/{id}/branch/push',
} as const

/** Merge / Check out / Discard / Push & open PR on a `branch_ready` task. Errors are the caller's to show (409s inline). */
export function useBranchAction(taskId: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (kind: BranchActionKind) => call(api.POST(BRANCH_PATH[kind], { params: { path: { id: taskId }, header: CSRF } })),
    onSuccess: (task: TaskView) => {
      qc.setQueryData(keys.tasks.detail(task.id), task)
      upsertTask(qc, task)
      void qc.invalidateQueries({ queryKey: localKeys.repoStatusAll })
    },
  })
}

// ---- GitHub (optional in local mode) ----

export function useLocalGitHub(enabled = true) {
  return useQuery({
    queryKey: localKeys.github,
    queryFn: ({ signal }) => call(api.GET('/api/v1/local/github', { signal })),
    enabled,
    staleTime: 60_000,
  })
}

function useGitHubResult() {
  const qc = useQueryClient()
  return (gh: GitHubConnStatus) => qc.setQueryData(localKeys.github, gh)
}

export function useStartDeviceFlow() {
  return useMutation({
    mutationFn: () => call(api.POST('/api/v1/local/github/device', { params: { header: CSRF } })),
  })
}

/** One poll of the device flow; the caller schedules them `interval` seconds apart. */
export function usePollDeviceFlow() {
  const apply = useGitHubResult()
  return useMutation({
    mutationFn: () => call(api.POST('/api/v1/local/github/device/poll', { params: { header: CSRF } })),
    onSuccess: (res) => apply(res.github),
  })
}

/** Imports `gh auth token` (the UI asks first). */
export function useImportGhToken() {
  const apply = useGitHubResult()
  return useMutation({
    mutationFn: () => call(api.POST('/api/v1/local/github/gh', { params: { header: CSRF } })),
    onSuccess: apply,
  })
}

export function useSetGitHubToken() {
  const apply = useGitHubResult()
  return useMutation({
    mutationFn: (token: string) => call(api.POST('/api/v1/local/github/token', { params: { header: CSRF }, body: { token } })),
    onSuccess: apply,
  })
}

export function useDisconnectGitHub() {
  const apply = useGitHubResult()
  return useMutation({
    mutationFn: () => call(api.DELETE('/api/v1/local/github', { params: { header: CSRF } })),
    onSuccess: apply,
  })
}
