import type { TaskView } from '@/api/types'
import type { Tone } from '@/lib/status'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'

const af = t.autofix

type CIFields = Pick<TaskView, 'ci_status' | 'autofix_state' | 'autofix_attempts' | 'autofix_max_attempts'>

/** The task's CI result with the CI auto-fix loop: `CI ✓`, `CI ✕ · fixing 1/2`, `CI ✕ · needs you`. */
export function ciBadge(task: CIFields): { text: string; tone: Tone } | null {
  const ci = task.ci_status
  const state = task.autofix_state
  if (!ci && !state) return null
  const head = af.ci[ci ?? 'pending'] ?? af.ci.pending
  if (state === 'needs_you' || state === 'held') return { text: `${af.ci.failure} · ${af.needsYou}`, tone: 'ask' }
  if (state === 'fixing' || state === 'approved' || (state === 'waiting' && ci !== 'success')) {
    const n = Math.max(task.autofix_attempts, 1)
    return { text: `${state === 'waiting' ? head : af.ci.failure} · ${af.fixing(n, task.autofix_max_attempts ?? n)}`, tone: 'signal' }
  }
  if (!ci) return null
  return { text: head, tone: ci === 'success' ? 'olive' : ci === 'failure' ? 'danger' : 'plain' }
}

export function CIBadge({ task, className }: { task: TaskView; className?: string }) {
  const b = ciBadge(task)
  if (!b) return null
  return (
    <span data-tone={b.tone} data-testid="ci-badge" className={cn('fm-stamp w-fit shrink-0', className)}>
      {b.text}
    </span>
  )
}
