import { Cost } from '@/components/Cost'
import { Link } from 'react-router'
import type { Task } from '@/api/types'
import { StatusBadge } from '@/components/StatusBadge'
import { HostChip } from '@/components/HostChip'
import { UserBadge } from '@/components/UserBadge'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'

/** One-line activity: the backend's latest tool_use or status change; CI for open PRs. */
function activityLine(task: Task): string | null {
  if (task.status === 'pr_open' && task.ci_status) return `CI ${task.ci_status}`
  if (task.status === 'branch_ready') return null
  return task.last_activity ?? task.status_reason ?? task.branch ?? null
}

/** Zero-padded logbook number, e.g. 142 → "0142". */
export function taskNumber(id: number): string {
  return String(id).padStart(4, '0')
}

export function TaskCard({ task, projectName, selected }: { task: Task; projectName?: string; selected?: boolean }) {
  const waiting = task.status === 'waiting_input'
  const line = activityLine(task)
  return (
    <Link
      to={`/tasks/${task.id}`}
      aria-current={selected ? 'page' : undefined}
      className={cn(
        'flex gap-3 py-3 pr-1 pl-2 hover:bg-grid',
        waiting && 'fm-ask my-1 px-2',
        selected && 'bg-grid shadow-[inset_3px_0_0_var(--amber)]',
      )}
    >
      <span className="w-11 shrink-0 pt-0.5 font-mono text-[13px] font-semibold text-muted-foreground tabular-nums">{taskNumber(task.id)}</span>
      <div className="min-w-0 flex-1">
      <h2 className="line-clamp-2 min-w-0 text-[15px] leading-snug font-semibold break-words">{task.title}</h2>
      {task.status === 'branch_ready' && task.local_branch ? (
        <div className="mt-1.5 flex min-w-0 items-center gap-2">
          <StatusBadge status={task.status} />
          <span className="min-w-0 truncate font-mono text-xs text-olive-text">{task.local_branch}</span>
        </div>
      ) : (
        <StatusBadge status={task.status} className="mt-1.5" />
      )}
      {line && <p className="mt-1.5 truncate font-mono text-xs text-muted-foreground">{line}</p>}
      <div className="mt-1 flex items-center gap-2 font-mono text-xs text-muted-foreground">
        <UserBadge user={task.created_by} className="shrink-0" />
        <span className="truncate">{projectName ?? task.project_id}</span>
        {task.node && (
          <>
            <span aria-hidden="true">·</span>
            <HostChip node={task.node} className="shrink" />
          </>
        )}
        <span aria-hidden="true">·</span>
        <span className="shrink-0">{relativeTime(task.last_activity_at ?? task.updated_at)}</span>
        <Cost className="ml-auto shrink-0 tabular-nums" value={task.cost_usd_total} estimated={task.cost_estimated} unknown={task.cost_unknown} />
      </div>
      </div>
      {waiting && <span className="sr-only">{t.tasks.needsAnswer}</span>}
    </Link>
  )
}
