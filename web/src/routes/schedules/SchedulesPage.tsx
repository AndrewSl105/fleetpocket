import { Link, useNavigate } from 'react-router'
import { Copy, MoreVertical, Pause, Pencil, Play, Plus, Rocket, Trash2 } from 'lucide-react'
import { useSchedules } from '@/api/schedules'
import { errorMessage } from '@/api/hooks'
import type { Schedule } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { describeCron, sc } from '@/i18n/schedules'
import { nextRun } from './scheduleForm'
import { FiringStamp, useScheduleActions } from './scheduleParts'

/** Schedules: a ledger of recurring tasks (next run, last result, month spend) with Run now, Pause, Duplicate. */
export default function SchedulesPage() {
  const list = useSchedules()
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl px-4 pt-4 pb-6">
        <div className="mb-3 flex items-center gap-2">
          <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">{sc.title}</h1>
          <Button asChild className="h-10">
            <Link to="/schedules/new">
              <Plus /> {sc.newSchedule}
            </Link>
          </Button>
        </div>
        <p className="mb-4 text-xs text-muted-foreground">{sc.intro}</p>
        {list.isPending ? (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        ) : list.isError ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{errorMessage(list.error) || sc.loadError}</p>
        ) : !list.data.length ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{sc.empty}</p>
        ) : (
          <ul className="divide-y fm-field" aria-label={sc.title}>
            {list.data.map((s) => (
              <ScheduleRow key={s.id} s={s} />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function ScheduleRow({ s }: { s: Schedule }) {
  const navigate = useNavigate()
  const a = useScheduleActions(s)
  return (
    <li className="flex items-start gap-1" data-testid="schedule-row">
      <Link to={`/schedules/${s.id}`} className="block min-h-11 min-w-0 flex-1 px-3.5 py-3 hover:bg-accent/60">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          <span className="min-w-0 truncate font-medium">{s.name}</span>
          <FiringStamp f={s.last_firing} />
          {s.loop && (
            <span className="fm-stamp" data-tone="ask">
              {`Loop ×${s.loop.max_iterations}`}
            </span>
          )}
        </div>
        <div className="mt-1 grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-xs">
          <span className="fm-label text-muted-foreground">{sc.next}</span>
          <span className={s.enabled ? 'font-mono' : 'font-mono text-muted-foreground'}>{nextRun(s)}</span>
          <span className="fm-label text-muted-foreground">{sc.when}</span>
          <span className="min-w-0 truncate text-muted-foreground">
            {describeCron(s.cron)} · {s.timezone} · <span className="font-mono">{s.project_id}</span>
          </span>
        </div>
        <div className="mt-1 font-mono text-xs text-muted-foreground">{sc.monthSpend(s.month_spend_usd, s.monthly_budget_usd)}</div>
      </Link>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="mt-2 mr-1 size-11 shrink-0" aria-label={sc.actions(s.name)}>
            <MoreVertical />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-44">
          <DropdownMenuItem className="h-10" disabled={a.busy} onSelect={a.runNow}>
            <Rocket /> {sc.runNow}
          </DropdownMenuItem>
          <DropdownMenuItem className="h-10" disabled={a.busy} onSelect={a.toggle}>
            {s.enabled ? <Pause /> : <Play />} {s.enabled ? sc.pause : sc.resume}
          </DropdownMenuItem>
          <DropdownMenuItem className="h-10" disabled={a.busy} onSelect={a.duplicate}>
            <Copy /> {sc.duplicate}
          </DropdownMenuItem>
          <DropdownMenuItem className="h-10" onSelect={() => void navigate(`/schedules/${s.id}/edit`)}>
            <Pencil /> {sc.edit}
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem className="h-10 text-destructive" disabled={a.busy} onSelect={() => void a.remove()}>
            <Trash2 /> {sc.delete}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </li>
  )
}
