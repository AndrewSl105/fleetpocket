import { useMemo, useState } from 'react'
import { Link } from 'react-router'
import { Download } from 'lucide-react'
import { costsCsv, useCosts } from '@/api/costs'
import { RANGES, rangeDates, type Range } from './range'
import type { CostReport } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { CostBar } from '@/components/CostBar'
import { Cost } from '@/components/Cost'
import { usd } from '@/lib/format'
import { cn } from '@/lib/utils'
import { copyText } from '@/components/CopyButton'
import { useRuntime } from '@/telegram/runtime'
import { t } from '@/i18n/en'
import { c } from '@/i18n/costs'

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/csv;charset=utf-8' }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

/**
 * Telegram's in-app browsers often ignore `a.download` on a blob URL: there
 * the CSV goes to the share sheet as a file when the WebView can share files,
 * else to the clipboard.
 */
async function saveCsv(name: string, text: string, telegram: boolean) {
  if (telegram) {
    const file = new File([text], name, { type: 'text/csv' })
    if (navigator.canShare?.({ files: [file] })) {
      try {
        await navigator.share({ files: [file], title: name })
        return
      } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
      }
    }
    await copyText(text)
    return
  }
  download(name, text)
}

function Section({ title, children, action }: { title: string; children: React.ReactNode; action?: React.ReactNode }) {
  return (
    <section>
      <div className="mb-2 flex items-end justify-between gap-2">
        <h2 className="fm-label font-semibold">{title}</h2>
        {action}
      </div>
      {children}
    </section>
  )
}

/** One ruled row: label, a bar against the largest value, the amount. */
function BarRow({ label, sub, value, max, estimated, unknown, to }: { label: string; sub?: string; value: number; max: number; estimated?: boolean; unknown?: boolean; to?: string }) {
  const body = (
    <>
      <div className="flex items-baseline justify-between gap-3">
        <span className="min-w-0 truncate text-sm font-medium">{label}</span>
        <Cost className="shrink-0 font-mono text-sm tabular-nums" value={value} estimated={estimated} unknown={unknown} />
      </div>
      <CostBar value={value} max={max} className="mt-1.5" label={label} />
      {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
    </>
  )
  return (
    <li>
      {to ? (
        <Link to={to} className="block min-h-11 px-3.5 py-2.5 hover:bg-accent/60">
          {body}
        </Link>
      ) : (
        <div className="px-3.5 py-2.5">{body}</div>
      )}
    </li>
  )
}

function Totals({ report }: { report: CostReport }) {
  const tot = report.total
  const cap = report.budget.monthly_usd
  const lines = [
    tot.estimated_usd > 0 && c.estimated(tot.estimated_usd),
    tot.autofix_usd > 0 && c.autofix(tot.autofix_usd),
    tot.api_equivalent_usd > 0 && c.apiEquivalentPart(tot.api_equivalent_usd),
    tot.unknown_runs > 0 && c.unknownRuns(tot.unknown_runs),
  ].filter(Boolean) as string[]
  return (
    <div className="fm-panel px-4 py-3.5">
      <div className="fm-label text-muted-foreground">{c.total}</div>
      <Cost className="mt-0.5 block text-3xl font-semibold tabular-nums" value={tot.cost_usd} estimated={tot.estimated_usd > 0} unknown={tot.unknown_runs > 0} />
      <div className="mt-1 text-xs text-muted-foreground">
        {c.runs(tot.runs)} · {c.tokens(tot.input_tokens, tot.output_tokens)}
      </div>
      {lines.length > 0 && (
        <ul className="mt-2 space-y-0.5 text-xs text-muted-foreground">
          {lines.map((l) => (
            <li key={l}>{l}</li>
          ))}
        </ul>
      )}
      {tot.api_equivalent_usd > 0 && <p className="mt-2 text-xs text-muted-foreground">{c.apiEquivalentHint}</p>}
      <div className="mt-3 fm-rule pt-3">
        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1 text-xs">
          <span className="fm-label">{c.budget}</span>
          <span className="font-mono tabular-nums">
            {cap
              ? c.ofBudget(report.budget.month_cost_usd, cap, report.budget.month_cost_unknown)
              : `${usd(report.budget.month_cost_usd, false, report.budget.month_cost_unknown)} · ${c.noBudget}`}
          </span>
        </div>
        {cap ? <CostBar value={report.budget.month_cost_usd} max={cap} tone="budget" className="mt-1.5" label={c.budget} /> : null}
      </div>
    </div>
  )
}

export default function CostsPage() {
  const runtime = useRuntime()
  const [range, setRange] = useState<Range>('month')
  const { from, to, period } = useMemo(() => rangeDates(range), [range])
  const report = useCosts(from, to, period)
  const data = report.data

  const names = useMemo(() => new Map((data?.projects ?? []).map((p) => [p.project_id, p.name])), [data])
  const budgets = useMemo(() => new Map((data?.projects ?? []).map((p) => [p.project_id, p])), [data])
  const maxOf = (xs: { cost_usd: number }[]) => Math.max(0, ...xs.map((x) => x.cost_usd))

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl space-y-6 px-4 pt-4 pb-8">
        <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">{c.title}</h1>

        <div role="radiogroup" aria-label={c.rangeLabel} className="grid grid-cols-3 gap-2">
          {RANGES.map((r) => (
            <button
              key={r}
              type="button"
              role="radio"
              aria-checked={range === r}
              onClick={() => setRange(r)}
              className={cn('fm-btn flex h-11 items-center justify-center px-1 text-sm', range === r && 'fm-pressed font-bold')}
            >
              {c.range[r]}
            </button>
          ))}
        </div>

        {report.isError ? (
          <p className="py-6 text-center text-sm text-danger">{t.common.somethingWrong}</p>
        ) : !data ? (
          <div className="space-y-3">
            <Skeleton className="h-40" />
            <Skeleton className="h-32" />
          </div>
        ) : (
          <>
            <Totals report={data} />

            <Section title={c.byProject}>
              {data.by_project.length === 0 ? (
                <p className="text-sm text-muted-foreground">{c.empty}</p>
              ) : (
                <ul className="divide-y fm-field">
                  {data.by_project.map((p) => {
                    const b = budgets.get(p.key)
                    const sub = b?.monthly_usd ? `${c.budget}: ${c.ofBudget(b.month_cost_usd, b.monthly_usd, b.month_cost_unknown)}` : c.runs(p.runs)
                    return (
                      <BarRow
                        key={p.key}
                        label={names.get(p.key) ?? p.key}
                        sub={sub}
                        value={p.cost_usd}
                        max={maxOf(data.by_project)}
                        estimated={p.estimated_usd > 0}
                        unknown={p.unknown_runs > 0}
                        to={`/projects/${encodeURIComponent(p.key)}`}
                      />
                    )
                  })}
                </ul>
              )}
            </Section>

            <Section title={c.byAgent}>
              {data.by_agent.length === 0 ? (
                <p className="text-sm text-muted-foreground">{c.empty}</p>
              ) : (
                <ul className="divide-y fm-field">
                  {data.by_agent.map((a) => (
                    <BarRow
                      key={a.key}
                      label={(t.agents as Record<string, string>)[a.key] ?? a.key}
                      sub={`${c.runs(a.runs)} · ${c.tokens(a.input_tokens, a.output_tokens)}${a.unknown_runs ? ` · ${c.unknownRuns(a.unknown_runs)}` : ''}`}
                      value={a.cost_usd}
                      max={maxOf(data.by_agent)}
                      estimated={a.estimated_usd > 0}
                      unknown={a.unknown_runs > 0}
                    />
                  ))}
                </ul>
              )}
            </Section>

            {data.people && (
              // Team mode: cost by person (the task creator of each run).
              <Section title={c.byPerson}>
                {data.people.length === 0 ? (
                  <p className="text-sm text-muted-foreground">{c.empty}</p>
                ) : (
                  <ul className="divide-y fm-field">
                    {data.people.map((p) => (
                      <BarRow
                        key={p.user_id ?? 'none'}
                        label={p.name}
                        sub={`${c.runs(p.runs)}${p.monthly_budget_usd != null ? ` · ${c.personBudget(p.monthly_budget_usd)}` : ''}`}
                        value={p.cost_usd}
                        max={Math.max(...data.people!.map((x) => x.cost_usd), 0)}
                      />
                    ))}
                  </ul>
                )}
              </Section>
            )}

            <Section
              title={period === 'week' ? c.byWeek : period === 'month' ? c.byMonth : c.byDay}
              action={
                <Button
                  variant="outline"
                  className="h-11"
                  disabled={data.rows.length === 0}
                  onClick={() => void saveCsv(c.csvFile(data.from, data.to), costsCsv(data), runtime.mode === 'telegram')}
                >
                  <Download /> {c.exportCsv}
                </Button>
              }
            >
              <ol className="divide-y fm-field">
                {[...data.by_period].reverse().map((d) => (
                  <li key={d.start} className="grid grid-cols-[4.5rem_1fr_4.5rem] items-center gap-3 px-3.5 py-2">
                    <span className="font-mono text-xs text-muted-foreground tabular-nums">{d.start.slice(5)}</span>
                    <CostBar value={d.cost_usd} max={maxOf(data.by_period)} label={d.start} />
                    <Cost className="text-right font-mono text-xs tabular-nums" value={d.cost_usd} estimated={d.estimated_usd > 0} unknown={d.unknown_runs > 0} />
                  </li>
                ))}
              </ol>
            </Section>
          </>
        )}
      </div>
    </div>
  )
}
