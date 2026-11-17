import { lazy, Suspense, useState } from 'react'
import { Mic } from 'lucide-react'
import { useMe } from '@/api/hooks'
import { t } from '@/i18n/en'
import { cn } from '@/lib/utils'

// The recorder (and the Ideas API) load only when the owner asks for them.
const load = () => import('@/routes/ideas/QuickRecorder')
const QuickRecorder = lazy(load)

/** A small mic button in the header that opens the hold-to-talk recorder; only while the Ideas board is on. */
export function CaptureTrigger({ className }: { className?: string }) {
  const on = useMe().data?.ideas_enabled
  const [open, setOpen] = useState(false)
  if (!on || typeof MediaRecorder === 'undefined') return null
  return (
    <>
      <button type="button" aria-label={t.nav.record} title={t.nav.record} onPointerEnter={() => void load()} onClick={() => setOpen(true)} className={cn('fm-btn flex size-11 shrink-0 items-center justify-center', className)}>
        <Mic className="size-4" aria-hidden="true" />
      </button>
      {open && (
        <Suspense>
          <QuickRecorder onClose={() => setOpen(false)} />
        </Suspense>
      )}
    </>
  )
}
