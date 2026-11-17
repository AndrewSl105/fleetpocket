import type { TaskGroup } from '@/lib/status'

export type TaskFilter = { group: TaskGroup; project?: string }

export const keys = {
  tasks: {
    all: ['tasks'] as const,
    lists: () => ['tasks', 'list'] as const,
    list: (filter: TaskFilter) => ['tasks', 'list', filter] as const,
    detail: (id: number) => ['tasks', 'detail', id] as const,
    events: (id: number) => ['tasks', 'events', id] as const,
    runs: (id: number) => ['tasks', 'runs', id] as const,
    diff: (id: number) => ['tasks', 'diff', id] as const,
    attachment: (taskId: number, id: number, thumb: boolean) => ['tasks', 'attachment', taskId, id, thumb] as const,
  },
  projects: {
    all: ['projects'] as const,
    list: () => ['projects', 'list'] as const,
    detail: (id: string) => ['projects', 'detail', id] as const,
  },
  nodes: ['nodes'] as const,
  githubRepos: ['github', 'repositories'] as const,
  me: ['me'] as const,
  stats: ['stats'] as const,
  autofixDefaults: ['settings', 'ci-autofix'] as const,
  costSettings: ['settings', 'costs'] as const,
  costs: (from: string, to: string, period: string) => ['costs', from, to, period] as const,
  integrations: (projectId: string) => ['integrations', projectId] as const,
  integrationsAll: ['integrations'] as const,
  globalMCP: ['mcp-servers', 'global'] as const,
  health: ['health'] as const,
}
