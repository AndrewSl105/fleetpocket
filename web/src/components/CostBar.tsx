import { cn } from '@/lib/utils'

/**
 * A thin ruled bar: value against max. With a budget (tone "budget") it turns
 * signal at 80% and danger at 100%; the label says the numbers, the colour
 * only repeats them.
 */
export function CostBar({ value, max, tone = 'plain', className, label }: { value: number; max: number; tone?: 'plain' | 'budget'; className?: string; label?: string }) {
  const ratio = max > 0 ? value / max : 0
  const pct = Math.max(0, Math.min(100, ratio * 100))
  const fill = tone === 'budget' && ratio >= 1 ? 'var(--danger)' : tone === 'budget' && ratio >= 0.8 ? 'var(--signal)' : 'var(--ink)'
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={Math.round(value * 100) / 100}
      className={cn('h-1.5 w-full border border-[var(--rule)] bg-[var(--paper)]', className)}
    >
      <div className="h-full" style={{ width: `${pct}%`, background: fill }} />
    </div>
  )
}
