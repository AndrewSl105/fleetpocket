import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { LoopValues } from '@/routes/schedules/scheduleForm'
import { lp } from '@/i18n/loops'

const ITERATIONS = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]

/** "Loop until…": a switch, then the check command, max iterations and the loop budget. */
export function LoopFields({ value, onChange, error, idPrefix = 'loop' }: { value: LoopValues; onChange: (v: LoopValues) => void; error?: string; idPrefix?: string }) {
  const set = (patch: Partial<LoopValues>) => onChange({ ...value, ...patch })
  return (
    <fieldset className="space-y-3">
      <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3">
        <span className="min-w-0">
          <span className="block text-sm font-medium">{lp.toggle}</span>
          {value.enabled && <span className="block text-xs text-muted-foreground">{lp.toggleHint}</span>}
        </span>
        <Switch checked={value.enabled} onCheckedChange={(enabled) => set({ enabled })} aria-label={lp.toggle} data-testid={`${idPrefix}-toggle`} />
      </label>
      {value.enabled && (
        <div className="grid gap-3 border-l-2 border-amber/50 pl-3 sm:grid-cols-[1fr_8rem_9rem]">
          <div className="space-y-1.5">
            <Label htmlFor={`${idPrefix}-check`}>{lp.check}</Label>
            <Input
              id={`${idPrefix}-check`}
              value={value.check}
              onChange={(e) => set({ check: e.target.value })}
              placeholder={lp.checkPlaceholder}
              className="h-11 font-mono text-sm"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
              aria-invalid={!!error}
            />
            {error ? <p className="text-xs text-destructive">{error}</p> : <p className="text-xs text-muted-foreground">{lp.checkExamples}</p>}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${idPrefix}-max`}>{lp.maxIterations}</Label>
            <Select value={String(value.max)} onValueChange={(v) => set({ max: Number(v) })}>
              <SelectTrigger id={`${idPrefix}-max`} className="h-11 w-full font-mono">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ITERATIONS.map((n) => (
                  <SelectItem key={n} value={String(n)} className="h-10 font-mono">
                    {n}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor={`${idPrefix}-budget`}>{lp.budget}</Label>
            <Input
              id={`${idPrefix}-budget`}
              value={value.budget}
              onChange={(e) => set({ budget: e.target.value })}
              inputMode="decimal"
              placeholder="—"
              className="h-11 font-mono"
              aria-describedby={`${idPrefix}-budget-hint`}
            />
            <p id={`${idPrefix}-budget-hint`} className="text-xs text-muted-foreground">
              {lp.budgetHint}
            </p>
          </div>
        </div>
      )}
    </fieldset>
  )
}
