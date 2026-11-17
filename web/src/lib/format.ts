import { formatDistanceToNowStrict, format } from 'date-fns'

export function relativeTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return ''
  if (Date.now() - date.getTime() < 45_000) return 'just now'
  return `${formatDistanceToNowStrict(date)} ago`
}

export function dateTime(iso: string | null | undefined): string {
  if (!iso) return '—'
  const date = new Date(iso)
  return Number.isNaN(date.getTime()) ? '—' : format(date, 'd MMM yyyy, HH:mm')
}

/**
 * A cost, one format everywhere: $0.84; ≈$0.84 when part of it is an
 * estimate (Task.cost_estimated); $? when it is unknown (runs with tokens but
 * no price, nothing known); $0.84+? when only part of it is known
 * (Task.cost_unknown). Screen readers get costLabel().
 */
export function usd(value: number | null | undefined, estimated = false, unknown = false): string {
  const v = value ?? 0
  if (unknown && v === 0) return '$?'
  return `${estimated ? '≈' : ''}$${v < 10 ? v.toFixed(2) : v.toFixed(1)}${unknown ? '+?' : ''}`
}

/** What usd() says, in words: "cost unknown", "about $0.84 plus an unknown part". */
export function costLabel(value: number | null | undefined, estimated = false, unknown = false): string {
  const v = value ?? 0
  if (unknown && v === 0) return 'cost unknown'
  const known = `${estimated ? 'about ' : ''}${usd(v)}`
  return unknown ? `${known} plus an unknown part` : known
}

export function duration(fromIso: string | null | undefined, toIso: string | null | undefined): string {
  if (!fromIso) return '—'
  const ms = (toIso ? new Date(toIso).getTime() : Date.now()) - new Date(fromIso).getTime()
  if (!Number.isFinite(ms) || ms < 0) return '—'
  const s = Math.round(ms / 1000)
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m ${s % 60}s`
  return `${Math.floor(m / 60)}h ${m % 60}m`
}
