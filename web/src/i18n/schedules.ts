// Strings of the Schedules UI. Kept out of en.ts so they load with the lazy
// screens that use them.
import { usd } from '@/lib/format'

export const sc = {
  title: 'Schedules',
  open: 'Schedules',
  openHint: 'Recurring tasks: cron, budgets, history',
  intro: 'Each firing starts an ordinary task tagged with its schedule. Times are shown in your time zone.',
  newSchedule: 'New schedule',
  empty: 'No schedules yet. A schedule starts a task on a timetable: daily dependency updates, weekday lint fixes, a weekly report.',
  loadError: 'Could not load schedules.',
  next: 'Next',
  nextNone: 'No next run',
  paused: 'Paused',
  ended: 'Ended',
  monthSpend: (v: number, cap: number | null) => (cap ? `${usd(v)} of ${usd(cap)} this month` : `${usd(v)} this month`),
  never: 'Not run yet',
  running: 'Running',
  actions: (name: string) => `Actions for ${name}`,
  runNow: 'Run now',
  pause: 'Pause',
  resume: 'Resume',
  duplicate: 'Duplicate',
  edit: 'Edit',
  delete: 'Delete',
  deleteConfirm: (name: string) => `Delete “${name}”? Its tasks stay.`,
  deleted: (name: string) => `Deleted ${name}`,
  duplicated: (name: string) => `Created ${name} (paused)`,
  pausedToast: (name: string) => `Paused ${name}`,
  resumedToast: (name: string) => `Resumed ${name}`,
  fired: (outcome: string, task?: number | null) =>
    outcome === 'started' && task ? `Started task #${task}` : outcome === 'queued' ? 'Queued: waits for the previous task' : `Not started: ${outcome}`,

  // Detail
  history: 'History',
  noHistory: 'No firings yet.',
  outcome: (o: string) => ({ started: 'Started', queued: 'Queued', skipped: 'Skipped', missed: 'Missed', failed: 'Failed' })[o] ?? o,
  trigger: (t: string) => (t === 'manual' ? 'Run now' : 'Cron'),
  task: (id: number) => `Task #${id}`,
  prompt: 'Prompt',
  project: 'Project',
  when: 'When',
  budgets: 'Budgets',
  overlap: 'Overlap',
  perRun: (v: number | null) => (v ? `${usd(v)} per run` : 'Project budget per run'),
  perMonth: (v: number | null) => (v ? `${usd(v)} per month` : 'No monthly cap'),
  concurrencyLabel: (c: string) => (c === 'queue' ? 'Queue while the previous task runs' : 'Skip while the previous task runs'),
  prOnlyIfChanges: (on: boolean) => (on ? 'PR only if there are changes' : 'PR every time (empty commit without changes)'),
  endsAt: (d: string) => `Ends ${d}`,
  loopLine: (cmd: string, n: number) => `Loop until \`${cmd}\` passes (max ${n})`,
  notFound: 'Schedule not found.',

  // Editor
  newTitle: 'New schedule',
  editTitle: 'Edit schedule',
  name: 'Name',
  namePlaceholder: 'Nightly dependency update',
  nameRequired: 'Give the schedule a name',
  promptRequired: 'Describe the task',
  repository: 'Repository',
  agent: 'Agent',
  projectAgent: 'Project agent',
  frequency: 'Repeat',
  presets: { daily: 'Daily', weekdays: 'Weekdays', weekly: 'Weekly', custom: 'Custom (cron)' } as Record<string, string>,
  time: 'Time',
  weekday: 'Day',
  days: ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'],
  cron: 'Cron expression',
  cronHint: 'minute hour day-of-month month day-of-week, or @hourly, @daily, @weekly',
  cronRequired: 'Enter a cron expression',
  timezone: 'Time zone',
  skills: 'Skills',
  skillsHint: 'Added to the project’s skills',
  fromProject: 'project',
  mcp: 'MCP servers',
  mcpAll: 'All of the project’s servers',
  mcpHint: 'Untick servers this schedule’s tasks should not get',
  runBudget: 'Budget per run (USD)',
  monthlyBudget: 'Monthly cap (USD)',
  budgetPlaceholder: 'none',
  concurrency: 'While the previous task still runs',
  skip: 'Skip the firing',
  queue: 'Queue it (one waits)',
  prOnly: 'Open a PR only if there are changes',
  endDate: 'End date',
  endDateHint: 'Optional: no firings after this day',
  enabled: 'Enabled',
  save: 'Save',
  create: 'Create schedule',
  saved: 'Schedule saved',
  created: 'Schedule created',
  invalidBudget: 'Enter an amount, or leave it empty',
}

export function describeCron(cron: string): string {
  const m = /^(\d{1,2}) (\d{1,2}) \* \* (\*|1-5|[0-6])$/.exec(cron)
  if (!m) return cron
  const time = `${m[2].padStart(2, '0')}:${m[1].padStart(2, '0')}`
  if (m[3] === '*') return `Daily at ${time}`
  if (m[3] === '1-5') return `Weekdays at ${time}`
  return `${sc.days[Number(m[3])]}s at ${time}`
}
