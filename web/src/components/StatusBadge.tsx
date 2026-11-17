import type { TaskStatus } from '@/api/types'
import { STATUS } from '@/lib/status'
import { cn } from '@/lib/utils'

/** A rubber stamp: mono uppercase label in a 1.5 px frame (docs/SPEC-web.md §7). */
export function StatusBadge({ status, className }: { status: TaskStatus; className?: string }) {
  const meta = STATUS[status]
  const Icon = meta.icon
  return (
    <span data-tone={meta.tone} className={cn('fm-stamp w-fit shrink-0', className)}>
      {meta.blink ? (
        <span className="fm-blink" aria-hidden="true">
          ●
        </span>
      ) : meta.glyph ? (
        <span aria-hidden="true">{meta.glyph}</span>
      ) : (
        <Icon className="size-3" strokeWidth={2.5} aria-hidden="true" />
      )}
      {meta.label}
    </span>
  )
}
