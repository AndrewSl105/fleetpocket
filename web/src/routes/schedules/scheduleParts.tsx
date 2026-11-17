import { useNavigate } from 'react-router'
import { toast } from 'sonner'
import { useDeleteSchedule, useDuplicateSchedule, useRunSchedule, useSetScheduleEnabled } from '@/api/schedules'
import { errorMessage } from '@/api/hooks'
import type { Schedule, ScheduleFiring } from '@/api/types'
import { useConfirm } from '@/components/confirm'
import { STATUS } from '@/lib/status'
import { haptic } from '@/telegram/haptics'
import { sc } from '@/i18n/schedules'

/** The stamp of a firing: its task's status, else the firing's outcome. */
export function FiringStamp({ f }: { f: ScheduleFiring | null }) {
  if (!f) return <span className="fm-stamp" data-tone="paused">{sc.never}</span>
  if (f.task_status) {
    const st = STATUS[f.task_status]
    return (
      <span className="fm-stamp" data-tone={st.tone === 'plain' ? undefined : st.tone}>
        {st.label}
      </span>
    )
  }
  const tone = f.outcome === 'failed' ? 'danger' : f.outcome === 'queued' ? 'ask' : 'paused'
  return (
    <span className="fm-stamp" data-tone={tone}>
      {sc.outcome(f.outcome)}
    </span>
  )
}

/** Run now, pause or resume, duplicate and delete, with their toasts. */
export function useScheduleActions(s: Schedule) {
  const confirm = useConfirm()
  const navigate = useNavigate()
  const run = useRunSchedule()
  const toggle = useSetScheduleEnabled()
  const dup = useDuplicateSchedule()
  const del = useDeleteSchedule()
  const fail = (e: unknown) => {
    haptic.error()
    toast.error(errorMessage(e))
  }
  return {
    busy: run.isPending || toggle.isPending || dup.isPending || del.isPending,
    runNow: () =>
      run.mutate(s.id, {
        onSuccess: (f) => {
          haptic.success()
          if (f.outcome === 'started' || f.outcome === 'queued') toast.success(sc.fired(f.outcome, f.task_id))
          else toast.message(sc.fired(f.outcome) + (f.reason ? ` (${f.reason})` : ''))
        },
        onError: fail,
      }),
    toggle: () =>
      toggle.mutate(
        { id: s.id, enabled: !s.enabled },
        { onSuccess: (n) => toast.success(n.enabled ? sc.resumedToast(n.name) : sc.pausedToast(n.name)), onError: fail },
      ),
    duplicate: () =>
      dup.mutate(s.id, {
        onSuccess: (c) => {
          toast.success(sc.duplicated(c.name))
          void navigate(`/schedules/${c.id}`)
        },
        onError: fail,
      }),
    remove: async () => {
      if (!(await confirm(sc.deleteConfirm(s.name), sc.delete))) return
      del.mutate(s.id, {
        onSuccess: () => {
          toast.success(sc.deleted(s.name))
          void navigate('/schedules')
        },
        onError: fail,
      })
    },
  }
}
