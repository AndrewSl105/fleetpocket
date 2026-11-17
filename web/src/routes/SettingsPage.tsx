import type { ReactNode } from 'react'
import { LogOut, Monitor, Moon, Sun } from 'lucide-react'
import { useHealth, useMode, useStats } from '@/api/hooks'
import { useSession } from '@/auth/AuthGate'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useTheme, type ThemeChoice } from '@/lib/theme'
import { Cost } from '@/components/Cost'
import { cn } from '@/lib/utils'
import { useRuntime } from '@/telegram/runtime'
import { t } from '@/i18n/en'
import { ComputersSection } from './settings/ComputersSection'
import { AutoFixSection } from './settings/AutoFixSection'
import { CheckpointsSection } from './settings/CheckpointsSection'
import { GlobalServersSection } from './integrations/GlobalServersSection'
import { CostSettingsSection } from './settings/CostSettingsSection'
import { GitHubSection } from './settings/GitHubSection'
import { Link } from 'react-router'
import { ChevronRight } from 'lucide-react'
import { c } from '@/i18n/costs'
import { sk } from '@/i18n/skills'
import { sc } from '@/i18n/schedules'

const s = t.settings
const version = import.meta.env.VITE_APP_VERSION || 'dev'

function Stat({ label, value, className }: { label: string; value: ReactNode; className?: string }) {
  return (
    <div className={cn('fm-well px-3.5 py-3', className)}>
      <div className="text-xs text-muted-foreground">{label}</div>
      <div className="mt-0.5 text-xl font-semibold tabular-nums">{value}</div>
    </div>
  )
}

const THEMES: Array<{ value: ThemeChoice; icon: typeof Sun }> = [
  { value: 'system', icon: Monitor },
  { value: 'light', icon: Sun },
  { value: 'dark', icon: Moon },
]

export default function SettingsPage() {
  const runtime = useRuntime()
  const session = useSession()
  const stats = useStats()
  const health = useHealth()
  const theme = useTheme()
  const mode = useMode()

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl space-y-8 px-4 pt-4 pb-8">
        <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">{s.title}</h1>

        <section>
          <h2 className="mb-3 text-sm font-bold">{s.stats}</h2>
          {/* Counts in a row of three, costs in a row of two: five cards never leave one stranded. */}
          {stats.data ? (
            <div className="grid grid-cols-6 gap-2">
              <Stat className="col-span-2" label={s.running} value={stats.data.running} />
              <Stat className="col-span-2" label={s.queued} value={stats.data.queued} />
              <Stat className="col-span-2" label={s.waiting} value={stats.data.waiting_input} />
              <Stat className="col-span-3" label={s.costToday} value={<Cost value={stats.data.cost_usd_today} unknown={stats.data.cost_unknown_today} />} />
              <Stat className="col-span-3" label={s.costMonth} value={<Cost value={stats.data.cost_usd_month} unknown={stats.data.cost_unknown_month} />} />
            </div>
          ) : (
            <div className="grid grid-cols-6 gap-2">
              {[2, 2, 2, 3, 3].map((span, i) => (
                <Skeleton key={i} className={cn('h-[68px] rounded-xl', span === 2 ? 'col-span-2' : 'col-span-3')} />
              ))}
            </div>
          )}
          <Link to="/costs" className="mt-2 flex min-h-11 items-center gap-3 fm-group px-3.5 py-2 hover:bg-accent/60">
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{c.open}</span>
              <span className="block text-xs text-muted-foreground">{c.openHint}</span>
            </span>
            <ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" />
          </Link>
          <Link to="/skills" className="mt-2 flex min-h-11 items-center gap-3 fm-group px-3.5 py-2 hover:bg-accent/60">
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{sk.open}</span>
              <span className="block text-xs text-muted-foreground">{sk.openHint}</span>
            </span>
            <ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" />
          </Link>
          <Link to="/schedules" className="mt-2 flex min-h-11 items-center gap-3 fm-group px-3.5 py-2 hover:bg-accent/60">
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{sc.open}</span>
              <span className="block text-xs text-muted-foreground">{sc.openHint}</span>
            </span>
            <ChevronRight className="size-4 text-muted-foreground" aria-hidden="true" />
          </Link>
        </section>

        {runtime.mode === 'browser' && (
          <section>
            <h2 className="mb-3 text-sm font-bold">{s.theme}</h2>
            <div role="radiogroup" aria-label={s.theme} className="grid grid-cols-3 gap-2">
              {THEMES.map(({ value, icon: Icon }) => (
                <button
                  key={value}
                  role="radio"
                  aria-checked={theme.choice === value}
                  onClick={() => theme.setChoice(value)}
                  className={cn(
                    'fm-btn flex h-11 items-center justify-center gap-2 text-sm',
                    theme.choice === value && 'fm-pressed bg-muted font-bold',
                  )}
                >
                  <Icon className="size-4" aria-hidden="true" />
                  {s.themes[value]}
                </button>
              ))}
            </div>
          </section>
        )}

        {mode === 'local' && <GitHubSection />}

        <ComputersSection />

        <AutoFixSection />

        <CheckpointsSection />
        <CostSettingsSection />
        <GlobalServersSection />

        <section>
          <h2 className="mb-3 text-sm font-bold">{s.about}</h2>
          <dl className="divide-y fm-field px-3.5 text-sm">
            <div className="flex justify-between gap-4 py-2.5">
              <dt className="shrink-0 text-muted-foreground">{s.version}</dt>
              <dd className="min-w-0 truncate font-mono" title={version}>
                {/^[0-9a-f]{40}$/.test(version) ? version.slice(0, 7) : version}
              </dd>
            </div>
            <div className="flex justify-between gap-4 py-2.5">
              <dt className="text-muted-foreground">{s.backend}</dt>
              <dd className="text-right">
                {health.data ? (
                  <span className={health.data.ok ? 'text-olive-text' : 'text-danger'}>
                    {health.data.ok ? s.healthy : s.unhealthy}
                    <span className="block font-mono text-xs text-muted-foreground">
                      {Object.entries(health.data.checks)
                        .map(([k, v]) => `${k}: ${v}`)
                        .join(' · ')}
                    </span>
                  </span>
                ) : (
                  '…'
                )}
              </dd>
            </div>
            <div className="flex justify-between gap-4 py-2.5">
              <dt className="text-muted-foreground">{s.session}</dt>
              <dd>{session.session}</dd>
            </div>
          </dl>
        </section>

        {runtime.mode === 'browser' && (
          <Button variant="outline" className="h-11 w-full" onClick={() => void session.logout()}>
            <LogOut /> {t.auth.logout}
          </Button>
        )}
      </div>
    </div>
  )
}
