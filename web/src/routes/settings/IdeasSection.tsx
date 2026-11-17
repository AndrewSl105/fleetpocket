import { Link } from 'react-router'
import { toast } from 'sonner'
import { useIdeaSettings, useSaveIdeaSettings } from '@/api/ideas'
import { errorMessage } from '@/api/hooks'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { haptic } from '@/telegram/haptics'
import { id as tx } from '@/i18n/ideas'

/** Global switch for the Ideas board (PUT /settings/ideas; default off). */
export function IdeasSection() {
  const settings = useIdeaSettings()
  const save = useSaveIdeaSettings()

  async function toggle(enabled: boolean) {
    try {
      await save.mutateAsync({ enabled })
      haptic.success()
      toast.success(tx.saved)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  return (
    <section aria-labelledby="ideas-title" id="ideas">
      <h2 id="ideas-title" className="mb-1 text-sm font-bold">
        {tx.settingsTitle}
      </h2>
      <p className="mb-3 text-xs text-muted-foreground">{tx.settingsHint}</p>
      {settings.data ? (
        <>
          <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3">
            <span className="text-sm font-medium">{tx.settingsEnabled}</span>
            <Switch checked={settings.data.enabled} disabled={save.isPending} onCheckedChange={toggle} aria-label={tx.settingsEnabled} />
          </label>
          {settings.data.enabled && (
            <Link to="/ideas" className="mt-2 inline-flex min-h-11 items-center text-sm underline">
              {tx.openBoard}
            </Link>
          )}
        </>
      ) : (
        <Skeleton className="h-11" />
      )}
    </section>
  )
}
