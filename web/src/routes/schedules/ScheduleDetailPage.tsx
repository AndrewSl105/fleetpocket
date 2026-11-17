import { Link, useParams } from 'react-router'
import { Copy, Pause, Pencil, Play, Rocket, Trash2 } from 'lucide-react'
import { useSchedule, useScheduleFirings } from '@/api/schedules'
import { errorMessage } from '@/api/hooks'
import type { Schedule, ScheduleFiring } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { PageSpinner } from '@/components/PageSpinner'
import { dateTime, usd } from '@/lib/format'
import { describeCron, sc } from '@/i18n/schedules'
import { lp } from '@/i18n/loops'
import { nextRun } from './scheduleForm'
import { FiringStamp, useScheduleActions } from './scheduleParts'

/** One schedule: what it runs and when, its actions and its history. */
export default function ScheduleDetailPage() {
  const id = Number(useParams().id)
  const s = useSchedule(Number.isFinite(id) ? id : undefined)
  if (s.isPending) return <PageSpinner />
  if (s.isError) return <p className="py-10 text-center text-sm text-muted-foreground">{errorMessage(s.error) || sc.notFound}</p>
  return <Detail s={s.data} />
}

function Detail({ s }: { s: Schedule }) {
  const a = useScheduleActions(s)
  const history = useScheduleFirings(s.id)
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl space-y-5 px-4 pt-4 pb-6">
        <div className="flex items-center gap-2">
          <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center truncate px-2 text-base">{s.name}</h1>
          {!s.enabled && (
            <span className="fm-stamp" data-tone="paused">
              {sc.paused}
            </span>
          )}
        </div>

        <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Button className="h-11" disabled={a.busy} onClick={a.runNow}>
            <Rocket /> {sc.runNow}
          </Button>
          <Button variant="outline" className="h-11" disabled={a.busy} onClick={a.toggle}>
            {s.enabled ? <Pause /> : <Play />} {s.enabled ? sc.pause : sc.resume}
          </Button>
          <Button variant="outline" className="h-11" disabled={a.busy} onClick={a.duplicate}>
            <Copy /> {sc.duplicate}
          </Button>
          <Button asChild variant="outline" className="h-11">
            <Link to={`/schedules/${s.id}/edit`}>
              <Pencil /> {sc.edit}
            </Link>
          </Button>
        </div>

        <dl className="divide-y fm-field text-sm">
          <Row label={sc.next}>
            <span className="font-mono">{nextRun(s)}</span>
          </Row>
          <Row label={sc.when}>
            {describeCron(s.cron)} · {s.timezone}
            <span className="block font-mono text-xs text-muted-foreground">{s.cron}</span>
          </Row>
          <Row label={sc.project}>
            <span className="font-mono">{s.project_id}</span>
          </Row>
          <Row label={sc.budgets}>
            {sc.perRun(s.run_budget_usd)} · {sc.perMonth(s.monthly_budget_usd)}
            <span className="block font-mono text-xs text-muted-foreground">{sc.monthSpend(s.month_spend_usd, s.monthly_budget_usd)}</span>
          </Row>
          <Row label={sc.overlap}>
            {sc.concurrencyLabel(s.concurrency)}
            <span className="block text-xs text-muted-foreground">
              {sc.prOnlyIfChanges(s.pr_only_if_changes)}
              {s.ends_at ? ` · ${sc.endsAt(dateTime(s.ends_at))}` : ''}
            </span>
          </Row>
          {s.loop && (
            <Row label={lp.short}>
              <span className="font-mono text-xs">{s.loop.check_command}</span>
              <span className="block text-xs text-muted-foreground">
                {lp.maxIterations}: {s.loop.max_iterations}
                {s.loop.budget_usd ? ` · ${usd(s.loop.budget_usd)}` : ''}
              </span>
            </Row>
          )}
          <Row label={sc.prompt}>
            <span className="whitespace-pre-wrap">{s.prompt}</span>
          </Row>
        </dl>

        <section aria-labelledby="history-title">
          <h2 id="history-title" className="mb-2 text-sm font-bold">
            {sc.history}
          </h2>
          {history.isPending ? (
            <Skeleton className="h-24" />
          ) : !history.data?.length ? (
            <p className="text-sm text-muted-foreground">{sc.noHistory}</p>
          ) : (
            <ul className="divide-y fm-field" aria-label={sc.history}>
              {history.data.map((f) => (
                <FiringRow key={f.id} f={f} />
              ))}
            </ul>
          )}
        </section>

        <Button variant="ghost" className="h-11 text-destructive" disabled={a.busy} onClick={() => void a.remove()}>
          <Trash2 /> {sc.delete}
        </Button>
      </div>
    </div>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[6rem_1fr] gap-3 px-3.5 py-2.5">
      <dt className="fm-label pt-0.5 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words">{children}</dd>
    </div>
  )
}

function FiringRow({ f }: { f: ScheduleFiring }) {
  const body = (
    <>
      <div className="flex flex-wrap items-center gap-2">
        <FiringStamp f={f} />
        {f.loop_state && f.loop_state !== 'running' && (
          <span className="fm-stamp" data-tone={f.loop_state === 'passed' ? 'olive' : 'danger'}>
            {lp.stateStamp(f.loop_state)}
          </span>
        )}
        <span className="font-mono text-xs text-muted-foreground">{dateTime(f.scheduled_for)}</span>
        <span className="ml-auto fm-label text-muted-foreground">{sc.trigger(f.trigger)}</span>
      </div>
      <div className="mt-1 flex flex-wrap gap-x-3 text-xs text-muted-foreground">
        {f.task_id != null && <span className="font-mono text-foreground">{sc.task(f.task_id)}</span>}
        {f.task_cost_usd != null && <span className="font-mono">{usd(f.task_cost_usd)}</span>}
        {f.reason && <span>{f.reason}</span>}
      </div>
    </>
  )
  return (
    <li data-testid="schedule-firing">
      {f.task_id != null ? (
        <Link to={`/tasks/${f.task_id}`} className="block min-h-11 px-3.5 py-2.5 hover:bg-accent/60">
          {body}
        </Link>
      ) : (
        <div className="px-3.5 py-2.5">{body}</div>
      )}
    </li>
  )
}
