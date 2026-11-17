import { Link } from 'react-router'
import { ChevronRight } from 'lucide-react'
import { useSkills } from '@/api/skills'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { sk } from '@/i18n/skills'

/**
 * Project → Skills: which skills the project's tasks get (saved with the form
 * as PATCH `skills`). Default skills are on for every project, so their switch
 * is on and locked. Output is PR only: the API refuses the diagram outputs
 * until the server collects them, so there is nothing to choose yet.
 */
export function ProjectSkillsSection({ value, onChange }: { value: string[]; onChange: (v: string[]) => void }) {
  const skills = useSkills()
  const toggle = (slug: string, on: boolean) => onChange(on ? [...value.filter((s) => s !== slug), slug] : value.filter((s) => s !== slug))

  return (
    <section className="space-y-4" aria-labelledby="project-skills">
      <h2 id="project-skills" className="text-sm font-bold">
        {sk.projectTitle}
      </h2>
      <p className="-mt-2 text-xs text-muted-foreground">{sk.projectHint}</p>
      {skills.isPending ? (
        <Skeleton className="h-24" />
      ) : !skills.data?.length ? (
        <p className="fm-group px-3 py-2.5 text-sm text-muted-foreground">{sk.projectEmpty}</p>
      ) : (
        <ul className="divide-y fm-group">
          {skills.data.map((s) => {
            const locked = s.enabled_by_default
            return (
              <li key={s.id}>
                <label className="flex min-h-14 items-center gap-3 px-3 py-2">
                  <span className="min-w-0 flex-1">
                    <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="font-mono text-sm font-medium">{s.slug}</span>
                      {locked && (
                        <span className="fm-stamp" data-tone="olive">
                          {sk.alwaysOn}
                        </span>
                      )}
                    </span>
                    <span className="mt-0.5 line-clamp-2 block text-xs text-muted-foreground">{s.description}</span>
                  </span>
                  <Switch
                    aria-label={s.slug}
                    checked={locked || value.includes(s.slug)}
                    disabled={locked}
                    onCheckedChange={(on) => toggle(s.slug, on)}
                  />
                </label>
              </li>
            )
          })}
        </ul>
      )}
      <div className="flex min-h-11 items-center justify-between gap-3 fm-group px-3 py-2">
        <span className="min-w-0">
          <span className="block text-sm font-medium">{sk.output}</span>
          <span className="block text-xs text-muted-foreground">{sk.outputHint}</span>
        </span>
        <span className="flex shrink-0 flex-col items-end gap-1">
          <span className="fm-stamp">{sk.outputPr}</span>
          <span className="fm-label text-[10px] text-muted-foreground">{sk.diagramsSoon}</span>
        </span>
      </div>
      <Link to="/skills" className="flex min-h-11 items-center gap-3 fm-group px-3 py-2 hover:bg-muted">
        <span className="min-w-0 flex-1 text-sm font-medium">{sk.manage}</span>
        <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
      </Link>
    </section>
  )
}
