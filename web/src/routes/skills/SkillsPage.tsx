import { useRef } from 'react'
import { Link, useNavigate } from 'react-router'
import { Copy, Download, MoreVertical, Pencil, Plus, Trash2, Upload, Users } from 'lucide-react'
import { toast } from 'sonner'
import { skillError, useDeleteSkill, useDuplicateSkill, useExportSkill, useImportSkill, useSkills } from '@/api/skills'
import type { Skill } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { UserBadge } from '@/components/UserBadge'
import { usePromoteSkill, useRole } from '@/api/team'
import { useConfirm } from '@/components/confirm'
import { haptic } from '@/telegram/haptics'
import { sk } from '@/i18n/skills'

/** Skills: the library as a ruled list, with New, Import, and per skill Edit / Duplicate / Export / Delete. */
export default function SkillsPage() {
  const skills = useSkills()
  const imp = useImportSkill()
  const navigate = useNavigate()
  const fileRef = useRef<HTMLInputElement>(null)

  function onImport(file: File | undefined) {
    if (!file) return
    imp.mutate(
      { file },
      {
        onSuccess: (s) => {
          haptic.success()
          toast.success(sk.imported(s.slug))
          void navigate(`/skills/${s.id}`)
        },
        onError: (e) => {
          haptic.error()
          toast.error(skillError(e))
        },
      },
    )
  }

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl px-4 pt-4 pb-6">
        <div className="mb-3 flex items-center gap-2">
          <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">{sk.title}</h1>
          <Button variant="outline" className="h-10" disabled={imp.isPending} onClick={() => fileRef.current?.click()}>
            <Upload /> {imp.isPending ? sk.importing : sk.import}
          </Button>
          <input
            ref={fileRef}
            type="file"
            accept=".md,.zip,text/markdown,application/zip"
            className="hidden"
            aria-label={sk.import}
            data-testid="skill-import"
            onChange={(e) => {
              onImport(e.target.files?.[0])
              e.target.value = ''
            }}
          />
          <Button asChild className="h-10">
            <Link to="/skills/new">
              <Plus /> {sk.newSkill}
            </Link>
          </Button>
        </div>
        <p className="mb-4 text-xs text-muted-foreground">
          {sk.intro} {sk.importHint}
        </p>

        {skills.isPending ? (
          <div className="space-y-2">
            {[0, 1, 2].map((i) => (
              <Skeleton key={i} className="h-20" />
            ))}
          </div>
        ) : skills.isError ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{skillError(skills.error)}</p>
        ) : !skills.data.length ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{sk.empty}</p>
        ) : (
          <ul className="divide-y fm-field" aria-label={sk.title}>
            {skills.data.map((s) => (
              <SkillRow key={s.id} skill={s} />
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}

function SkillRow({ skill: s }: { skill: Skill }) {
  const confirm = useConfirm()
  const navigate = useNavigate()
  const dup = useDuplicateSkill()
  const del = useDeleteSkill()
  const exp = useExportSkill()
  // Team mode: admins promote a member's personal skill to the team.
  const role = useRole()
  const promote = usePromoteSkill()

  async function remove() {
    if (!(await confirm(sk.deleteConfirm(s.slug, s.projects.length), sk.delete))) return
    del.mutate(s.id, {
      onSuccess: () => toast.success(sk.deleted(s.slug)),
      onError: (e) => toast.error(skillError(e)),
    })
  }

  return (
    <li className="flex items-start gap-1">
      <Link to={`/skills/${s.id}`} className="block min-h-11 min-w-0 flex-1 px-3.5 py-3 hover:bg-accent/60">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
          {/* Seeded skills are named after their slug: then the name is shown once, in mono. */}
          <span className={s.slug === s.name ? 'font-mono text-sm font-semibold' : 'font-medium'}>{s.name}</span>
          {s.slug !== s.name && <span className="font-mono text-xs text-muted-foreground">{s.slug}</span>}
          {s.enabled_by_default && (
            <span className="fm-stamp" data-tone="olive">
              {sk.defaultStamp}
            </span>
          )}
          {s.personal && (
            <span className="fm-stamp" data-tone="plain">
              {sk.personalStamp}
            </span>
          )}
          <UserBadge user={s.created_by} />
        </div>
        <p className="mt-1 line-clamp-2 text-sm text-muted-foreground">{s.description}</p>
        <div className="mt-1 text-xs text-muted-foreground">
          {sk.files(s.files.length + 1)} · {s.enabled_by_default ? sk.allProjects : <span className="font-mono">{sk.usedBy(s.projects)}</span>}
        </div>
      </Link>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant="ghost" size="icon" className="mt-2 mr-1 size-11 shrink-0" aria-label={sk.actions(s.slug)}>
            <MoreVertical />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-44">
          <DropdownMenuItem className="h-10" onSelect={() => void navigate(`/skills/${s.id}`)}>
            <Pencil /> {sk.edit}
          </DropdownMenuItem>
          <DropdownMenuItem
            className="h-10"
            disabled={dup.isPending}
            onSelect={() =>
              dup.mutate(s.id, {
                onSuccess: (c) => toast.success(sk.duplicated(c.slug)),
                onError: (e) => toast.error(skillError(e)),
              })
            }
          >
            <Copy /> {sk.duplicate}
          </DropdownMenuItem>
          <DropdownMenuItem className="h-10" disabled={exp.isPending} onSelect={() => exp.mutate(s, { onError: (e) => toast.error(skillError(e) || sk.exportFailed) })}>
            <Download /> {sk.export}
          </DropdownMenuItem>
          {s.personal && role.isAdmin && (
            <DropdownMenuItem
              className="h-10"
              disabled={promote.isPending}
              onSelect={() => promote.mutate(s.id, { onSuccess: () => toast.success(sk.promoted(s.slug)), onError: (e) => toast.error(skillError(e)) })}
            >
              <Users /> {sk.promote}
            </DropdownMenuItem>
          )}
          <DropdownMenuSeparator />
          <DropdownMenuItem className="h-10 text-destructive" disabled={del.isPending} onSelect={() => void remove()}>
            <Trash2 /> {sk.delete}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </li>
  )
}
