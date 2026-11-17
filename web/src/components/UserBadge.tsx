import type { UserRef } from '@/api/types'
import { cn } from '@/lib/utils'

/** A person's initials in a small stamp (task rows, canvas, ideas); nothing without one. */
export function UserBadge({ user, withName, className }: { user?: UserRef | null; withName?: boolean; className?: string }) {
  if (!user) return null
  return (
    <span className={cn('inline-flex min-w-0 items-center gap-1 text-xs', className)} title={user.name}>
      <span
        aria-hidden={withName ? 'true' : undefined}
        aria-label={withName ? undefined : user.name}
        role={withName ? undefined : 'img'}
        className="inline-flex size-5 shrink-0 items-center justify-center border border-ink bg-grid font-mono text-[9px] font-bold"
      >
        {user.initials}
      </span>
      {withName && <span className="truncate">{user.name}</span>}
    </span>
  )
}
