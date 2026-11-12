import { lazy, Suspense, useMemo, useState } from 'react'
import { useParams } from 'react-router'
import { useTask, useTaskEvents } from '@/api/hooks'
import { isApiError } from '@/api/errors'
import { useTaskSubscription } from '@/realtime/RealtimeProvider'
import { buildTimeline, isStopping } from '@/lib/timeline'
import { cn } from '@/lib/utils'
import { haptic } from '@/telegram/haptics'
import { useDisableVerticalSwipes } from '@/telegram/controls'
import { PageSpinner } from '@/components/PageSpinner'
import { NotFound } from '@/routes/NotFound'
import { t } from '@/i18n/en'
import { TaskHeader } from './TaskHeader'
import { Timeline } from './Timeline'
import { Composer } from './Composer'
import { InfoTab } from './InfoTab'

const DiffTab = lazy(() => import('./DiffTab'))

type Tab = 'activity' | 'diff' | 'info'
const TABS: Tab[] = ['activity', 'diff', 'info']

export function TaskDetailPage() {
  const id = Number(useParams().id)
  if (!Number.isInteger(id) || id <= 0) return <NotFound title={t.task.notFound} />
  return <TaskDetail key={id} id={id} />
}

function TaskDetail({ id }: { id: number }) {
  const [tab, setTab] = useState<Tab>('activity')
  const task = useTask(id)
  const events = useTaskEvents(id)
  useTaskSubscription(id)
  useDisableVerticalSwipes()

  const items = useMemo(() => buildTimeline(events.data?.items ?? [], events.data?.local ?? []), [events.data])

  if (task.isPending) return <PageSpinner />
  if (task.isError) {
    if (isApiError(task.error, 'not_found')) return <NotFound title={t.task.notFound} />
    return <NotFound title={t.common.somethingWrong} />
  }
  const view = task.data
  const stopping = isStopping(events.data?.items ?? [], view.status)

  return (
    <div className="flex h-full flex-col">
      <TaskHeader task={view} stopping={stopping} />
      <div role="tablist" aria-label="Task sections" className="flex shrink-0 items-end px-2 pt-1 [box-shadow:inset_0_-1px_var(--win-hi)]">
        {TABS.map((name) => (
          <button
            key={name}
            role="tab"
            aria-selected={tab === name}
            onClick={() => {
              haptic.select()
              setTab(name)
            }}
            className={cn(
              'relative h-10 px-3 text-sm text-foreground [box-shadow:inset_1px_1px_var(--win-hi),inset_-1px_0_var(--win-dark),inset_-2px_0_var(--win-shadow)]',
              tab === name && 'z-10 -mx-0.5 h-11 bg-background font-bold',
            )}
          >
            {t.task.tabs[name]}
          </button>
        ))}
      </div>
      <div className="flex min-h-0 flex-1 flex-col">
        {tab === 'activity' && (
          <>
            <Timeline task={view} items={items} older={events.data?.older ?? ''} loading={events.isPending} stopping={stopping} />
            <Composer task={view} />
          </>
        )}
        {tab === 'diff' && (
          <Suspense fallback={<PageSpinner />}>
            <DiffTab taskId={id} />
          </Suspense>
        )}
        {tab === 'info' && <InfoTab task={view} />}
      </div>
    </div>
  )
}

export default TaskDetailPage
