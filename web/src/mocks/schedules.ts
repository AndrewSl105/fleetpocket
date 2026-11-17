import { http, HttpResponse } from 'msw'
import type { LoopInput, Schedule, ScheduleFiring, ScheduleInput, Task } from '@/api/types'
import { addEvent, db, makeTask, startRun } from './db'

// Schedules and a finished loop for mock mode (docs/API.md "Schedules", "Loops").

type Guard = (request: Request) => Response | null
type Err = (status: number, code: string, message: string) => Response

type Row = Omit<Schedule, 'month_spend_usd' | 'busy' | 'last_firing'>

export const scheduleState: { rows: Row[]; firings: Omit<ScheduleFiring, 'task_status' | 'task_cost_usd' | 'task_pr_url' | 'loop_state'>[]; nextId: number; nextFiring: number } = {
  rows: [],
  firings: [],
  nextId: 1,
  nextFiring: 1,
}

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString()
const BUSY = ['queued', 'preparing', 'running', 'waiting_input', 'paused', 'pushing']

/** The next time "M H * * D" fires in the browser's zone (good enough for the mock). */
function nextRun(cron: string, enabled: boolean): string | null {
  if (!enabled) return null
  const m = /^(\d{1,2}) (\d{1,2}) \* \* (\*|1-5|[0-6])$/.exec(cron)
  const d = new Date()
  if (!m) return new Date(Date.now() + 3_600_000).toISOString()
  d.setSeconds(0, 0)
  d.setHours(Number(m[2]), Number(m[1]))
  for (let i = 0; i < 8; i++) {
    const day = d.getDay()
    const ok = m[3] === '*' || (m[3] === '1-5' ? day >= 1 && day <= 5 : day === Number(m[3]))
    if (ok && d.getTime() > Date.now()) return d.toISOString()
    d.setDate(d.getDate() + 1)
  }
  return d.toISOString()
}

function view(r: Row): Schedule {
  const tasks = db.tasks.filter((t) => t.schedule_id === r.id)
  const fs = scheduleState.firings.filter((f) => f.schedule_id === r.id)
  const last = fs.length ? fs[fs.length - 1] : null
  return {
    ...r,
    month_spend_usd: Math.round(tasks.reduce((s, t) => s + t.cost_usd_total, 0) * 100) / 100,
    busy: tasks.some((t) => BUSY.includes(t.status)),
    last_firing: last ? firingView(last) : null,
  }
}

function firingView(f: (typeof scheduleState.firings)[number]): ScheduleFiring {
  const t = f.task_id != null ? db.tasks.find((x) => x.id === f.task_id) : undefined
  return { ...f, task_status: t?.status ?? null, task_cost_usd: t ? t.cost_usd_total : null, task_pr_url: t?.pr_url ?? null, loop_state: t?.loop_state ?? null }
}

function loopOf(l: LoopInput | null | undefined) {
  return l ? { check_command: l.check_command, max_iterations: l.max_iterations ?? 3, budget_usd: l.budget_usd ?? null, ...(l.timeout_sec ? { timeout_sec: l.timeout_sec } : {}) } : null
}

function fromInput(b: ScheduleInput, id: number, created: string): Row {
  const enabled = b.enabled ?? true
  return {
    id,
    name: b.name.trim(),
    project_id: b.project_id,
    repository_id: b.repository_id ?? null,
    prompt: b.prompt.trim(),
    agent: b.agent ?? null,
    skills: b.skills ?? [],
    mcp_servers: b.mcp_servers ?? null,
    cron: b.cron.trim(),
    timezone: b.timezone || 'UTC',
    run_budget_usd: b.run_budget_usd || null,
    monthly_budget_usd: b.monthly_budget_usd || null,
    concurrency: b.concurrency ?? 'skip',
    pr_only_if_changes: b.pr_only_if_changes ?? true,
    ends_at: b.ends_at ?? null,
    loop: loopOf(b.loop),
    enabled,
    next_run_at: nextRun(b.cron.trim(), enabled),
    created_at: created,
    updated_at: new Date().toISOString(),
  }
}

/** A loop task: the check failed in iteration 1, passed in iteration 2, then the PR opened. */
function seedLoopTask(scheduleId: number | null): Task {
  const t = makeTask({ project_id: 'vatary', title: 'Make the audit tests pass', status: 'queued', created_at: ago(300), cost_usd_total: 0.64 })
  t.prompt = 'The audit export tests fail since the pagination change. Make them pass.'
  Object.assign(t, {
    loop: { check_command: 'pnpm test audit', max_iterations: 3, budget_usd: 2 },
    loop_state: 'passed',
    loop_iteration: 2,
    schedule_id: scheduleId,
    source: scheduleId ? 'schedule' : 'web',
  })
  const iter = (n: number, at: string, extra: Record<string, unknown> = {}) =>
    addEvent(t.id, 'loop', { phase: 'iteration', iteration: n, max_iterations: 3, ...extra }, false, at)
  if (scheduleId) addEvent(t.id, 'schedule', { schedule_id: scheduleId, name: 'Nightly test fixer', trigger: 'cron', scheduled_for: ago(300) }, false, ago(300))
  iter(1, ago(299), { check_command: 'pnpm test audit' })
  startRun(t.id, 'initial', t.prompt, ago(299))
  addEvent(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit src/audit/export.ts' }, true, ago(296))
  addEvent(t.id, 'tool_result', { tool: 'Edit', ok: true }, true, ago(296))
  addEvent(t.id, 'committed', { sha: 'a41c0e9b12f3', files_changed: 1, insertions: 6, deletions: 2 }, true, ago(294))
  addEvent(
    t.id,
    'check',
    {
      command: 'pnpm test audit',
      exit_code: 1,
      output: ' FAIL  src/audit/export.test.ts\n  ✕ returns next cursor on the last page (14 ms)\n\n  Expected: ""\n  Received: "MjAyNi0xMS0xNw"\n\nTests: 1 failed, 11 passed, 12 total',
      duration_ms: 8400,
      timed_out: false,
      iteration: 1,
      max_iterations: 3,
    },
    true,
    ago(293),
  )
  addEvent(t.id, 'done', { session_id: 's-loop', summary: 'Fixed the cursor encoding.', no_changes: false, cost_usd_total: 0.31 }, true, ago(293))
  iter(2, ago(292), { exit_code: 1, reason: 'check failed (exit code 1); iteration 2/3' })
  startRun(t.id, 'loop', 'Loop iteration 2 of 3. After your last changes the check `pnpm test audit` failed (exit code 1).', ago(292))
  addEvent(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit src/audit/routes.ts' }, true, ago(290))
  addEvent(t.id, 'tool_result', { tool: 'Edit', ok: true }, true, ago(290))
  addEvent(t.id, 'committed', { sha: 'c93e1d07aa45', files_changed: 1, insertions: 3, deletions: 1 }, true, ago(288))
  addEvent(t.id, 'check', { command: 'pnpm test audit', exit_code: 0, output: 'Tests: 12 passed, 12 total', duration_ms: 7900, timed_out: false, iteration: 2, max_iterations: 3 }, true, ago(287))
  addEvent(t.id, 'done', { session_id: 's-loop', summary: 'The last page now returns an empty cursor; all audit tests pass.', no_changes: false, cost_usd_total: 0.64 }, true, ago(287))
  addEvent(t.id, 'loop', { phase: 'stopped', state: 'passed', iteration: 2, max_iterations: 3, reason: 'check passed in iteration 2/3', spent_usd: 0.64 }, false, ago(287))
  addEvent(t.id, 'pr_opened', { number: 64, url: 'https://github.com/andrewsl105/vatary-server/pull/64' }, false, ago(286))
  Object.assign(t, { status: 'pr_open', pr_number: 64, pr_url: 'https://github.com/andrewsl105/vatary-server/pull/64', ci_status: 'success', turns: 9, updated_at: ago(286) })
  return t
}

export function seedSchedules() {
  scheduleState.rows = []
  scheduleState.firings = []
  scheduleState.nextId = 1
  scheduleState.nextFiring = 1
  const base = { repository_id: null, agent: null, skills: [], mcp_servers: null, run_budget_usd: null, ends_at: null, created_at: ago(20_000), updated_at: ago(20_000) }
  const rows: Row[] = [
    { ...base, id: 1, name: 'Nightly test fixer', project_id: 'vatary', prompt: 'Run the test suite and fix what fails.', cron: '0 3 * * *', timezone: 'Europe/Kyiv', monthly_budget_usd: 20, concurrency: 'skip', pr_only_if_changes: true, loop: { check_command: 'pnpm test audit', max_iterations: 3, budget_usd: 2 }, enabled: true, next_run_at: nextRun('0 3 * * *', true) },
    { ...base, id: 2, name: 'Weekday dependency update', project_id: 'vatary', prompt: 'Update patch and minor dependencies; keep the lockfile consistent.', cron: '30 8 * * 1-5', timezone: 'Europe/Kyiv', monthly_budget_usd: null, run_budget_usd: 1, concurrency: 'queue', pr_only_if_changes: true, skills: ['pr-checklist'], loop: null, enabled: true, next_run_at: nextRun('30 8 * * 1-5', true) },
    { ...base, id: 3, name: 'Weekly docs report', project_id: 'vatary', prompt: 'Summarise what changed in docs/ this week in a PR description.', cron: '0 17 * * 5', timezone: 'UTC', monthly_budget_usd: 5, concurrency: 'skip', pr_only_if_changes: false, loop: null, enabled: false, next_run_at: null },
  ]
  scheduleState.rows = rows
  scheduleState.nextId = 4
  const loopTask = seedLoopTask(1)
  const fire = (schedule_id: number, minAgo: number, outcome: ScheduleFiring['outcome'], task_id: number | null = null, reason: string | null = null, trigger: 'cron' | 'manual' = 'cron') =>
    scheduleState.firings.push({ id: scheduleState.nextFiring++, schedule_id, scheduled_for: ago(minAgo), trigger, outcome, reason, task_id, created_at: ago(minAgo) })
  fire(1, 1740, 'missed', null, 'missed: the server was not running for more than one interval')
  fire(1, 300, 'started', loopTask.id)
  const dep = makeTask({ project_id: 'vatary', title: 'Weekday dependency update', status: 'completed', created_at: ago(400), cost_usd_total: 0.18 })
  Object.assign(dep, { source: 'schedule', schedule_id: 2, status_reason: 'finished with no changes', finished_at: ago(395) })
  fire(2, 1840, 'skipped', null, 'the previous task is still running')
  fire(2, 400, 'started', dep.id)
}

export function scheduleHandlers(API: string, guard: Guard, err: Err) {
  const find = (id: unknown) => scheduleState.rows.find((r) => r.id === Number(id))
  const validate = (b: ScheduleInput): string | null => {
    if (!b.name?.trim()) return 'name is required'
    if (!b.prompt?.trim()) return 'prompt is required'
    if (!db.projects.some((p) => p.id === b.project_id)) return `project "${b.project_id}" does not exist`
    const c = b.cron?.trim() ?? ''
    if (!/^@(hourly|daily|weekly|monthly|yearly)$/.test(c) && c.split(/\s+/).length !== 5) return `cron: "${c}" needs 5 fields`
    if (b.loop && !b.loop.check_command?.trim()) return 'loop.check_command is required'
    return null
  }
  const startTask = (r: Row, trigger: 'cron' | 'manual') => {
    const busy = db.tasks.some((t) => t.schedule_id === r.id && BUSY.includes(t.status))
    const base = { id: scheduleState.nextFiring++, schedule_id: r.id, scheduled_for: new Date().toISOString(), trigger, created_at: new Date().toISOString() }
    if (busy) {
      const waiting = scheduleState.firings.some((f) => f.schedule_id === r.id && f.outcome === 'queued')
      const f = r.concurrency === 'queue' && !waiting
        ? { ...base, outcome: 'queued' as const, reason: 'waits for the previous task', task_id: null }
        : { ...base, outcome: 'skipped' as const, reason: waiting ? 'a firing already waits for the previous task' : 'the previous task is still running', task_id: null }
      scheduleState.firings.push(f)
      return f
    }
    const t = makeTask({ project_id: r.project_id, title: r.name, status: 'queued' })
    Object.assign(t, { prompt: r.prompt, source: 'schedule', schedule_id: r.id, loop: r.loop, loop_state: r.loop ? 'running' : null, loop_iteration: r.loop ? 1 : 0, skills_add: r.skills })
    addEvent(t.id, 'created', { attachments: [] })
    if (r.loop) addEvent(t.id, 'loop', { phase: 'iteration', iteration: 1, max_iterations: r.loop.max_iterations, check_command: r.loop.check_command })
    startRun(t.id, 'initial', t.prompt)
    addEvent(t.id, 'status', { status: 'queued' })
    addEvent(t.id, 'schedule', { schedule_id: r.id, name: r.name, trigger, scheduled_for: base.scheduled_for, via: 'web' })
    const f = { ...base, outcome: 'started' as const, reason: null, task_id: t.id }
    scheduleState.firings.push(f)
    return f
  }
  return [
    http.get(`${API}/schedules`, ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const items = [...scheduleState.rows].sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase())).map(view)
      return HttpResponse.json({ items })
    }),
    http.post(`${API}/schedules`, async ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const b = (await request.json()) as ScheduleInput
      const bad = validate(b)
      if (bad) return err(400, 'invalid', bad)
      const r = fromInput(b, scheduleState.nextId++, new Date().toISOString())
      scheduleState.rows.push(r)
      return HttpResponse.json(view(r), { status: 201 })
    }),
    http.get(`${API}/schedules/:id`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      return r ? HttpResponse.json(view(r)) : err(404, 'not_found', 'No such schedule')
    }),
    http.put(`${API}/schedules/:id`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such schedule')
      const b = (await request.json()) as ScheduleInput
      const bad = validate(b)
      if (bad) return err(400, 'invalid', bad)
      Object.assign(r, fromInput(b, r.id, r.created_at))
      return HttpResponse.json(view(r))
    }),
    http.delete(`${API}/schedules/:id`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such schedule')
      scheduleState.rows = scheduleState.rows.filter((x) => x !== r)
      for (const t of db.tasks) if (t.schedule_id === r.id) t.schedule_id = null
      return HttpResponse.json({ ok: true })
    }),
    http.post(`${API}/schedules/:id/:action`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such schedule')
      switch (params.action) {
        case 'enable':
        case 'disable':
          r.enabled = params.action === 'enable'
          r.next_run_at = nextRun(r.cron, r.enabled)
          return HttpResponse.json(view(r))
        case 'run': {
          const f = startTask(r, 'manual')
          return HttpResponse.json(firingView(f), { status: 201 })
        }
        case 'duplicate': {
          const copy: Row = { ...r, id: scheduleState.nextId++, name: `${r.name} (copy)`.slice(0, 80), enabled: false, next_run_at: null, created_at: new Date().toISOString() }
          scheduleState.rows.push(copy)
          return HttpResponse.json(view(copy), { status: 201 })
        }
      }
      return err(404, 'not_found', 'No such action')
    }),
    http.get(`${API}/schedules/:id/firings`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such schedule')
      const items = scheduleState.firings.filter((f) => f.schedule_id === r.id).reverse().map(firingView)
      return HttpResponse.json({ items })
    }),
  ]
}

/** The loop of a task created in mock mode (POST /tasks with `loop`). */
export function mockLoop(task: Task, loop: LoopInput | null | undefined) {
  const l = loopOf(loop)
  if (!l) return
  Object.assign(task, { loop: l, loop_state: 'running', loop_iteration: 1 })
  addEvent(task.id, 'loop', { phase: 'iteration', iteration: 1, max_iterations: l.max_iterations, check_command: l.check_command })
}

