// Strings of the costs and budgets UI. Kept out of en.ts so they load with
// the lazy screens that use them (the initial bundle has a size budget).
import { usd } from '@/lib/format'

const scopeName = (scope: string | undefined, project?: string) =>
  scope === 'project' ? `${project ?? 'This project'}'s monthly budget` : 'The global monthly budget'

export const c = {
  title: 'Costs',
  open: 'Costs',
  openHint: 'Totals per project, agent and day',
  thisMonth: 'This month',
  range: { month: 'This month', last30: 'Last 30 days', last90: 'Last 90 days' },
  rangeLabel: 'Range',
  total: 'Total',
  estimated: (v: number) => `${usd(v, true)} estimated`,
  autofix: (v: number) => `${usd(v)} CI auto-fix`,
  apiEquivalent: 'API-equivalent',
  apiEquivalentHint: 'Claude subscription runs: what the same tokens would cost on the API, not a bill.',
  apiEquivalentPart: (v: number) => `${usd(v)} API-equivalent`,
  unknownRuns: (n: number) => `${n} ${n === 1 ? 'run' : 'runs'} without a price ($?)`,
  runs: (n: number) => `${n} ${n === 1 ? 'run' : 'runs'}`,
  tokens: (input: number, output: number) => `${compact(input)} in · ${compact(output)} out`,
  byProject: 'By project',
  byAgent: 'By agent',
  byDay: 'By day',
  byWeek: 'By week',
  byMonth: 'By month',
  budget: 'Budget',
  ofBudget: (spent: number, cap: number, unknown = false) => `${usd(spent, false, unknown)} of ${usd(cap)}`,
  noBudget: 'No monthly cap',
  monthToDate: (v: number, unknown = false) => `${usd(v, false, unknown)} this month`,
  exportCsv: 'Export CSV',
  empty: 'No runs in this range.',
  csvFile: (from: string, to: string) => `costs-${from}-to-${to}.csv`,

  // Settings → Budgets and prices
  settingsTitle: 'Budgets and prices',
  settingsHint:
    'Monthly caps stop new runs from starting once they are used up (running ones finish); you get a notice at 80%. Prices estimate Codex and Cursor runs, which report tokens but no cost.',
  globalCap: 'Global monthly budget (USD)',
  globalCapHint: 'Empty: no cap. Each project can have its own cap too.',
  prices: 'Price table (USD per 1M tokens)',
  pricesUpdated: (d: string) => `Prices last checked ${d}`,
  pricesDefault: 'Built-in table',
  pricesHint: 'Model “*” matches any model of the agent. Codex “default” and Cursor “auto” need a row of their own.',
  agent: 'Agent',
  model: 'Model',
  input: 'Input',
  cachedInput: 'Cached',
  output: 'Output',
  addPrice: 'Add price',
  removePrice: (model: string) => `Remove ${model || 'row'}`,
  resetPrices: 'Restore built-in prices',
  saved: 'Cost settings saved',

  // Project form
  projectCap: 'Monthly budget (USD)',
  projectCapHint: 'New runs of this project wait once it is used up. Empty: no cap.',

  // Task header and timeline
  autofixShare: (v: number) => `incl. ${usd(v)} auto-fix`,
  waitingBudget: (scope: string | null) =>
    scope === 'project' ? "Waiting: budget. This project's monthly budget is used up." : 'Waiting: budget. The monthly budget is used up.',
  startAnyway: 'Start anyway',
  started: 'Starting despite the budget',
  runCost: (cost: string) => `cost ${cost}`,
  budgetEvent: (phase: string, scope: string | undefined, project: string | undefined, spent?: number, limit?: number, via?: string) => {
    const amounts = spent !== undefined && limit !== undefined ? ` (${usd(spent)} of ${usd(limit)})` : ''
    switch (phase) {
      case 'warning':
        return `${scopeName(scope, project)} is at 80%${amounts}`
      case 'reached':
        return `${scopeName(scope, project)} is used up${amounts}; new runs wait`
      case 'waiting':
        return `Waiting: budget. ${scopeName(scope, project)} is used up${amounts}`
      case 'override':
        return `Started despite the budget${via ? ` (${via})` : ''}`
    }
    return 'Budget'
  },
}

function compact(n: number): string {
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (n >= 1e3) return `${(n / 1e3).toFixed(0)}k`
  return String(n)
}
