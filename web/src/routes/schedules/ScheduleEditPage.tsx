import { useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { Check } from 'lucide-react'
import { toast } from 'sonner'
import { errorMessage, useAgents, useIntegrations, useProjects } from '@/api/hooks'
import { useCreateSchedule, useSchedule, useUpdateSchedule } from '@/api/schedules'
import { useSkills } from '@/api/skills'
import type { Project } from '@/api/types'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { PageSpinner } from '@/components/PageSpinner'
import { PrimaryAction } from '@/telegram/controls'
import { haptic } from '@/telegram/haptics'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'
import { sc } from '@/i18n/schedules'
import { lp } from '@/i18n/loops'
import { LoopFields } from '@/routes/loops/LoopFields'
import { baseSkills } from '@/routes/skills/ComposerSkills'
import { emptyValues, parseMoney, presetCron, toInput, toValues, zones, type Preset, type ScheduleValues } from './scheduleForm'

const PROJECT = 'project' // Radix Select items cannot have an empty value
const PRESETS: Preset[] = ['daily', 'weekdays', 'weekly', 'custom']

/** New or edit schedule: what to run, when (presets or cron, in a time zone), budgets, policy and loop. */
export default function ScheduleEditPage() {
  const params = useParams()
  const id = params.id ? Number(params.id) : undefined
  const existing = useSchedule(id)
  const projects = useProjects()
  if (projects.isPending || (id !== undefined && existing.isPending)) return <PageSpinner />
  if (id !== undefined && existing.isError) return <p className="py-10 text-center text-sm text-muted-foreground">{errorMessage(existing.error) || sc.notFound}</p>
  const initial = existing.data ? toValues(existing.data) : emptyValues(projects.data?.[0]?.id ?? '')
  return <Editor key={id ?? 'new'} id={id} initial={initial} projects={projects.data ?? []} />
}

type Errors = Partial<Record<'name' | 'prompt' | 'cron' | 'run_budget' | 'monthly_budget' | 'loop', string>>

function validate(v: ScheduleValues): Errors {
  const e: Errors = {}
  if (!v.name.trim()) e.name = sc.nameRequired
  if (!v.prompt.trim()) e.prompt = sc.promptRequired
  if (v.preset === 'custom' && !v.cron.trim()) e.cron = sc.cronRequired
  if (parseMoney(v.run_budget) === undefined) e.run_budget = sc.invalidBudget
  if (parseMoney(v.monthly_budget) === undefined) e.monthly_budget = sc.invalidBudget
  if (v.loop.enabled && !v.loop.check.trim()) e.loop = lp.checkRequired
  return e
}

function Editor({ id, initial, projects }: { id?: number; initial: ScheduleValues; projects: Project[] }) {
  const navigate = useNavigate()
  const [v, setV] = useState(initial)
  const [errors, setErrors] = useState<Errors>({})
  const set = (patch: Partial<ScheduleValues>) => setV((cur) => ({ ...cur, ...patch }))
  const create = useCreateSchedule()
  const update = useUpdateSchedule(id ?? 0)
  const project = projects.find((p) => p.id === v.project_id)
  const agents = useAgents()
  const integrations = useIntegrations(v.project_id || undefined)
  const skills = useSkills()
  const tzs = useMemo(() => zones(v.timezone), [v.timezone])
  const saving = create.isPending || update.isPending

  async function submit() {
    const e = validate(v)
    setErrors(e)
    if (Object.keys(e).length) {
      haptic.error()
      return
    }
    try {
      const body = toInput(v)
      const saved = id === undefined ? await create.mutateAsync(body) : await update.mutateAsync(body)
      haptic.success()
      toast.success(id === undefined ? sc.created : sc.saved)
      void navigate(`/schedules/${saved.id}`, { replace: true })
    } catch (err) {
      haptic.error()
      toast.error(errorMessage(err))
    }
  }

  const servers = integrations.data?.active ?? []
  const base = baseSkills(project, skills.data ?? [])
  const toggleServer = (name: string) => {
    const all = servers.map((s) => s.name)
    const cur = v.mcp_servers ?? all
    const next = cur.includes(name) ? cur.filter((n) => n !== name) : [...cur, name]
    set({ mcp_servers: next.length === all.length && all.every((n) => next.includes(n)) ? null : next })
  }

  return (
    <div className="h-full overflow-y-auto">
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
        className="mx-auto max-w-2xl space-y-5 px-4 pt-4 pb-6"
        noValidate
      >
        <h1 className="fm-strip flex min-h-7 items-center px-2 text-base">{id === undefined ? sc.newTitle : sc.editTitle}</h1>

        <div className="space-y-2">
          <Label htmlFor="sched-name">{sc.name}</Label>
          <Input id="sched-name" value={v.name} onChange={(e) => set({ name: e.target.value })} placeholder={sc.namePlaceholder} className="h-11" maxLength={80} aria-invalid={!!errors.name} />
          {errors.name && <p className="text-xs text-destructive">{errors.name}</p>}
        </div>

        <div className="grid gap-5 sm:grid-cols-2">
          <div className="space-y-2">
            <Label htmlFor="sched-project">{sc.project}</Label>
            <Select
              value={v.project_id}
              // A project change resets what belongs to the project.
              onValueChange={(project_id) => set({ project_id, repository_id: '', mcp_servers: null, skills: [] })}
            >
              <SelectTrigger id="sched-project" className="h-11 w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {projects.map((p) => (
                  <SelectItem key={p.id} value={p.id} className="h-10">
                    {p.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {(project?.repositories.length ?? 0) > 1 && (
            <div className="space-y-2">
              <Label htmlFor="sched-repo">{sc.repository}</Label>
              <Select value={v.repository_id || PROJECT} onValueChange={(r) => set({ repository_id: r === PROJECT ? '' : r })}>
                <SelectTrigger id="sched-repo" className="h-11 w-full font-mono text-sm">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={PROJECT} className="h-10">
                    {t.common.serverDefault}
                  </SelectItem>
                  {project?.repositories.map((r) => (
                    <SelectItem key={r.id} value={String(r.id)} className="h-10 font-mono text-sm">
                      {r.full_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          {(agents.data?.agents.length ?? 0) > 1 && (
            <div className="space-y-2">
              <Label htmlFor="sched-agent">{sc.agent}</Label>
              <Select value={v.agent || PROJECT} onValueChange={(a) => set({ agent: (a === PROJECT ? '' : a) as ScheduleValues['agent'] })}>
                <SelectTrigger id="sched-agent" className="h-11 w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value={PROJECT} className="h-10">
                    {sc.projectAgent}
                  </SelectItem>
                  {agents.data?.agents.map((a) => (
                    <SelectItem key={a} value={a} className="h-10">
                      {t.agents[a]}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
        </div>

        <div className="space-y-2">
          <Label htmlFor="sched-prompt">{sc.prompt}</Label>
          <Textarea id="sched-prompt" value={v.prompt} onChange={(e) => set({ prompt: e.target.value })} className="min-h-32 field-sizing-content" aria-invalid={!!errors.prompt} />
          {errors.prompt && <p className="text-xs text-destructive">{errors.prompt}</p>}
        </div>

        <fieldset className="space-y-3">
          <legend className="mb-2 text-sm font-medium">{sc.frequency}</legend>
          <div role="radiogroup" aria-label={sc.frequency} className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            {PRESETS.map((p) => (
              <button
                key={p}
                type="button"
                role="radio"
                aria-checked={v.preset === p}
                onClick={() => set({ preset: p, cron: p === 'custom' ? presetCron(v.preset, v.time, v.weekday, v.cron) : v.cron })}
                className={cn('fm-label min-h-11 border-2 px-2 text-[12px]', v.preset === p ? 'fm-selected border-ink font-semibold' : 'border-rule hover:bg-grid')}
              >
                {sc.presets[p]}
              </button>
            ))}
          </div>
          {v.preset === 'custom' ? (
            <div className="space-y-1.5">
              <Label htmlFor="sched-cron">{sc.cron}</Label>
              <Input id="sched-cron" value={v.cron} onChange={(e) => set({ cron: e.target.value })} className="h-11 font-mono" autoCapitalize="off" spellCheck={false} aria-invalid={!!errors.cron} />
              {errors.cron ? <p className="text-xs text-destructive">{errors.cron}</p> : <p className="text-xs text-muted-foreground">{sc.cronHint}</p>}
            </div>
          ) : (
            <div className="grid grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label htmlFor="sched-time">{sc.time}</Label>
                <Input id="sched-time" type="time" value={v.time} onChange={(e) => set({ time: e.target.value || '09:00' })} className="h-11 font-mono" />
              </div>
              {v.preset === 'weekly' && (
                <div className="space-y-1.5">
                  <Label htmlFor="sched-day">{sc.weekday}</Label>
                  <Select value={String(v.weekday)} onValueChange={(d) => set({ weekday: Number(d) })}>
                    <SelectTrigger id="sched-day" className="h-11 w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      {sc.days.map((d, i) => (
                        <SelectItem key={d} value={String(i)} className="h-10">
                          {d}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              )}
            </div>
          )}
          <div className="space-y-1.5">
            <Label htmlFor="sched-tz">{sc.timezone}</Label>
            <Select value={v.timezone} onValueChange={(timezone) => set({ timezone })}>
              <SelectTrigger id="sched-tz" className="h-11 w-full font-mono text-sm">
                <SelectValue />
              </SelectTrigger>
              <SelectContent className="max-h-72">
                {tzs.map((z) => (
                  <SelectItem key={z} value={z} className="h-10 font-mono text-sm">
                    {z}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
        </fieldset>

        {!!skills.data?.length && (
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">{sc.skills}</legend>
            <p className="text-xs text-muted-foreground">{sc.skillsHint}</p>
            <div className="flex flex-wrap gap-2">
              {skills.data.map((s) => {
                const fromProject = base.has(s.slug)
                const on = fromProject || v.skills.includes(s.slug)
                return (
                  <button
                    key={s.slug}
                    type="button"
                    aria-pressed={on}
                    disabled={fromProject}
                    onClick={() => set({ skills: on ? v.skills.filter((x) => x !== s.slug) : [...v.skills, s.slug] })}
                    className={cn('flex min-h-11 items-center gap-1.5 border px-2.5 font-mono text-xs', on ? 'border-ink bg-grid' : 'border-rule text-muted-foreground', fromProject && 'opacity-70')}
                  >
                    {on && <Check className="size-3.5" aria-hidden="true" />}
                    {s.slug}
                    {fromProject && <span className="text-[10px] uppercase">· {sc.fromProject}</span>}
                  </button>
                )
              })}
            </div>
          </fieldset>
        )}

        {servers.length > 0 && (
          <fieldset className="space-y-2">
            <legend className="text-sm font-medium">{sc.mcp}</legend>
            <p className="text-xs text-muted-foreground">{v.mcp_servers === null ? sc.mcpAll : sc.mcpHint}</p>
            <div className="flex flex-wrap gap-2">
              {servers.map((s) => {
                const on = v.mcp_servers === null || v.mcp_servers.includes(s.name)
                return (
                  <button
                    key={s.name}
                    type="button"
                    aria-pressed={on}
                    onClick={() => toggleServer(s.name)}
                    className={cn('flex min-h-11 items-center gap-1.5 border px-2.5 font-mono text-xs', on ? 'border-ink bg-grid' : 'border-rule text-muted-foreground')}
                  >
                    {on && <Check className="size-3.5" aria-hidden="true" />}
                    {s.name}
                  </button>
                )
              })}
            </div>
          </fieldset>
        )}

        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="sched-run-budget">{sc.runBudget}</Label>
            <Input id="sched-run-budget" value={v.run_budget} onChange={(e) => set({ run_budget: e.target.value })} inputMode="decimal" placeholder={sc.budgetPlaceholder} className="h-11 font-mono" aria-invalid={!!errors.run_budget} />
            {errors.run_budget && <p className="text-xs text-destructive">{errors.run_budget}</p>}
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="sched-month-budget">{sc.monthlyBudget}</Label>
            <Input id="sched-month-budget" value={v.monthly_budget} onChange={(e) => set({ monthly_budget: e.target.value })} inputMode="decimal" placeholder={sc.budgetPlaceholder} className="h-11 font-mono" aria-invalid={!!errors.monthly_budget} />
            {errors.monthly_budget && <p className="text-xs text-destructive">{errors.monthly_budget}</p>}
          </div>
        </div>

        <fieldset className="space-y-2">
          <legend className="mb-2 text-sm font-medium">{sc.concurrency}</legend>
          <div role="radiogroup" aria-label={sc.concurrency} className="grid grid-cols-2 gap-2">
            {(['skip', 'queue'] as const).map((c) => (
              <button
                key={c}
                type="button"
                role="radio"
                aria-checked={v.concurrency === c}
                onClick={() => set({ concurrency: c })}
                className={cn('min-h-11 border-2 px-2 text-sm', v.concurrency === c ? 'fm-selected border-ink font-semibold' : 'border-rule hover:bg-grid')}
              >
                {c === 'skip' ? sc.skip : sc.queue}
              </button>
            ))}
          </div>
        </fieldset>

        <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3">
          <span className="text-sm font-medium">{sc.prOnly}</span>
          <Switch checked={v.pr_only_if_changes} onCheckedChange={(pr_only_if_changes) => set({ pr_only_if_changes })} aria-label={sc.prOnly} />
        </label>

        <LoopFields idPrefix="sched-loop" value={v.loop} onChange={(loop) => set({ loop })} error={errors.loop} />

        <div className="grid grid-cols-2 gap-3">
          <div className="space-y-1.5">
            <Label htmlFor="sched-ends">{sc.endDate}</Label>
            <Input id="sched-ends" type="date" value={v.ends_on} onChange={(e) => set({ ends_on: e.target.value })} className="h-11 font-mono" aria-describedby="sched-ends-hint" />
            <p id="sched-ends-hint" className="text-xs text-muted-foreground">
              {sc.endDateHint}
            </p>
          </div>
          <label className="flex min-h-11 items-center justify-between gap-3 self-start fm-group px-3 sm:mt-6">
            <span className="text-sm font-medium">{sc.enabled}</span>
            <Switch checked={v.enabled} onCheckedChange={(enabled) => set({ enabled })} aria-label={sc.enabled} />
          </label>
        </div>

        <PrimaryAction label={id === undefined ? sc.create : sc.save} onClick={() => void submit()} loading={saving} />
      </form>
    </div>
  )
}
