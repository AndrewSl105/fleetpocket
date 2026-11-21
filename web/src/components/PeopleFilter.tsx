import { useUsers } from '@/api/team'
import { cn } from '@/lib/utils'
import { haptic } from '@/telegram/haptics'
import type { usePeopleScope } from '@/lib/people'
import { t } from '@/i18n/en'

const tm = t.team

type Props = { people: ReturnType<typeof usePeopleScope>; className?: string }

/** Mine | Everyone, plus a person picker under Everyone (team mode only). */
export function PeopleFilter({ people, className }: Props) {
  const users = useUsers(people.team)
  if (!people.team) return null
  const others = (users.data ?? []).filter((u) => !u.disabled_at)
  return (
    <div className={cn('flex min-w-0 items-center gap-2', className)}>
      <div role="radiogroup" aria-label={tm.mine + ' / ' + tm.everyone} className="grid shrink-0 grid-cols-2 gap-1 rounded-full border border-border p-1">
        {(['mine', 'everyone'] as const).map((s) => (
          <button
            key={s}
            role="radio"
            aria-checked={people.scope === s}
            onClick={() => {
              haptic.select()
              people.setScope(s)
            }}
            className={cn('fm-label h-8 rounded-full px-3 text-[13px]', people.scope === s ? 'bg-panel-2 text-foreground font-semibold shadow-[inset_0_0_0_1px_rgb(242_182_90/0.55)]' : 'text-muted-foreground hover:bg-grid')}
          >
            {s === 'mine' ? tm.mine : tm.everyone}
          </button>
        ))}
      </div>
      {people.scope === 'everyone' && others.length > 1 && (
        <select
          aria-label={tm.person}
          value={people.person ?? ''}
          onChange={(e) => people.setPerson(e.target.value ? Number(e.target.value) : undefined)}
          className="fm-field h-9 min-w-0 flex-1 bg-background px-2 text-xs"
        >
          <option value="">{tm.anyone}</option>
          {others.map((u) => (
            <option key={u.id} value={u.id}>
              {u.name}
            </option>
          ))}
        </select>
      )}
    </div>
  )
}
