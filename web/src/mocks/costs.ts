import { http, HttpResponse } from 'msw'
import type { CostReport, CostSettings, Run, Task } from '@/api/types'
import { addEvent, db, makeTask, startRun, taskView } from './db'

// Costs and budgets for mock mode (docs/API.md "Costs and budgets").

const DEFAULT_TABLE: CostSettings['price_table'] = {
  updated: '2026-11-17',
  prices: [
    { agent: 'codex', model: 'gpt-5.6-sol', input_usd: 4, cached_input_usd: 0.4, cache_write_usd: null, output_usd: 20 },
    { agent: 'codex', model: 'gpt-5.6-terra', input_usd: 2, cached_input_usd: 0.2, cache_write_usd: null, output_usd: 12 },
    { agent: 'codex', model: 'gpt-5.6-luna', input_usd: 0.2, cached_input_usd: 0.02, cache_write_usd: null, output_usd: 1.2 },
    { agent: 'codex', model: 'gpt-5.5', input_usd: 5, cached_input_usd: 0.5, cache_write_usd: null, output_usd: 30 },
    { agent: 'codex', model: 'gpt-5.4', input_usd: 2.5, cached_input_usd: 0.25, cache_write_usd: null, output_usd: 15 },
    { agent: 'codex', model: 'gpt-5.3-codex', input_usd: 1.75, cached_input_usd: 0.175, cache_write_usd: null, output_usd: 14 },
  ],
}

export const costs: { settings: CostSettings } = {
  settings: { monthly_budget_usd: null, price_table: DEFAULT_TABLE, price_table_is_default: true },
}

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString()

/** Fills in the usage fields of the seeded runs and adds tasks that show costs: an estimated Codex run and one waiting for the budget. */
export function seedCosts() {
  costs.settings = { monthly_budget_usd: null, price_table: DEFAULT_TABLE, price_table_is_default: true }
  for (const [taskId, runs] of db.runs) {
    const task = db.tasks.find((t) => t.id === taskId)
    for (const r of runs) {
      if (!r.started_at) continue
      Object.assign(r, {
        agent: task?.agent ?? 'claude',
        model: 'claude-opus-5-5',
        cost_source: 'reported',
        auth: 'subscription',
        input_tokens: Math.round(r.cost_usd * 90_000),
        output_tokens: Math.round(r.cost_usd * 4_000),
      })
    }
  }
  for (const t of db.tasks) if (t.cost_usd_total > 0) t.cost_api_equivalent = true
  const fixed = db.tasks.find((t) => t.autofix_state === 'green')
  if (fixed) fixed.autofix_cost_usd = 0.39

  // Codex reports tokens only: its cost is an estimate.
  const x = makeTask({ project_id: 'bioma', title: 'Add a dark mode toggle', status: 'queued', agent: 'codex', created_at: ago(60 * 5) })
  const xr = startRun(x.id, 'initial', x.prompt, ago(60 * 5))
  addEvent(x.id, 'started', { session_id: 't-1', model: 'gpt-5.5' }, true, ago(60 * 5))
  addEvent(x.id, 'usage', { cost_usd_total: 0, input_tokens: 420_000, output_tokens: 18_000, turns: 9, cache_read_tokens: 300_000, cache_write_tokens: 0 }, true, ago(60 * 5 - 20))
  addEvent(x.id, 'done', { session_id: 't-1', summary: 'Added the toggle to the header.', no_changes: false, cost_usd_total: 0 }, true, ago(60 * 5 - 21))
  // 120k × $5 + 300k × $0.50 + 18k × $30
  Object.assign(xr, { model: 'gpt-5.5', input_tokens: 420_000, output_tokens: 18_000, cache_read_tokens: 300_000, cost_usd: 1.29, cost_source: 'estimated', auth: null, exit_code: 0, finished_at: ago(60 * 5 - 21) })
  addEvent(x.id, 'run_finished', { run_id: xr.id, exit_code: 0, cost_usd: 1.29, cost_source: 'estimated' }, false, ago(60 * 5 - 21))
  Object.assign(x, { status: 'completed', finished_at: ago(60 * 5 - 21), cost_usd_total: 1.29, cost_estimated: true, turns: 9, updated_at: ago(60 * 5 - 21) })

  // Cursor "auto" has no price: its cost is unknown ($?).
  const u = makeTask({ project_id: 'bioma', title: 'Rename the theme tokens', status: 'queued', agent: 'cursor', created_at: ago(60 * 3) })
  const ur = startRun(u.id, 'initial', u.prompt, ago(60 * 3))
  addEvent(u.id, 'started', { session_id: 'c-1', model: 'auto' }, true, ago(60 * 3))
  addEvent(u.id, 'usage', { cost_usd_total: 0, input_tokens: 96_000, output_tokens: 4_100, turns: 3 }, true, ago(60 * 3 - 9))
  Object.assign(ur, { model: 'auto', input_tokens: 96_000, output_tokens: 4_100, cost_usd: 0, cost_source: 'unknown', exit_code: 0, finished_at: ago(60 * 3 - 10) })
  addEvent(u.id, 'run_finished', { run_id: ur.id, exit_code: 0, cost_usd: 0, cost_source: 'unknown' }, false, ago(60 * 3 - 10))
  Object.assign(u, { status: 'completed', finished_at: ago(60 * 3 - 10), cost_unknown: true, turns: 3, updated_at: ago(60 * 3 - 10) })

  // Vatary has a monthly budget it has used up: a new task waits.
  const vatary = db.projects.find((p) => p.id === 'vatary')
  if (vatary) vatary.budget = { monthly_usd: 3.5 }
  const w = makeTask({ project_id: 'vatary', title: 'Archive old webhook logs', status: 'queued', created_at: ago(8), updated_at: ago(8) })
  addEvent(w.id, 'status', { status: 'queued' }, false, ago(8))
  addEvent(w.id, 'budget', { phase: 'waiting', scope: 'project', project_id: 'vatary', spent_usd: 3.9, limit_usd: 3.5 }, false, ago(7))
  Object.assign(w, { budget_wait: 'project', status_reason: 'waiting: budget', last_activity: 'waiting: budget', last_activity_at: ago(7) })
}

const DAY = 86_400_000

function day(iso: string): string {
  const d = new Date(iso)
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

function emptySum() {
  return { cost_usd: 0, estimated_usd: 0, autofix_usd: 0, api_equivalent_usd: 0, runs: 0, unknown_runs: 0, input_tokens: 0, output_tokens: 0 }
}
type Sum = ReturnType<typeof emptySum>

function add(s: Sum, r: Run) {
  s.cost_usd += r.cost_usd
  s.runs++
  s.input_tokens += r.input_tokens
  s.output_tokens += r.output_tokens
  if (r.cost_source === 'estimated') s.estimated_usd += r.cost_usd
  if (r.cost_source === 'unknown') s.unknown_runs++
  if (r.autofix_attempt) s.autofix_usd += r.cost_usd
  if (r.auth === 'subscription' && r.cost_source === 'reported') s.api_equivalent_usd += r.cost_usd
}

function round(s: Sum): Sum {
  for (const k of ['cost_usd', 'estimated_usd', 'autofix_usd', 'api_equivalent_usd'] as const) s[k] = Math.round(s[k] * 1e4) / 1e4
  return s
}

function periodStart(d: string, period: string): string {
  const date = new Date(`${d}T00:00:00`)
  if (period === 'month') return day(new Date(date.getFullYear(), date.getMonth(), 1).toISOString())
  if (period === 'week') return day(new Date(date.getTime() - ((date.getDay() + 6) % 7) * DAY).toISOString())
  return d
}

function startedRuns(): { run: Run; task: Task }[] {
  const out: { run: Run; task: Task }[] = []
  for (const [taskId, runs] of db.runs) {
    const task = db.tasks.find((t) => t.id === taskId)
    if (!task) continue
    for (const run of runs) if (run.started_at) out.push({ run, task })
  }
  return out
}

/** Month-to-date cost per project (Project.month_cost_usd). */
export function monthCost(projectId: string): number {
  const now = new Date()
  const start = day(new Date(now.getFullYear(), now.getMonth(), 1).toISOString())
  return round({ ...emptySum(), cost_usd: startedRuns().filter(({ run, task }) => task.project_id === projectId && day(run.started_at!) >= start).reduce((n, { run }) => n + run.cost_usd, 0) }).cost_usd
}

/** Whether some of a project's runs this month have no price. */
export function monthUnknown(projectId: string): boolean {
  const now = new Date()
  const start = day(new Date(now.getFullYear(), now.getMonth(), 1).toISOString())
  return startedRuns().some(({ run, task }) => task.project_id === projectId && run.cost_source === 'unknown' && day(run.started_at!) >= start)
}

/** GET /costs over the mock runs, like costs.Aggregate on the server. */
export function costReport(from: string, to: string, period: string): CostReport {
  const total = emptySum()
  const projects = new Map<string, Sum>()
  const agents = new Map<string, Sum>()
  const rows = new Map<string, Sum & { day: string; project_id: string; agent: string }>()
  const periods: { start: string; sum: Sum }[] = []
  for (let d = periodStart(from, period); d <= to; ) {
    periods.push({ start: d, sum: emptySum() })
    const date = new Date(`${d}T00:00:00`)
    d =
      period === 'month'
        ? day(new Date(date.getFullYear(), date.getMonth() + 1, 1).toISOString())
        : day(new Date(date.getFullYear(), date.getMonth(), date.getDate() + (period === 'week' ? 7 : 1)).toISOString())
  }
  for (const { run, task } of startedRuns()) {
    const d = day(run.started_at!)
    if (d < from || d > to) continue
    const agent = run.agent ?? task.agent
    add(total, run)
    if (!projects.has(task.project_id)) projects.set(task.project_id, emptySum())
    add(projects.get(task.project_id)!, run)
    if (!agents.has(agent)) agents.set(agent, emptySum())
    add(agents.get(agent)!, run)
    const p = periods.find((x) => x.start === periodStart(d, period))
    if (p) add(p.sum, run)
    const key = `${d}|${task.project_id}|${agent}`
    if (!rows.has(key)) rows.set(key, { ...emptySum(), day: d, project_id: task.project_id, agent })
    add(rows.get(key)!, run)
  }
  const keyed = (m: Map<string, Sum>) => [...m].map(([key, s]) => ({ key, ...round(s) })).sort((a, b) => b.cost_usd - a.cost_usd)
  const now = new Date()
  const monthStart = day(new Date(now.getFullYear(), now.getMonth(), 1).toISOString())
  const monthTotal = db.projects.reduce((n, p) => n + monthCost(p.id), 0)
  return {
    from,
    to,
    period: period as CostReport['period'],
    month_start: monthStart,
    budget: {
      monthly_usd: costs.settings.monthly_budget_usd,
      month_cost_usd: Math.round(monthTotal * 1e4) / 1e4,
      month_cost_unknown: db.projects.some((p) => monthUnknown(p.id)),
    },
    projects: db.projects.map((p) => ({
      project_id: p.id,
      name: p.name,
      monthly_usd: p.budget?.monthly_usd ?? null,
      month_cost_usd: monthCost(p.id),
      month_cost_unknown: monthUnknown(p.id),
    })),
    total: round(total),
    by_project: keyed(projects),
    by_agent: keyed(agents),
    by_period: periods.map((p) => ({ start: p.start, ...round(p.sum) })),
    rows: [...rows.values()].map((r) => round(r) as typeof r).sort((a, b) => (a.day + a.project_id + a.agent).localeCompare(b.day + b.project_id + b.agent)),
  }
}

type Guard = (request: Request) => Response | null
type Err = (status: number, code: string, message: string) => Response

export function costHandlers(API: string, guard: Guard, err: Err) {
  return [
    http.get(`${API}/settings/costs`, ({ request }) => guard(request) ?? HttpResponse.json(costs.settings)),
    http.put(`${API}/settings/costs`, async ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const body = (await request.json()) as { monthly_budget_usd: number | null; price_table: CostSettings['price_table'] | null }
      if (body.monthly_budget_usd != null && body.monthly_budget_usd < 0) return err(400, 'invalid', 'monthly_budget_usd must be 0 to 1000000')
      costs.settings = {
        monthly_budget_usd: body.monthly_budget_usd || null,
        price_table: body.price_table ?? DEFAULT_TABLE,
        price_table_is_default: body.price_table == null,
      }
      return HttpResponse.json(costs.settings)
    }),
    http.get(`${API}/costs`, ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const q = new URL(request.url).searchParams
      const now = new Date()
      const from = q.get('from') || day(new Date(now.getFullYear(), now.getMonth(), 1).toISOString())
      const to = q.get('to') || day(now.toISOString())
      return HttpResponse.json(costReport(from, to, q.get('period') || 'day'))
    }),
    http.post(`${API}/tasks/:id/budget-override`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = db.tasks.find((t) => t.id === Number(params.id))
      if (!task) return err(404, 'not_found', 'No such task')
      if (task.status !== 'queued') return err(409, 'conflict', 'only a queued task can start despite the budget')
      Object.assign(task, { budget_wait: null, budget_override_at: new Date().toISOString(), status_reason: null })
      addEvent(task.id, 'budget', { phase: 'override', via: 'web' })
      return HttpResponse.json(taskView(task))
    }),
  ]
}
