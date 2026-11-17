import { Check } from 'lucide-react'
import { useSkills } from '@/api/skills'
import type { Project, Skill } from '@/api/types'
import { cn } from '@/lib/utils'
import { sk } from '@/i18n/skills'

/** The skills a project's task gets before overrides: the project's own plus the default ones. */
export function baseSkills(project: Pick<Project, 'skills'> | undefined, skills: Pick<Skill, 'slug' | 'enabled_by_default'>[]): Set<string> {
  return new Set([...(project?.skills ?? []), ...skills.filter((s) => s.enabled_by_default).map((s) => s.slug)])
}

/**
 * Toggles one skill for this task relative to the project's set: dropping a
 * base skill goes to skills_remove, adding another one to skills_add.
 */
export function toggleSkill(slug: string, base: Set<string>, add: string[], remove: string[]): { add: string[]; remove: string[] } {
  if (base.has(slug)) return remove.includes(slug) ? { add, remove: remove.filter((s) => s !== slug) } : { add, remove: [...remove, slug] }
  return add.includes(slug) ? { add: add.filter((s) => s !== slug), remove } : { add: [...add, slug], remove }
}

/** New task: the skills the task will get, as toggle chips (on = the task gets it). */
export function ComposerSkills({
  project,
  add,
  remove,
  onChange,
}: {
  project: Project | undefined
  add: string[]
  remove: string[]
  onChange: (v: { add: string[]; remove: string[] }) => void
}) {
  const skills = useSkills()
  if (!skills.data?.length) return null
  const base = baseSkills(project, skills.data)
  const on = (slug: string) => (base.has(slug) && !remove.includes(slug)) || add.includes(slug)
  const count = skills.data.filter((s) => on(s.slug)).length

  return (
    <fieldset className="space-y-2">
      <legend className="mb-2 flex w-full items-baseline justify-between gap-2 text-sm font-medium">
        <span>{sk.composerLabel}</span>
        <span className="text-xs font-normal text-muted-foreground">{count ? sk.composerHint : sk.composerNone}</span>
      </legend>
      <div className="flex flex-wrap gap-2">
        {skills.data.map((s) => {
          const active = on(s.slug)
          const changed = add.includes(s.slug) || remove.includes(s.slug)
          return (
            <button
              key={s.id}
              type="button"
              aria-pressed={active}
              title={changed ? `${s.description} (${active ? sk.added : sk.removed})` : s.description}
              onClick={() => onChange(toggleSkill(s.slug, base, add, remove))}
              className={cn(
                'fm-btn flex min-h-11 items-center gap-1.5 px-3 font-mono text-xs',
                active ? 'fm-pressed font-semibold' : 'text-muted-foreground',
              )}
            >
              {active && <Check className="size-3.5" aria-hidden="true" />}
              {s.slug}
            </button>
          )
        })}
      </div>
    </fieldset>
  )
}
