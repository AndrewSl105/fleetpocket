import { Logo } from '@/components/Logo'
import { t } from '@/i18n/en'

export function Splash() {
  return (
    <div className="win-desktop flex h-(--app-height) flex-col items-center justify-center gap-5" role="status" aria-label={t.auth.signingIn}>
      <Logo className="size-14" />
      <div className="win-field h-5 w-48 overflow-hidden p-[3px]">
        <div className="fp-progress h-full" />
      </div>
    </div>
  )
}
