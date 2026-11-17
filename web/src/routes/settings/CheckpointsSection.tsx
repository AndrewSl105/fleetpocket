import { toast } from 'sonner'
import { useCheckpointSettings, useSaveCheckpointSettings } from '@/api/checkpoints'
import { errorMessage } from '@/api/hooks'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { haptic } from '@/telegram/haptics'
import { cp } from '@/i18n/checkpoints'

const s = cp.settings

/** Global switch for checkpoints (PUT /settings/checkpoints; default off). */
export function CheckpointsSection() {
  const settings = useCheckpointSettings()
  const save = useSaveCheckpointSettings()

  async function toggle(enabled: boolean) {
    try {
      await save.mutateAsync({ enabled })
      haptic.success()
      toast.success(s.saved)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  return (
    <section aria-labelledby="checkpoints-title">
      <h2 id="checkpoints-title" className="mb-1 text-sm font-bold">
        {s.title}
      </h2>
      <p className="mb-3 text-xs text-muted-foreground">{s.hint}</p>
      {settings.data ? (
        <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3">
          <span className="text-sm font-medium">{s.enabled}</span>
          <Switch checked={settings.data.enabled} disabled={save.isPending} onCheckedChange={toggle} aria-label={s.enabled} />
        </label>
      ) : (
        <Skeleton className="h-11" />
      )}
    </section>
  )
}
