import { useMemo } from 'react'
import { Link } from 'react-router'
import { ChevronRight, FolderGit2, Plus } from 'lucide-react'
import { useProjects, useTasks } from '@/api/hooks'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n/en'
import { c } from '@/i18n/costs'
import { CostBar } from '@/components/CostBar'

export default function ProjectsPage() {
  const projects = useProjects()
  // The Project object has no active task count; count the active list client-side.
  const active = useTasks({ group: 'active' })
  const activeByProject = useMemo(() => {
    const m = new Map<string, number>()
    for (const task of active.data?.pages.flatMap((p) => p.items) ?? []) m.set(task.project_id, (m.get(task.project_id) ?? 0) + 1)
    return m
  }, [active.data])

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl px-4 pt-4 pb-6">
        <div className="mb-4 flex items-center justify-between">
          <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">{t.projects.title}</h1>
          <Button asChild variant="outline" className="ml-2 h-10">
            <Link to="/costs">{c.open}</Link>
          </Button>
          <Button asChild variant="outline" className="ml-2 h-10">
            <Link to="/projects/new">
              <Plus /> {t.projects.new}
            </Link>
          </Button>
        </div>
        {projects.isPending ? (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-16 rounded-xl" />
            ))}
          </div>
        ) : !projects.data?.length ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{t.projects.empty}</p>
        ) : (
          <ul className="divide-y fm-field">
            {projects.data.map((p) => {
              const n = activeByProject.get(p.id) ?? 0
              return (
                <li key={p.id}>
                  <Link to={`/projects/${p.id}`} className="flex min-h-16 items-center gap-3 px-4 py-3 hover:bg-accent/60">
                    <FolderGit2 className="size-5 shrink-0 text-muted-foreground" aria-hidden="true" />
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-medium">{p.name}</div>
                      <div className="text-xs text-muted-foreground">
                        <span className="font-mono">{p.id}</span> · {t.projects.repos(p.repositories.length)}
                        {n > 0 && <span className="text-primary"> · {t.projects.active(n)}</span>}
                      </div>
                      <MonthCost spent={p.month_cost_usd} unknown={p.month_cost_unknown} cap={p.budget?.monthly_usd ?? null} />
                    </div>
                    <ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" />
                  </Link>
                </li>
              )
            })}
          </ul>
        )}
      </div>
    </div>
  )
}

/** Month-to-date cost, against the monthly budget when the project has one. */
function MonthCost({ spent, unknown, cap }: { spent: number; unknown: boolean; cap: number | null }) {
  if (!cap)
    return spent > 0 || unknown ? <div className="mt-0.5 font-mono text-xs text-muted-foreground tabular-nums">{c.monthToDate(spent, unknown)}</div> : null
  return (
    <div className="mt-1.5">
      <CostBar value={spent} max={cap} tone="budget" label={c.budget} />
      <div className="mt-0.5 font-mono text-xs text-muted-foreground tabular-nums">
        {c.budget}: {c.ofBudget(spent, cap, unknown)}
      </div>
    </div>
  )
}
