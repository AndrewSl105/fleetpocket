import type { LoopInput, Schedule, ScheduleInput } from '@/api/types'
import { sc } from '@/i18n/schedules'

export type Preset = 'daily' | 'weekdays' | 'weekly' | 'custom'

export type LoopValues = { enabled: boolean; check: string; max: number; budget: string }

export type ScheduleValues = {
  name: string
  project_id: string
  repository_id: string
  prompt: string
  agent: '' | 'claude' | 'codex' | 'cursor'
  skills: string[]
  /** null: all of the project's servers. */
  mcp_servers: string[] | null
  preset: Preset
  time: string
  weekday: number
  cron: string
  timezone: string
  run_budget: string
  monthly_budget: string
  concurrency: 'skip' | 'queue'
  pr_only_if_changes: boolean
  /** yyyy-mm-dd or ''. */
  ends_on: string
  enabled: boolean
  loop: LoopValues
}

export const NO_LOOP: LoopValues = { enabled: false, check: '', max: 3, budget: '' }

/** The viewer's IANA time zone (the default of a new schedule). */
export function localZone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
  } catch {
    return 'UTC'
  }
}

/** Time zones the browser knows (a fallback list when it cannot say). */
export function zones(current: string): string[] {
  let all: string[]
  try {
    all = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf?.('timeZone') ?? []
  } catch {
    all = []
  }
  if (!all.length) all = ['UTC', 'Europe/London', 'Europe/Berlin', 'Europe/Kyiv', 'America/New_York', 'America/Los_Angeles', 'Asia/Tokyo']
  if (!all.includes('UTC')) all = ['UTC', ...all]
  return all.includes(current) ? all : [current, ...all]
}

/** The cron expression of a preset ("09:30" → "30 9 * * 1-5"). */
export function presetCron(preset: Preset, time: string, weekday: number, custom: string): string {
  if (preset === 'custom') return custom.trim()
  const [h, m] = time.split(':').map((x) => Number(x))
  const hm = `${Number.isFinite(m) ? m : 0} ${Number.isFinite(h) ? h : 0}`
  if (preset === 'daily') return `${hm} * * *`
  if (preset === 'weekdays') return `${hm} * * 1-5`
  return `${hm} * * ${weekday}`
}

/** Reads a cron expression back into a preset when it is one. */
export function parsePreset(cron: string): { preset: Preset; time: string; weekday: number } {
  const m = /^(\d{1,2}) (\d{1,2}) \* \* (\*|1-5|[0-6])$/.exec(cron.trim())
  if (!m) return { preset: 'custom', time: '09:00', weekday: 1 }
  const time = `${m[2].padStart(2, '0')}:${m[1].padStart(2, '0')}`
  if (m[3] === '*') return { preset: 'daily', time, weekday: 1 }
  if (m[3] === '1-5') return { preset: 'weekdays', time, weekday: 1 }
  return { preset: 'weekly', time, weekday: Number(m[3]) }
}

export function emptyValues(projectId = ''): ScheduleValues {
  return {
    name: '',
    project_id: projectId,
    repository_id: '',
    prompt: '',
    agent: '',
    skills: [],
    mcp_servers: null,
    preset: 'weekdays',
    time: '09:00',
    weekday: 1,
    cron: '0 9 * * 1-5',
    timezone: localZone(),
    run_budget: '',
    monthly_budget: '',
    concurrency: 'skip',
    pr_only_if_changes: true,
    ends_on: '',
    enabled: true,
    loop: NO_LOOP,
  }
}

const money = (v: number | null) => (v == null ? '' : String(v))

export function toValues(s: Schedule): ScheduleValues {
  const p = parsePreset(s.cron)
  return {
    name: s.name,
    project_id: s.project_id,
    repository_id: s.repository_id == null ? '' : String(s.repository_id),
    prompt: s.prompt,
    agent: s.agent ?? '',
    skills: s.skills,
    mcp_servers: s.mcp_servers,
    preset: p.preset,
    time: p.time,
    weekday: p.weekday,
    cron: s.cron,
    timezone: s.timezone,
    run_budget: money(s.run_budget_usd),
    monthly_budget: money(s.monthly_budget_usd),
    concurrency: s.concurrency,
    pr_only_if_changes: s.pr_only_if_changes,
    ends_on: s.ends_at ? s.ends_at.slice(0, 10) : '',
    enabled: s.enabled,
    loop: s.loop
      ? { enabled: true, check: s.loop.check_command, max: s.loop.max_iterations, budget: money(s.loop.budget_usd) }
      : NO_LOOP,
  }
}

/** "" → null; anything else must be a non-negative number. */
export function parseMoney(v: string): number | null | undefined {
  const t = v.trim()
  if (!t) return null
  const n = Number(t)
  return Number.isFinite(n) && n >= 0 ? n : undefined
}

export function loopInput(l: LoopValues): LoopInput | null {
  if (!l.enabled) return null
  const budget = parseMoney(l.budget)
  return { check_command: l.check.trim(), max_iterations: l.max, ...(budget ? { budget_usd: budget } : {}) }
}

/** The PUT/POST body (ScheduleInput, additionalProperties false). */
export function toInput(v: ScheduleValues): ScheduleInput {
  // ends_at: the last second of the end day, UTC.
  return {
    name: v.name.trim(),
    project_id: v.project_id,
    repository_id: v.repository_id ? Number(v.repository_id) : null,
    prompt: v.prompt.trim(),
    agent: v.agent || null,
    skills: v.skills,
    mcp_servers: v.mcp_servers,
    cron: presetCron(v.preset, v.time, v.weekday, v.cron),
    timezone: v.timezone,
    run_budget_usd: parseMoney(v.run_budget) ?? null,
    monthly_budget_usd: parseMoney(v.monthly_budget) ?? null,
    concurrency: v.concurrency,
    pr_only_if_changes: v.pr_only_if_changes,
    ends_at: v.ends_on ? `${v.ends_on}T23:59:59Z` : null,
    loop: loopInput(v.loop),
    enabled: v.enabled,
  }
}

/** The next run in the viewer's own time zone. */
export function nextRun(s: Pick<Schedule, 'enabled' | 'next_run_at' | 'ends_at'>): string {
  if (!s.enabled) return sc.paused
  if (!s.next_run_at) return s.ends_at ? sc.ended : sc.nextNone
  const d = new Date(s.next_run_at)
  const day = new Intl.DateTimeFormat('en-GB', { weekday: 'short', day: 'numeric', month: 'short' }).format(d)
  const time = new Intl.DateTimeFormat('en-GB', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(d)
  return `${day}, ${time}`
}
