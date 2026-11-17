import type { StartFrom } from '@/api/types'
import { cn } from '@/lib/utils'
import { lc } from '@/i18n/local'

const OPTIONS: StartFrom[] = ['last_commit', 'working_tree']

/** Local repositories: start from the last commit, or include the uncommitted changes (`start_from`). */
export function StartFromField({ value, onChange, dirty }: { value: StartFrom; onChange: (v: StartFrom) => void; dirty?: boolean }) {
  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 text-sm font-semibold">{lc.startFrom.label}</legend>
      <div role="radiogroup" aria-label={lc.startFrom.label} className="grid gap-2 sm:grid-cols-2">
        {OPTIONS.map((o) => (
          <label
            key={o}
            className={cn(
              'fm-btn flex min-h-11 cursor-pointer items-center gap-2 px-3 py-2 text-sm',
              value === o && 'fm-pressed bg-muted font-semibold',
            )}
          >
            <input type="radio" name="start_from" value={o} checked={value === o} onChange={() => onChange(o)} className="accent-[var(--ink)]" />
            {lc.startFrom[o]}
            {o === 'working_tree' && dirty && <span className="ml-auto fm-label text-signal-text">{lc.folder.dirty}</span>}
          </label>
        ))}
      </div>
      {value === 'working_tree' && <p className="text-xs text-muted-foreground">{lc.startFrom.hint}</p>}
    </fieldset>
  )
}
