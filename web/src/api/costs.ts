// Costs and budgets hooks. A module of their own so they load with the lazy
// screens that use them, not in the initial bundle.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { toast } from 'sonner'
import { api, call, CSRF } from './client'
import { keys } from './keys'
import { upsertTask } from './cache'
import { errorMessage } from './hooks'
import type { CostReport, CostSettings, PriceRow, TaskView } from './types'
import { haptic } from '@/telegram/haptics'
import { c } from '@/i18n/costs'

export type CostPeriod = 'day' | 'week' | 'month'

export function useCosts(from: string, to: string, period: CostPeriod) {
  return useQuery({
    queryKey: keys.costs(from, to, period),
    queryFn: ({ signal }) => call(api.GET('/api/v1/costs', { params: { query: { from, to, period } }, signal })),
    staleTime: 30_000,
  })
}

export function useCostSettings() {
  return useQuery({
    queryKey: keys.costSettings,
    queryFn: ({ signal }) => call(api.GET('/api/v1/settings/costs', { signal })),
    staleTime: 60_000,
  })
}

export type CostSettingsInput = { monthly_budget_usd: number | null; price_table: { updated?: string; prices: PriceRow[] } | null }

export function useSaveCostSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: CostSettingsInput) => call(api.PUT('/api/v1/settings/costs', { params: { header: CSRF }, body })),
    onSuccess: (data: CostSettings) => {
      qc.setQueryData(keys.costSettings, data)
      void qc.invalidateQueries({ queryKey: ['costs'] })
    },
  })
}

/** POST /tasks/{id}/budget-override: a task waiting for a monthly budget starts anyway. */
export function useBudgetOverride(taskId: number) {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () =>
      call(api.POST('/api/v1/tasks/{id}/budget-override', { params: { path: { id: taskId }, header: CSRF } })),
    onSuccess: (task: TaskView) => {
      haptic.success()
      qc.setQueryData(keys.tasks.detail(task.id), task)
      upsertTask(qc, task)
      toast.success(c.started)
    },
    onError: (e) => {
      haptic.error()
      toast.error(errorMessage(e))
    },
  })
}

/**
 * The report's rows as CSV (one row per day, project and agent). cost_usd is
 * "unknown" when none of the row's runs has a price; with some priced, it is
 * the known part and unknown_runs counts the others.
 */
export function costsCsv(report: CostReport): string {
  const head = ['day', 'project_id', 'agent', 'cost_usd', 'estimated_usd', 'autofix_usd', 'api_equivalent_usd', 'runs', 'unknown_runs', 'input_tokens', 'output_tokens']
  const cell = (v: string | number) => {
    const s = String(v)
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const lines = [head.join(',')]
  for (const r of report.rows) {
    lines.push(
      [r.day, r.project_id, r.agent, r.runs > 0 && r.unknown_runs === r.runs ? 'unknown' : r.cost_usd, r.estimated_usd, r.autofix_usd, r.api_equivalent_usd, r.runs, r.unknown_runs, r.input_tokens, r.output_tokens]
        .map(cell)
        .join(','),
    )
  }
  return lines.join('\n') + '\n'
}
