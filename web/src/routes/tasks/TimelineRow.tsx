import { memo, useState } from 'react'
import { toast } from 'sonner'
import {
  Ban,
  ChevronRight,
  CircleCheck,
  CircleX,
  ExternalLink,
  GitCommitHorizontal,
  GitMerge,
  GitPullRequest,
  GitPullRequestClosed,
  OctagonAlert,
  Plug,
  Sparkles,
  Wrench,
} from 'lucide-react'
import type { TimelineItem } from '@/lib/timeline'
import { STATUS } from '@/lib/status'
import { cn } from '@/lib/utils'
import { Markdown } from '@/components/Markdown'
import { AttachmentGrid } from '@/components/attachments/AttachmentGrid'
import { CopyButton } from '@/components/CopyButton'
import { Button } from '@/components/ui/button'
import { useAllowEgress, useTask } from '@/api/hooks'
import { t } from '@/i18n/en'
import { c } from '@/i18n/costs'
import { usd } from '@/lib/format'
import { cp } from '@/i18n/checkpoints'
import { CheckpointRow } from './CheckpointRow'
import { lp } from '@/i18n/loops'

const tl = t.task.timeline

export function dividerText(text: string): string {
  if (text.startsWith('run_started:')) return tl.runStarted(text.slice('run_started:'.length))
  if (text.startsWith('run_finished:')) {
    const [code, cost, source] = text.slice('run_finished:'.length).split('|')
    const done = tl.runFinished(code === '' ? null : Number(code))
    if (cost === undefined) return done
    return `${done} · ${c.runCost(usd(Number(cost), source === 'estimated', source === 'unknown'))}`
  }
  if (text.startsWith('budget:')) {
    try {
      const b = JSON.parse(text.slice('budget:'.length)) as { phase: string; scope?: string; project_id?: string; spent_usd?: number; limit_usd?: number; via?: string }
      return c.budgetEvent(b.phase, b.scope, b.project_id, b.spent_usd, b.limit_usd, b.via)
    } catch {
      return text
    }
  }
  if (text.startsWith('integrations:')) {
    const [servers, egress] = text.slice('integrations:'.length).split('|')
    return tl.integrations(servers, egress)
  }
  if (text === 'interrupt') return tl.interrupt
  if (text === 'paused') return tl.paused
  return text
}

function noteText(note: NonNullable<Extract<TimelineItem, { kind: 'user' }>['note']>): string {
  if (note === 'queued') return tl.queued
  if (note === 'injected') return tl.injected
  return tl.dropped(note.dropped)
}

function Divider({ children, warn }: { children: React.ReactNode; warn?: boolean }) {
  return (
    <div className={cn('flex items-center gap-3 py-1 text-[11px] text-muted-foreground', warn && 'text-signal-text')}>
      <span className="h-px flex-1 bg-border" />
      <span className="max-w-[75%] text-center">{children}</span>
      <span className="h-px flex-1 bg-border" />
    </div>
  )
}

function ToolRow({ item }: { item: Extract<TimelineItem, { kind: 'tool' }> }) {
  const [open, setOpen] = useState(false)
  const [name, ...rest] = item.summary.split(' ')
  const target = item.summary.startsWith(item.tool) ? rest.join(' ') : item.summary
  const label = item.summary.startsWith(item.tool) ? name : item.tool
  const failed = item.ok === false
  return (
    <div>
      <button
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className={cn(
          'flex min-h-9 w-full items-center gap-2 rounded-md px-2 text-left text-xs text-muted-foreground hover:bg-muted',
          failed && 'text-danger',
        )}
      >
        <ChevronRight className={cn('size-3.5 shrink-0 transition-transform', open && 'rotate-90')} aria-hidden="true" />
        <span className="shrink-0 font-medium text-foreground/80">{label}</span>
        {target && <span className="truncate font-mono">· {target}</span>}
        {failed && <CircleX className="ml-auto size-3.5 shrink-0" aria-label="failed" />}
      </button>
      {open && (
        <div className="ml-7 rounded-md bg-muted px-2.5 py-1.5 font-mono text-xs break-all text-muted-foreground">
          {item.summary}
          {item.error && <div className="mt-1 text-danger">{item.error}</div>}
        </div>
      )}
    </div>
  )
}

/** A gateway call: like a tool row, with the server and the time it took. */
function MCPRow({ item }: { item: Extract<TimelineItem, { kind: 'mcp' }> }) {
  const failed = !item.ok
  const label = item.tool ? tl.mcpCall(item.server, item.tool) : tl.mcpConnect(item.server)
  const note = item.denied === 'denied' ? tl.mcpDenied : item.denied === 'limit' ? tl.mcpLimit : item.error
  return (
    <div className={cn('flex min-h-9 items-center gap-2 px-2 text-xs text-muted-foreground', failed && 'text-danger')}>
      <Plug className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="shrink-0 fm-label">mcp</span>
      <span className="truncate font-mono">{label}</span>
      {note && <span className="truncate">· {note}</span>}
      {item.ok && item.ms > 0 && <span className="ml-auto shrink-0 font-mono">{item.ms} ms</span>}
      {failed && <CircleX className="ml-auto size-3.5 shrink-0" aria-label="failed" />}
    </div>
  )
}

/** A host the egress proxy refused, with "Allow for this project" (next run). */
function EgressRow({ item, taskId }: { item: Extract<TimelineItem, { kind: 'egress' }>; taskId: number }) {
  const task = useTask(taskId)
  const allow = useAllowEgress(task.data?.project_id ?? '')
  const [done, setDone] = useState(false)
  return (
    <div className="flex flex-wrap items-center gap-2 fm-ask px-3 py-2 text-sm">
      <Ban className="size-4 shrink-0 text-danger" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <div className="font-mono text-xs font-semibold break-all">{tl.blocked(item.host)}</div>
        <div className="text-xs text-muted-foreground">{done ? tl.allowed(item.host) : tl.blockedHint}</div>
      </div>
      {!done && (
        <Button
          variant="outline"
          className="h-11"
          disabled={!task.data || allow.isPending}
          onClick={() => allow.mutate(item.host, { onSuccess: () => { setDone(true); toast.success(tl.allowed(item.host)) } })}
        >
          {tl.allow}
        </Button>
      )}
    </div>
  )
}

const af = t.autofix

function CheckLinks({ checks }: { checks: Extract<TimelineItem, { kind: 'autofix' }>['checks'] }) {
  if (!checks.length) return null
  return (
    <ul className="mt-1.5 space-y-0.5">
      {checks.map((c) => (
        <li key={c.name}>
          <a
            href={c.url}
            target="_blank"
            rel="noreferrer"
            className="inline-flex min-h-8 items-center gap-1.5 font-mono text-xs text-danger underline-offset-2 hover:underline"
          >
            <CircleX className="size-3.5 shrink-0" aria-hidden="true" />
            {c.name}
            <ExternalLink className="size-3 shrink-0 text-muted-foreground" aria-hidden="true" />
          </a>
        </li>
      ))}
    </ul>
  )
}

/** A CI auto-fix step: the attempt section, its stops and the green stamp. */
function AutoFixRow({ item, taskId }: { item: Extract<TimelineItem, { kind: 'autofix' }>; taskId: number }) {
  switch (item.phase) {
    case 'started':
      return (
        <section aria-label={af.attempt(item.attempt, item.maxAttempts)} className="fm-card px-3.5 py-3">
          <div className="fm-label flex items-center gap-1.5 font-semibold">
            <Wrench className="size-3.5" aria-hidden="true" />
            {af.attempt(item.attempt, item.maxAttempts)}
          </div>
          <div className="mt-2 text-xs text-muted-foreground">{af.failingChecks}</div>
          <CheckLinks checks={item.checks} />
          {item.signature && (
            <div className="mt-1.5 text-xs text-muted-foreground">
              {af.signature} <span className="font-mono text-foreground">{item.signature}</span>
            </div>
          )}
          {item.attachments.length > 0 && (
            <>
              <div className="mt-2 mb-1 text-xs text-muted-foreground">{af.logs}</div>
              <AttachmentGrid taskId={taskId} items={item.attachments} />
            </>
          )}
        </section>
      )
    case 'green':
      return (
        <div className="fm-group bg-grid px-3 py-2.5 text-sm font-semibold text-olive-text" role="status">
          {af.green(item.fixes)}
        </div>
      )
    case 'needs_you':
    case 'held':
      return (
        <div className="fm-ask px-3.5 py-3 text-sm">
          <div className="fm-label font-semibold text-signal-text">{item.phase === 'held' ? af.held : af.stopped}</div>
          {item.reason && <p className="mt-1 break-words">{item.reason}</p>}
          {item.reasons.length > 0 && (
            <ul className="mt-1 list-disc pl-5 font-mono text-xs break-words">
              {item.reasons.map((r) => (
                <li key={r}>{r}</li>
              ))}
            </ul>
          )}
          <CheckLinks checks={item.checks} />
        </div>
      )
    case 'pushed':
      return <Divider>{af.pushed(item.attempt)}</Divider>
    case 'discarded':
      return <Divider warn>{af.discarded}</Divider>
    case 'cancelled':
      return <Divider warn>{af.cancelled(item.reason ?? '')}</Divider>
  }
  return null
}

function TimelineRowImpl({ item, taskId }: { item: TimelineItem; taskId: number }) {
  switch (item.kind) {
    case 'claude':
      return (
        <div className="fm-field max-w-[92%] px-3.5 py-2.5">
          <Markdown>{item.text}</Markdown>
        </div>
      )
    case 'user':
      return (
        <div className="flex justify-end">
          <div
            className={cn(
              'fm-card max-w-[85%] bg-accent px-3.5 py-2.5 text-[15px] whitespace-pre-wrap text-foreground',
              item.pending && 'opacity-70',
            )}
          >
            {item.text}
            {item.attachments && <AttachmentGrid taskId={taskId} items={item.attachments} className="mt-2" />}
            {item.pending ? (
              <div className="mt-1 text-[11px] opacity-80">{tl.sending}</div>
            ) : (
              item.note && <div className="mt-1 text-[11px] opacity-80">{noteText(item.note)}</div>
            )}
          </div>
        </div>
      )
    case 'attachments':
      return (
        <div className="flex flex-col items-end gap-1">
          <AttachmentGrid taskId={taskId} items={item.attachments} />
          <span className="text-[11px] text-muted-foreground">{t.attachments.taskAttachments(item.attachments.length)}</span>
        </div>
      )
    case 'tool':
      return <ToolRow item={item} />
    case 'divider':
      return <Divider warn={item.tone === 'warn'}>{dividerText(item.text)}</Divider>
    case 'status': {
      const label = STATUS[item.status]?.label ?? item.status
      return (
        <Divider>
          {tl.status(label)}
          {item.reason ? ` · ${item.reason}` : ''}
        </Divider>
      )
    }
    case 'commit':
      return (
        <div className="flex items-center gap-2.5 fm-group px-3 py-2.5 text-sm">
          <GitCommitHorizontal className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
          <span>{tl.committed(item.files)}</span>
          <span className="font-mono text-xs text-olive-text">+{item.insertions}</span>
          <span className="font-mono text-xs text-danger">−{item.deletions}</span>
          <span className="ml-auto font-mono text-xs text-muted-foreground">{item.sha.slice(0, 7)}</span>
        </div>
      )
    case 'pr': {
      const Icon = item.action === 'closed' ? (item.merged ? GitMerge : GitPullRequestClosed) : GitPullRequest
      const text =
        item.action === 'opened'
          ? tl.prOpened(item.number)
          : item.action === 'updated'
            ? tl.prUpdated(item.number)
            : tl.prClosed(item.number, !!item.merged)
      const body = (
        <>
          <Icon className="size-4 shrink-0 text-olive-text" aria-hidden="true" />
          <span className="font-medium">{text}</span>
          {item.url && <ExternalLink className="ml-auto size-4 text-muted-foreground" aria-hidden="true" />}
        </>
      )
      const cls = 'flex items-center gap-2.5 fm-group bg-grid px-3 py-2.5 text-sm'
      return item.url ? (
        <a href={item.url} target="_blank" rel="noreferrer" className={cn(cls, 'hover:bg-grid')}>
          {body}
        </a>
      ) : (
        <div className={cls}>{body}</div>
      )
    }
    case 'ci': {
      const ok = item.status === 'success'
      const bad = item.status === 'failure'
      const Icon = ok ? CircleCheck : bad ? CircleX : ChevronRight
      return (
        <a
          href={item.url}
          target="_blank"
          rel="noreferrer"
          className={cn(
            'flex min-h-9 items-center gap-2 px-2 text-xs text-muted-foreground',
            ok && 'text-olive-text',
            bad && 'text-danger',
          )}
        >
          <Icon className="size-3.5" aria-hidden="true" />
          {tl.ci(item.status, item.name)}
        </a>
      )
    }
    case 'done':
      return (
        <div className="fm-group bg-grid px-3.5 py-3">
          <div className="mb-1.5 flex items-center gap-2 text-sm font-medium text-olive-text">
            <Sparkles className="size-4" aria-hidden="true" />
            {item.noChanges ? tl.noChanges : tl.done}
          </div>
          {item.summary && <Markdown className="text-sm">{item.summary}</Markdown>}
        </div>
      )
    case 'error':
      return (
        <div role="alert" className="flex gap-2.5 fm-group bg-grid px-3 py-2.5 text-sm text-danger">
          <OctagonAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
          <div className="min-w-0 flex-1">
            <div className="font-mono text-xs font-medium">{item.title === 'diff_rejected' ? tl.diffRejected : item.title}</div>
            <div className="break-words">{item.message}</div>
          </div>
          <CopyButton
            text={`${item.title}: ${item.message}`}
            label={t.task.copyError}
            className="-mt-1 -mr-1.5"
          />
        </div>
      )
    case 'autofix':
      return <AutoFixRow item={item} taskId={taskId} />
    case 'mcp':
      return <MCPRow item={item} />
    case 'egress':
      return <EgressRow item={item} taskId={taskId} />
    case 'checkpoint':
      return <CheckpointRow item={item} taskId={taskId} />
    case 'restored':
      return (
        <Divider>
          {cp.restored(item.n, item.mode)}
          {item.instruction && <span className="block italic">“{item.instruction}”</span>}
        </Divider>
      )
    case 'force_push':
      return <Divider warn>{cp.forcePushed(item.branch, item.from)}</Divider>
    case 'abandoned':
      return <AbandonedRow item={item} taskId={taskId} />
    case 'iteration':
      return (
        <div className="flex min-h-9 items-center gap-2 border-y-2 border-ink bg-grid px-3 py-1.5" role="separator" aria-label={lp.iteration(item.n, item.max)}>
          <span className="fm-label font-semibold">{lp.iteration(item.n, item.max)}</span>
          {item.command && <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">{item.command}</span>}
        </div>
      )
    case 'check':
      return <CheckRow item={item} />
    case 'loop_end':
      return (
        <Divider warn={item.state !== 'passed'}>
          {lp.ended(item.state)}
          {item.reason && item.state !== 'passed' ? ` · ${item.reason}` : ''}
        </Divider>
      )
    case 'question':
      return null
  }
}

/** The check that ended a loop iteration: a stamp, then its output on demand. */
function CheckRow({ item }: { item: Extract<TimelineItem, { kind: 'check' }> }) {
  const [open, setOpen] = useState(false)
  const passed = item.exitCode === 0 && !item.timedOut
  return (
    <div className="fm-group px-3 py-2.5" data-testid="loop-check">
      <div className="flex flex-wrap items-center gap-2">
        <span className="fm-stamp" data-tone={passed ? 'olive' : 'danger'}>
          {passed ? lp.checkPassed : lp.checkFailed(item.iteration, item.max)}
        </span>
        <span className="min-w-0 truncate font-mono text-xs text-muted-foreground">{item.command}</span>
        <span className="ml-auto font-mono text-xs text-muted-foreground">
          {item.timedOut ? lp.checkTimedOut : lp.exitCode(item.exitCode)} · {(item.ms / 1000).toFixed(1)}s
        </span>
      </div>
      {item.output.trim() && (
        <>
          <button
            type="button"
            onClick={() => setOpen((o) => !o)}
            aria-expanded={open}
            className="mt-1 flex min-h-11 items-center gap-1.5 font-mono text-xs uppercase tracking-wide text-muted-foreground"
          >
            <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} aria-hidden="true" />
            {open ? lp.hideOutput : lp.showOutput}
          </button>
          {open && <pre className="max-h-72 overflow-auto bg-grid p-2 font-mono text-[11px] leading-snug whitespace-pre-wrap break-words">{item.output}</pre>}
        </>
      )}
    </div>
  )
}

/** Steps a restore discarded: collapsed and greyed, never deleted. */
function AbandonedRow({ item, taskId }: { item: Extract<TimelineItem, { kind: 'abandoned' }>; taskId: number }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="border border-dashed border-border opacity-60">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
        className="flex min-h-11 w-full items-center gap-2 px-3 text-left font-mono text-xs uppercase tracking-wide text-muted-foreground"
      >
        <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} aria-hidden="true" />
        <span className="flex-1 line-through decoration-muted-foreground/50">{cp.abandoned(item.items.length)}</span>
        <span>{open ? cp.hideAbandoned : cp.showAbandoned}</span>
      </button>
      {open && (
        <div className="space-y-2 px-2 pb-2 grayscale">
          {item.items.map((child) =>
            child.kind === 'question' ? (
              <p key={child.key} className="px-2 text-sm text-muted-foreground">
                {child.text}
                {child.answer && <span className="block">→ {child.answer.text}</span>}
              </p>
            ) : child.kind === 'checkpoint' ? (
              <p key={child.key} className="px-2 font-mono text-xs uppercase text-muted-foreground">
                {cp.marker(child.n, child.reason, child.files)}
              </p>
            ) : (
              <TimelineRowImpl key={child.key} item={child} taskId={taskId} />
            ),
          )}
        </div>
      )}
    </div>
  )
}

export const TimelineRow = memo(TimelineRowImpl)
