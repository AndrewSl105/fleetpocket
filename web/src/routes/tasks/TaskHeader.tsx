import { useNavigate } from 'react-router'
import { ArrowLeft, Ban, Copy, ExternalLink, GitMerge, GitPullRequest, MoreVertical, OctagonPause, RotateCcw } from 'lucide-react'
import type { TaskView } from '@/api/types'
import { useNodes, useTaskAction } from '@/api/hooks'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { HostChip } from '@/components/HostChip'
import { hostInfo } from '@/lib/hosts'
import { StatusBadge } from '@/components/StatusBadge'
import { CIBadge } from '@/components/CIBadge'
import { useConfirm } from '@/components/confirm'
import { CopyButton, copyText } from '@/components/CopyButton'
import { can } from '@/lib/status'
import { usd } from '@/lib/format'
import { useRuntime } from '@/telegram/runtime'
import { t } from '@/i18n/en'
import { taskNumber } from './TaskCard'
import { useBudgetOverride } from '@/api/costs'
import { c } from '@/i18n/costs'

export function githubUrl(task: TaskView): string | null {
  if (task.pr_url) return task.pr_url
  const repo = task.repository?.full_name
  if (!repo) return null
  return task.branch ? `https://github.com/${repo}/tree/${encodeURIComponent(task.branch)}` : `https://github.com/${repo}`
}

/** A task whose computer is offline: queued tasks wait, live ones resume when it is back. */
function HostWaiting({ task }: { task: TaskView }) {
  const nodes = useNodes()
  if (!task.node || !nodes.data) return null
  const h = hostInfo(task.node, nodes.data)
  if (h.online || ['completed', 'failed', 'cancelled'].includes(task.status)) return null
  return (
    <p className="mt-2 bg-grid px-2.5 py-1.5 text-xs text-signal-text">{t.hosts.waitingFor(h.name)}</p>
  )
}

/** A queued task that a monthly cap holds: say so, with the owner's override. */
function BudgetWaiting({ task }: { task: TaskView }) {
  const override = useBudgetOverride(task.id)
  if (task.status !== 'queued' || !task.budget_wait) return null
  return (
    <div className="mt-2 flex flex-wrap items-center gap-2 border border-dashed border-ink px-2.5 py-1.5 text-xs">
      <p className="min-w-0 flex-1">{c.waitingBudget(task.budget_wait)}</p>
      <button
        type="button"
        onClick={() => override.mutate()}
        disabled={override.isPending}
        className="fm-btn inline-flex h-11 items-center px-3 font-semibold disabled:opacity-60"
      >
        {c.startAnyway}
      </button>
    </div>
  )
}

export function TaskHeader({ task, stopping }: { task: TaskView; stopping: boolean }) {
  const runtime = useRuntime()
  const navigate = useNavigate()
  const confirm = useConfirm()
  const action = useTaskAction(task.id)
  const gh = githubUrl(task)

  async function cancel() {
    if (await confirm(t.task.confirmCancel, t.task.actions.cancel)) action.mutate('cancel')
  }

  const canMerge = can(task.status, 'merge') && task.pr_number != null

  async function merge() {
    if (task.pr_number == null) return
    if (await confirm(t.task.confirmMerge(task.pr_number), t.task.actions.merge)) action.mutate('merge')
  }

  function copyBranch() {
    if (task.branch) void copyText(task.branch)
  }

  return (
    <header className="shrink-0 px-4 pt-3 pb-3">
      <div className="flex items-start gap-2">
        {runtime.mode === 'browser' && (
          <Button
            variant="ghost"
            size="icon"
            className="-ml-2 size-11 shrink-0 lg:hidden"
            onClick={() => navigate(-1)}
            aria-label={t.common.back}
          >
            <ArrowLeft />
          </Button>
        )}
        <div className="min-w-0 flex-1 pt-1.5">
          <p className="fm-label text-muted-foreground">{t.task.number(taskNumber(task.id))}</p>
          <h1 className="text-lg leading-snug font-bold break-words">{task.title}</h1>
        </div>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon" className="-mr-2 size-11 shrink-0" aria-label="Task actions">
              <MoreVertical />
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-48">
            {can(task.status, 'interrupt') && !stopping && (
              <DropdownMenuItem className="h-10" onSelect={() => action.mutate('interrupt')}>
                <OctagonPause /> {t.task.actions.interrupt}
              </DropdownMenuItem>
            )}
            {canMerge && (
              <DropdownMenuItem className="h-10" disabled={action.isPending} onSelect={merge}>
                <GitMerge /> {t.task.actions.mergePr(task.pr_number!)}
              </DropdownMenuItem>
            )}
            {can(task.status, 'retry') && (
              <DropdownMenuItem className="h-10" onSelect={() => action.mutate('retry')}>
                <RotateCcw /> {t.task.actions.retry}
              </DropdownMenuItem>
            )}
            {task.branch && (
              <DropdownMenuItem className="h-10" onSelect={copyBranch}>
                <Copy /> {t.task.actions.copyBranch}
              </DropdownMenuItem>
            )}
            {gh && (
              <DropdownMenuItem className="h-10" asChild>
                <a href={gh} target="_blank" rel="noreferrer">
                  <ExternalLink /> {t.task.actions.openGitHub}
                </a>
              </DropdownMenuItem>
            )}
            {can(task.status, 'cancel') && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuItem className="h-10" variant="destructive" onSelect={cancel}>
                  <Ban /> {t.task.actions.cancel}
                </DropdownMenuItem>
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1.5 text-xs text-muted-foreground">
        <StatusBadge status={task.status} />
        <CIBadge task={task} />
        <span className="truncate">
          {task.project_id}
          {task.repository && <span className="font-mono"> / {task.repository.full_name.split('/').pop()}</span>}
        </span>
        <HostChip node={task.node} />
        <span className="tabular-nums" title={task.cost_api_equivalent ? c.apiEquivalentHint : undefined}>
          {usd(task.cost_usd_total, task.cost_estimated)}
          {task.cost_api_equivalent && <span className="fm-label ml-1">{c.apiEquivalent}</span>}
          {task.autofix_cost_usd > 0 && <span> ({c.autofixShare(task.autofix_cost_usd)})</span>}
        </span>
        {task.turns > 0 && <span className="tabular-nums">{t.task.turns(task.turns)}</span>}
        {task.pr_url && task.pr_number && (
          <a
            href={task.pr_url}
            target="_blank"
            rel="noreferrer"
            className="fm-btn inline-flex h-7 items-center gap-1 px-2.5 text-link underline active:fm-pressed"
          >
            <GitPullRequest className="size-3.5" aria-hidden="true" />
            {t.task.actions.openPr(task.pr_number)}
          </a>
        )}
        {canMerge && (
          <button
            type="button"
            onClick={merge}
            disabled={action.isPending}
            className="fm-btn-primary inline-flex h-7 items-center gap-1 px-2.5 font-semibold disabled:opacity-60"
          >
            <GitMerge className="size-3.5" aria-hidden="true" />
            {t.task.actions.merge}
          </button>
        )}
      </div>
      {task.branch && <p className="mt-1.5 truncate font-mono text-xs text-muted-foreground">{task.branch}</p>}
      <HostWaiting task={task} />
      <BudgetWaiting task={task} />
      {task.status_reason && (task.status === 'failed' || task.status === 'cancelled' || task.status === 'paused') && (
        <div className="mt-2 flex items-start gap-1 bg-grid py-1.5 pr-1 pl-2.5 text-xs text-danger">
          <p className="min-w-0 flex-1 py-1 break-words">{task.status_reason}</p>
          <CopyButton text={task.status_reason} label={t.task.copyError} className="-my-0.5" />
        </div>
      )}
    </header>
  )
}
