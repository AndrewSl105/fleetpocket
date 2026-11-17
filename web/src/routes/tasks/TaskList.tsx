import { lazy, Suspense, useEffect, useMemo, useRef, useState } from 'react'
import { CaptureTrigger } from '@/components/CaptureTrigger'
import { useNavigate, useSearchParams } from 'react-router'
import { Plus, Search, X } from 'lucide-react'
import { Link } from 'react-router'
import { Logo } from '@/components/Logo'
import { useMe, useProjects, useTasks } from '@/api/hooks'
import type { Task } from '@/api/types'
import { useTasksSubscription } from '@/realtime/RealtimeProvider'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import type { TaskGroup } from '@/lib/status'
import { haptic } from '@/telegram/haptics'
import { t } from '@/i18n/en'
import { TaskCard } from './TaskCard'
import { usePeopleScope } from '@/lib/people'

// Team mode only: kept out of the initial bundle.
const PeopleFilter = lazy(() => import('@/components/PeopleFilter').then((m) => ({ default: m.PeopleFilter })))
import { UserBadge } from '@/components/UserBadge'
import { usePullToRefresh } from './usePullToRefresh'

const GROUP_ORDER: TaskGroup[] = ['active', 'pr_open', 'done', 'failed']

/** waiting_input first ("Needs answer"), otherwise newest first as returned. */
export function sortTasks(tasks: Task[]): Task[] {
  const waiting = tasks.filter((t) => t.status === 'waiting_input')
  return [...waiting, ...tasks.filter((t) => t.status !== 'waiting_input')]
}

export function TaskList({ selectedId }: { selectedId?: number }) {
  const [params, setParams] = useSearchParams()
  const group = (GROUP_ORDER.includes(params.get('filter') as TaskGroup) ? params.get('filter') : 'active') as TaskGroup
  const project = params.get('project') ?? undefined
  const [query, setQuery] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const navigate = useNavigate()

  useTasksSubscription()
  const people = usePeopleScope()
  const me = useMe().data?.user
  const tasks = useTasks({ group, project, createdBy: people.createdBy })
  const projects = useProjects()
  const scrollRef = useRef<HTMLDivElement>(null)
  const pull = usePullToRefresh(scrollRef, () => tasks.refetch())

  // "/" focuses search on desktop.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const target = e.target as HTMLElement
      if (e.key === '/' && !['INPUT', 'TEXTAREA'].includes(target.tagName) && !target.isContentEditable) {
        e.preventDefault()
        searchRef.current?.focus()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const projectNames = useMemo(() => new Map(projects.data?.map((p) => [p.id, p.name])), [projects.data])
  const items = useMemo(() => {
    const all = tasks.data?.pages.flatMap((p) => p.items) ?? []
    const q = query.trim().toLowerCase()
    const filtered = q ? all.filter((t) => t.title.toLowerCase().includes(q) || t.branch?.toLowerCase().includes(q)) : all
    return sortTasks(filtered)
  }, [tasks.data, query])

  function setFilter(next: { filter?: TaskGroup; project?: string | null }) {
    haptic.select()
    const p = new URLSearchParams(params)
    if (next.filter) {
      if (next.filter === 'active') p.delete('filter')
      else p.set('filter', next.filter)
    }
    if (next.project !== undefined) {
      if (next.project) p.set('project', next.project)
      else p.delete('project')
    }
    setParams(p, { replace: true })
  }

  return (
    <div className="flex h-full flex-col">
      <header className="shrink-0 space-y-3 px-4 pt-4 pb-3">
        <div className="flex items-center justify-between gap-3 border-b-2 border-ink pb-3">
          <Logo className="size-10 lg:hidden" />
          <div className="min-w-0 flex-1">
            <p className="fm-label truncate text-[10px] text-muted-foreground">{t.app.kicker}</p>
            <h1 className="truncate text-[26px] leading-tight font-bold">{t.tasks.title}</h1>
          </div>
          {people.team && me && (
            <Link to="/settings#profile" className="lg:hidden" aria-label={t.team.signedInAs(me.name)}>
              <UserBadge user={{ id: me.id, name: me.name, initials: me.initials }} />
            </Link>
          )}
          <CaptureTrigger className="lg:hidden" />
          {me?.role !== 'viewer' && (
            <Link to="/new" className="fm-btn-signal flex h-11 shrink-0 items-center gap-1.5 px-3.5 text-[15px] lg:hidden">
              <Plus className="size-4" strokeWidth={2} aria-hidden="true" />
              {t.nav.new}
            </Link>
          )}
          <div className="relative hidden w-44 lg:block">
            <Search className="pointer-events-none absolute top-2.5 left-2.5 size-4 text-muted-foreground" aria-hidden="true" />
            <Input
              ref={searchRef}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Search  /"
              aria-label="Search tasks"
              className="h-9 pl-8"
            />
          </div>
        </div>
        <div role="tablist" aria-label="Filter" className="grid grid-cols-4 border-[1.5px] border-ink">
          {GROUP_ORDER.map((g) => (
            <button
              key={g}
              role="tab"
              aria-selected={group === g}
              onClick={() => setFilter({ filter: g })}
              className={cn(
                'fm-label h-11 border-r border-rule text-[11px] last:border-r-0',
                group === g ? 'fm-selected font-semibold' : 'text-foreground hover:bg-grid',
              )}
            >
              {t.tasks.filters[g]}
            </button>
          ))}
        </div>
        {people.team && (
          <Suspense fallback={null}>
            <PeopleFilter people={people} />
          </Suspense>
        )}
        {(projects.data?.length ?? 0) > 1 && (
          <div className="-mx-4 flex gap-2 overflow-x-auto px-4 pb-0.5 [scrollbar-width:none]">
            {project ? (
              <button
                onClick={() => setFilter({ project: null })}
                className="fm-pressed inline-flex h-9 shrink-0 items-center gap-1 px-3 font-mono text-xs font-semibold"
              >
                {projectNames.get(project) ?? project}
                <X className="size-3.5" aria-label="Clear project filter" />
              </button>
            ) : (
              projects.data?.map((p) => (
                <button
                  key={p.id}
                  onClick={() => setFilter({ project: p.id })}
                  className="fm-btn inline-flex h-9 shrink-0 items-center px-3 font-mono text-xs"
                >
                  {p.name}
                </button>
              ))
            )}
          </div>
        )}
      </header>

      <div ref={scrollRef} className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 pb-4" {...pull.handlers}>
        {pull.distance > 0 && (
          <div className="flex justify-center text-xs text-muted-foreground" style={{ height: pull.distance }}>
            <span className="self-end pb-2">{pull.ready ? t.tasks.pullToRefresh : ''}</span>
          </div>
        )}
        {tasks.isPending ? (
          <div className="space-y-2">
            {Array.from({ length: 5 }, (_, i) => (
              <Skeleton key={i} className="h-[76px] border-b border-rule" />
            ))}
          </div>
        ) : tasks.isError ? (
          <div className="py-10 text-center text-sm text-muted-foreground">
            <p>{t.common.somethingWrong}</p>
            <Button variant="outline" className="mt-3 h-11" onClick={() => tasks.refetch()}>
              {t.common.retry}
            </Button>
          </div>
        ) : items.length === 0 ? (
          <div className="fm-ground flex flex-col items-center gap-3 border border-rule py-16 text-center">
            <Logo className="size-16" />
            <p className="font-medium">{t.tasks.emptyTitle}</p>
            <p className="max-w-xs text-sm text-muted-foreground">{t.tasks.emptyBody}</p>
            <Button className="mt-1 h-11" onClick={() => navigate('/new')}>
              {t.tasks.createFirst}
            </Button>
          </div>
        ) : (
          <ul className="border-t border-rule">
            {items.map((task) => (
              <li key={task.id} className="animate-in fade-in-0 border-b border-rule duration-150">
                <TaskCard task={task} projectName={projectNames.get(task.project_id)} selected={task.id === selectedId} />
              </li>
            ))}
            {tasks.hasNextPage && (
              <li>
                <Button
                  variant="ghost"
                  className="h-11 w-full"
                  disabled={tasks.isFetchingNextPage}
                  onClick={() => tasks.fetchNextPage()}
                >
                  {tasks.isFetchingNextPage ? t.common.loading : t.tasks.loadMore}
                </Button>
              </li>
            )}
          </ul>
        )}
      </div>
    </div>
  )
}
