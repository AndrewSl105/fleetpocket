import { ShieldX, Clock, CloudOff } from 'lucide-react'
import type { ReactNode } from 'react'
import { Button } from '@/components/ui/button'
import { Window } from '@/components/Window'
import { useRuntime } from '@/telegram/runtime'
import { t } from '@/i18n/en'

function Screen({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <main className="win-desktop flex min-h-(--app-height) items-center justify-center px-3">
      <Window title={t.app.name} className="w-full max-w-sm" bodyClassName="flex gap-3 p-3">
        <div className="shrink-0 text-destructive">{icon}</div>
        <div className="flex min-w-0 flex-col items-start gap-2">
          <h1 className="text-sm font-bold">{title}</h1>
          {children}
        </div>
      </Window>
    </main>
  )
}

export function DeniedScreen() {
  const runtime = useRuntime()
  const user = runtime.mode === 'telegram' ? runtime.tg.initDataUnsafe?.user : undefined
  const name = user ? [user.first_name, user.last_name].filter(Boolean).join(' ') || user.username || '' : ''
  return (
    <Screen icon={<ShieldX className="size-8" />} title={t.auth.deniedTitle}>
      <p className="text-sm">{t.auth.deniedBody}</p>
      {user && <p className="font-mono text-sm select-all">{t.auth.deniedUser(name, user.id)}</p>}
    </Screen>
  )
}

export function ExpiredScreen() {
  return (
    <Screen icon={<Clock className="size-8" />} title={t.auth.expiredTitle}>
      <p className="text-sm">{t.auth.expiredBody}</p>
    </Screen>
  )
}

export function UnavailableScreen({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <Screen icon={<CloudOff className="size-8" />} title={t.auth.unavailableTitle}>
      <p className="text-sm">{message}</p>
      <Button className="mt-2 h-9 min-w-24 self-end" onClick={onRetry}>
        {t.common.retry}
      </Button>
    </Screen>
  )
}
