import { useState } from 'react'
import { toast } from 'sonner'
import { Plus, X } from 'lucide-react'
import { errorMessage } from '@/api/hooks'
import { useCostSettings, useSaveCostSettings, type CostSettingsInput } from '@/api/costs'
import type { Agent, CostSettings, PriceRow } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { haptic } from '@/telegram/haptics'
import { ymd } from '@/routes/costs/range'
import { t } from '@/i18n/en'
import { c } from '@/i18n/costs'

const AGENTS: Agent[] = ['claude', 'codex', 'cursor']

type Row = { agent: Agent; model: string; input: string; cached: string; output: string; cacheWrite: number | null }
type Form = { cap: string; rows: Row[] }

const num = (v: string) => (v.trim() === '' ? NaN : Number(v))

function toForm(s: CostSettings): Form {
  return {
    cap: s.monthly_budget_usd ? String(s.monthly_budget_usd) : '',
    rows: s.price_table.prices.map((p) => ({
      agent: p.agent,
      model: p.model,
      input: String(p.input_usd),
      cached: p.cached_input_usd == null ? '' : String(p.cached_input_usd),
      output: String(p.output_usd),
      cacheWrite: p.cache_write_usd ?? null,
    })),
  }
}

function toPrices(rows: Row[]): PriceRow[] | null {
  const out: PriceRow[] = []
  for (const r of rows) {
    const input = num(r.input)
    const output = num(r.output)
    const cached = r.cached.trim() === '' ? null : num(r.cached)
    if (!r.model.trim() || !(input >= 0) || !(output >= 0) || (cached !== null && !(cached >= 0))) return null
    out.push({ agent: r.agent, model: r.model.trim(), input_usd: input, cached_input_usd: cached, cache_write_usd: r.cacheWrite, output_usd: output })
  }
  return out
}

/** Settings → Budgets and prices: the global monthly cap and the price table (PUT /settings/costs). */
export function CostSettingsSection() {
  const settings = useCostSettings()
  return (
    <section aria-labelledby="costs-title">
      <h2 id="costs-title" className="mb-1 text-sm font-bold">
        {c.settingsTitle}
      </h2>
      <p className="mb-3 text-xs text-muted-foreground">{c.settingsHint}</p>
      {settings.data ? <CostForm key={JSON.stringify(settings.data)} settings={settings.data} /> : <Skeleton className="h-40" />}
    </section>
  )
}

function CostForm({ settings }: { settings: CostSettings }) {
  const save = useSaveCostSettings()
  const initial = toForm(settings)
  const [form, setForm] = useState<Form>(initial)
  const cap = form.cap.trim() === '' ? null : num(form.cap)
  const prices = toPrices(form.rows)
  const invalid = (cap !== null && !(cap >= 0)) || prices === null
  const tableChanged = JSON.stringify(form.rows) !== JSON.stringify(initial.rows)

  async function put(body: CostSettingsInput) {
    try {
      await save.mutateAsync(body)
      haptic.success()
      toast.success(c.saved)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  function submit() {
    if (invalid || prices === null) return
    const table = tableChanged || !settings.price_table_is_default ? { updated: tableChanged ? ymd(new Date()) : settings.price_table.updated, prices } : null
    void put({ monthly_budget_usd: cap || null, price_table: table })
  }

  const setRow = (i: number, patch: Partial<Row>) => setForm({ ...form, rows: form.rows.map((r, j) => (j === i ? { ...r, ...patch } : r)) })

  return (
    <div className="space-y-4">
      <div className="space-y-2">
        <Label htmlFor="cost-cap">{c.globalCap}</Label>
        <Input
          id="cost-cap"
          inputMode="decimal"
          className="h-11"
          placeholder="—"
          value={form.cap}
          onChange={(e) => setForm({ ...form, cap: e.target.value })}
        />
        <p className="text-xs text-muted-foreground">{c.globalCapHint}</p>
      </div>

      <div>
        <div className="mb-1 flex items-baseline justify-between gap-2">
          <span className="text-sm font-medium">{c.prices}</span>
          <span className="text-xs text-muted-foreground">
            {settings.price_table_is_default && !tableChanged ? c.pricesDefault : null}
          </span>
        </div>
        {settings.price_table.updated && <p className="text-xs text-muted-foreground">{c.pricesUpdated(settings.price_table.updated)}</p>}
        <p className="mb-2 text-xs text-muted-foreground">{c.pricesHint}</p>
        <ul className="divide-y fm-field">
          {form.rows.map((r, i) => (
            <li key={i} className="space-y-2 px-3 py-2.5">
              <div className="flex items-center gap-2">
                <select
                  aria-label={c.agent}
                  className="h-11 w-24 shrink-0 fm-field bg-transparent px-2 text-sm"
                  value={r.agent}
                  onChange={(e) => setRow(i, { agent: e.target.value as Agent })}
                >
                  {AGENTS.map((a) => (
                    <option key={a} value={a}>
                      {t.agents[a]}
                    </option>
                  ))}
                </select>
                <Input
                  aria-label={c.model}
                  placeholder="*"
                  autoCapitalize="none"
                  className="h-11 min-w-0 flex-1 font-mono"
                  value={r.model}
                  onChange={(e) => setRow(i, { model: e.target.value })}
                />
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="size-11 shrink-0"
                  aria-label={c.removePrice(r.model)}
                  onClick={() => setForm({ ...form, rows: form.rows.filter((_, j) => j !== i) })}
                >
                  <X />
                </Button>
              </div>
              <div className="grid grid-cols-3 gap-2">
                {(
                  [
                    ['input', c.input],
                    ['cached', c.cachedInput],
                    ['output', c.output],
                  ] as const
                ).map(([k, label]) => (
                  <label key={k} className="space-y-1">
                    <span className="fm-label block text-muted-foreground">{label}</span>
                    <Input inputMode="decimal" className="h-11 font-mono" value={r[k]} placeholder={k === 'cached' ? '=' : ''} onChange={(e) => setRow(i, { [k]: e.target.value })} />
                  </label>
                ))}
              </div>
            </li>
          ))}
        </ul>
        <div className="mt-2 flex flex-wrap gap-2">
          <Button
            type="button"
            variant="outline"
            className="h-11"
            onClick={() => setForm({ ...form, rows: [...form.rows, { agent: 'cursor', model: '', input: '', cached: '', output: '', cacheWrite: null }] })}
          >
            <Plus /> {c.addPrice}
          </Button>
          {!settings.price_table_is_default && (
            <Button type="button" variant="outline" className="h-11" disabled={save.isPending} onClick={() => void put({ monthly_budget_usd: cap || null, price_table: null })}>
              {c.resetPrices}
            </Button>
          )}
        </div>
      </div>

      <Button className="h-11 w-full" disabled={invalid || save.isPending} onClick={submit}>
        {t.common.save}
      </Button>
    </div>
  )
}
