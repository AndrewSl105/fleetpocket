import { useState } from 'react'
import { toast } from 'sonner'
import { errorMessage, useAutoFixDefaults, useSaveAutoFixDefaults } from '@/api/hooks'
import type { CIAutoFixSettings } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Skeleton } from '@/components/ui/skeleton'
import { haptic } from '@/telegram/haptics'
import { t } from '@/i18n/en'

const af = t.autofix

type Form = { enabled: boolean; maxAttempts: string; checks: string; budget: string; allowCI: boolean }

function toForm(s: CIAutoFixSettings): Form {
  return {
    enabled: s.enabled ?? false,
    maxAttempts: String(s.max_attempts ?? 2),
    checks: (s.checks ?? []).join(', '),
    budget: s.budget_usd ? String(s.budget_usd) : '',
    allowCI: s.allow_ci_changes ?? false,
  }
}

/** The global CI auto-fix default (PUT /settings/ci-autofix); projects inherit it. */
export function AutoFixSection() {
  const defaults = useAutoFixDefaults()
  return (
    <section aria-labelledby="autofix-title">
      <h2 id="autofix-title" className="mb-1 text-sm font-bold">
        {af.defaultsTitle}
      </h2>
      <p className="mb-3 text-xs text-muted-foreground">{af.defaultsHint}</p>
      {defaults.data ? <AutoFixForm initial={toForm(defaults.data)} /> : <Skeleton className="h-40" />}
    </section>
  )
}

function AutoFixForm({ initial }: { initial: Form }) {
  const save = useSaveAutoFixDefaults()
  const [form, setForm] = useState<Form>(initial)
  const attempts = Number(form.maxAttempts)
  const budget = Number(form.budget || 0)
  const invalid = !Number.isInteger(attempts) || attempts < 1 || attempts > 5 || !(budget >= 0)

  async function submit() {
    if (invalid) return
    const checks = form.checks
      .split(',')
      .map((c) => c.trim())
      .filter(Boolean)
    try {
      await save.mutateAsync({
        enabled: form.enabled,
        max_attempts: attempts,
        checks: checks.length ? checks : null,
        budget_usd: budget || null,
        allow_ci_changes: form.allowCI,
      })
      haptic.success()
      toast.success(af.saved)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  return (
    <div className="space-y-4">
      <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3">
        <span className="text-sm font-medium">{af.enabled}</span>
        <Switch checked={form.enabled} onCheckedChange={(v) => setForm({ ...form, enabled: v })} aria-label={af.enabled} />
      </label>
      <div className="grid grid-cols-2 gap-4">
        <div className="space-y-2">
          <Label htmlFor="af-attempts">{af.maxAttempts}</Label>
          <Input
            id="af-attempts"
            inputMode="numeric"
            className="h-11"
            value={form.maxAttempts}
            onChange={(e) => setForm({ ...form, maxAttempts: e.target.value })}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="af-budget">{af.budget}</Label>
          <Input
            id="af-budget"
            inputMode="decimal"
            placeholder={af.halfBudget}
            className="h-11"
            value={form.budget}
            onChange={(e) => setForm({ ...form, budget: e.target.value })}
          />
        </div>
      </div>
      <div className="space-y-2">
        <Label htmlFor="af-checks">{af.checks}</Label>
        <Input
          id="af-checks"
          autoCapitalize="none"
          placeholder={af.all}
          className="h-11 font-mono"
          value={form.checks}
          onChange={(e) => setForm({ ...form, checks: e.target.value })}
        />
        <p className="text-xs text-muted-foreground">{af.checksHint}</p>
      </div>
      <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3 py-2">
        <span className="text-sm font-medium">{af.allowCI}</span>
        <Switch checked={form.allowCI} onCheckedChange={(v) => setForm({ ...form, allowCI: v })} aria-label={af.allowCI} />
      </label>
      <p className="-mt-2 text-xs text-muted-foreground">{af.allowCIHint}</p>
      <Button className="h-11 w-full" disabled={invalid || save.isPending} onClick={() => void submit()}>
        {t.common.save}
      </Button>
    </div>
  )
}
