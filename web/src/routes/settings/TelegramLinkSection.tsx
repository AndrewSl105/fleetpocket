import { toast } from 'sonner'
import { errorMessage } from '@/api/hooks'
import { useCurrentUser, useTelegramLinkCode, useTelegramUnlink } from '@/api/team'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/CopyButton'
import { relativeTime } from '@/lib/format'
import { tm } from '@/i18n/team'

/** Settings → Telegram (team mode): link this person's Telegram account with /link. */
export function TelegramLinkSection() {
  const me = useCurrentUser()
  const code = useTelegramLinkCode()
  const unlink = useTelegramUnlink()
  if (!me) return null
  return (
    <section aria-labelledby="tg-title" id="telegram">
      <h2 id="tg-title" className="mb-1 text-sm font-bold">
        {tm.tgTitle}
      </h2>
      <div className="fm-group space-y-3 px-3.5 py-3">
        <p className="text-sm">{me.telegram_linked ? tm.tgLinked : tm.tgNotLinked}</p>
        {code.data && (
          <div className="space-y-1">
            <p className="text-xs text-muted-foreground">{code.data.bot_enabled ? tm.tgSend : tm.tgNoBot}</p>
            <div className="flex items-center gap-2">
              <code className="fm-field flex h-10 flex-1 items-center px-3 font-mono text-base">{code.data.command}</code>
              <CopyButton text={code.data.command} />
            </div>
            <p className="text-xs text-muted-foreground">{relativeTime(code.data.expires_at)}</p>
          </div>
        )}
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" className="h-11 flex-1" onClick={() => code.mutate(undefined, { onError: (e) => toast.error(errorMessage(e)) })} disabled={code.isPending}>
            {tm.tgGetCode}
          </Button>
          {me.telegram_linked && (
            <Button
              variant="outline"
              className="h-11 text-danger"
              disabled={unlink.isPending}
              onClick={() => unlink.mutate(undefined, { onSuccess: () => toast.success(tm.tgUnlinked), onError: (e) => toast.error(errorMessage(e)) })}
            >
              {tm.tgUnlink}
            </Button>
          )}
        </div>
      </div>
    </section>
  )
}
