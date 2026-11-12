import type { TaskStatus } from '@/api/types'
import { STATUS, TONE_CLASSES } from '@/lib/status'
import { cn } from '@/lib/utils'

export function StatusBadge({ status, className }: { status: TaskStatus; className?: string }) {
  const meta = STATUS[status]
  const Icon = meta.icon
  return (
    <span
      className={cn(
        'inline-flex shrink-0 items-center gap-1 border px-1.5 py-0.5 text-xs whitespace-nowrap',
        TONE_CLASSES[meta.tone],
        className,
      )}
    >
      <Icon className={cn('size-3.5', meta.spin && 'animate-spin [animation-duration:2.5s]')} aria-hidden="true" />
      {meta.label}
    </span>
  )
}
