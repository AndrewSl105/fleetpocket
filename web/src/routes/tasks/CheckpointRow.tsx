import { lazy, Suspense, useState } from 'react'
import { History, Undo2 } from 'lucide-react'
import type { TimelineItem } from '@/lib/timeline'
import { useTaskCheckpoints } from '@/api/checkpoints'
import { cp } from '@/i18n/checkpoints'

// The dialog (and its preview request) loads only when the owner opens it.
const RestoreDialog = lazy(() => import('./RestoreDialog'))

/** A checkpoint marker with "Restore here" (docs/API.md "Checkpoints"). */
export function CheckpointRow({ item, taskId }: { item: Extract<TimelineItem, { kind: 'checkpoint' }>; taskId: number }) {
  const list = useTaskCheckpoints(taskId)
  const [open, setOpen] = useState(false)
  const checkpoint = list.data?.items.find((c) => c.event_id === item.eventId)
  const reason = checkpoint && !checkpoint.available ? checkpoint.unavailable_reason : undefined
  return (
    <div className="flex min-h-11 items-center gap-2 border-y border-border px-2 text-xs text-muted-foreground">
      <History className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0 flex-1 truncate">{cp.marker(item.n, item.reason, item.files)}</span>
      {checkpoint && (
        <button
          type="button"
          onClick={() => setOpen(true)}
          disabled={!checkpoint.available}
          title={reason ?? undefined}
          aria-label={reason ? `${cp.unavailable}: ${reason}` : `${cp.restoreHere} (${item.n})`}
          className="inline-flex h-11 shrink-0 items-center gap-1.5 px-3 font-bold text-foreground fm-btn active:fm-pressed disabled:opacity-50"
        >
          <Undo2 className="size-3.5" aria-hidden="true" />
          {cp.restoreHere}
        </button>
      )}
      {reason && <span className="sr-only">{reason}</span>}
      {open && checkpoint && (
        <Suspense fallback={null}>
          <RestoreDialog taskId={taskId} checkpoint={checkpoint} open={open} onOpenChange={setOpen} />
        </Suspense>
      )}
    </div>
  )
}
