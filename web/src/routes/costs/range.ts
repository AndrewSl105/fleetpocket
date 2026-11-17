import type { CostPeriod } from '@/api/costs'

export type Range = 'month' | 'last30' | 'last90'

export const RANGES: Range[] = ['month', 'last30', 'last90']

export function ymd(d: Date): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`
}

/** The range's dates (local time) and the bucket size of its bar list. */
export function rangeDates(range: Range, today = new Date()): { from: string; to: string; period: CostPeriod } {
  const to = ymd(today)
  if (range === 'month') return { from: ymd(new Date(today.getFullYear(), today.getMonth(), 1)), to, period: 'day' }
  const days = range === 'last30' ? 29 : 89
  const from = new Date(today.getFullYear(), today.getMonth(), today.getDate() - days)
  return { from: ymd(from), to, period: range === 'last30' ? 'day' : 'week' }
}
