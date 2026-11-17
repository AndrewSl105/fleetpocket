import { useState, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ArrowLeft, Copy, Download, Plus, Trash2, TriangleAlert, X } from 'lucide-react'
import { toast } from 'sonner'
import { skillError, useCreateSkill, useDeleteSkill, useDuplicateSkill, useExportSkill, useSkill, useUpdateSkill } from '@/api/skills'
import { isApiError } from '@/api/errors'
import type { Skill, SkillFile } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { PageSpinner } from '@/components/PageSpinner'
import { useConfirm } from '@/components/confirm'
import { NotFound } from '@/routes/NotFound'
import { PrimaryAction } from '@/telegram/controls'
import { haptic } from '@/telegram/haptics'
import { t } from '@/i18n/en'
import { sk } from '@/i18n/skills'
import { badPath, buildSkillMd, newSkillDoc, parseSkillMd, SLUG_RE } from './skillDoc'

function Field({ id, label, hint, error, children }: { id: string; label: string; hint?: string; error?: string; children: ReactNode }) {
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>{label}</Label>
      {children}
      {hint && !error && <p className="text-xs text-muted-foreground">{hint}</p>}
      {error && <p className="text-xs text-destructive">{error}</p>}
    </div>
  )
}

/** /skills/new and /skills/:id. */
export default function SkillEditPage() {
  const { id } = useParams()
  const skillId = id ? Number(id) : undefined
  const skill = useSkill(skillId)
  if (skillId === undefined) return <SkillForm />
  if (skill.isPending) return <PageSpinner />
  if (skill.isError) return <NotFound title={isApiError(skill.error, 'not_found') ? sk.notFound : t.common.somethingWrong} />
  return <SkillForm key={skill.data.updated_at} skill={skill.data} />
}

type Errors = Partial<Record<'slug' | 'name' | 'description', string>> & { files?: Record<number, string> }

function SkillForm({ skill }: { skill?: Skill }) {
  const navigate = useNavigate()
  const confirm = useConfirm()
  const creating = !skill
  const create = useCreateSkill()
  const update = useUpdateSkill(skill?.id ?? 0)
  const del = useDeleteSkill()
  const dup = useDuplicateSkill()
  const exp = useExportSkill()

  const [doc, setDoc] = useState(() => (skill ? parseSkillMd(skill.body) : newSkillDoc()))
  const [slug, setSlug] = useState(skill?.slug ?? '')
  const [files, setFiles] = useState<SkillFile[]>(() => skill?.files.map((f) => ({ ...f })) ?? [])
  const [byDefault, setByDefault] = useState(skill?.enabled_by_default ?? false)
  const [errors, setErrors] = useState<Errors>({})
  const [refused, setRefused] = useState<string | null>(null)
  const busy = create.isPending || update.isPending

  function check(): Errors {
    const e: Errors = {}
    if (!SLUG_RE.test(slug.trim())) e.slug = sk.slugInvalid
    if (!doc.description.trim()) e.description = sk.required
    const seen = new Set<string>()
    const fe: Record<number, string> = {}
    files.forEach((f, i) => {
      const p = f.path.trim()
      if (badPath(p) || seen.has(p)) fe[i] = sk.pathInvalid
      seen.add(p)
    })
    if (Object.keys(fe).length) e.files = fe
    return e
  }

  async function save() {
    const e = check()
    setErrors(e)
    setRefused(null)
    if (Object.keys(e).length) {
      haptic.error()
      return
    }
    // The name defaults to the slug (the server needs one in the frontmatter).
    const body = buildSkillMd({ ...doc, name: doc.name.trim() || slug.trim(), description: doc.description.trim() })
    const input = { slug: slug.trim(), body, files: files.map((f) => ({ path: f.path.trim(), content: f.content })), enabled_by_default: byDefault }
    try {
      if (creating) {
        const s = await create.mutateAsync(input)
        toast.success(sk.created)
        void navigate(`/skills/${s.id}`, { replace: true })
      } else {
        await update.mutateAsync(input)
        toast.success(sk.saved)
      }
      haptic.success()
    } catch (err) {
      haptic.error()
      // Validation and secrets-scan findings stay on screen next to Save.
      setRefused(skillError(err))
    }
  }

  async function remove() {
    if (!skill || !(await confirm(sk.deleteConfirm(skill.slug, skill.projects.length), sk.delete))) return
    del.mutate(skill.id, {
      onSuccess: () => {
        toast.success(sk.deleted(skill.slug))
        void navigate('/skills', { replace: true })
      },
      onError: (e) => toast.error(skillError(e)),
    })
  }

  const setFile = (i: number, patch: Partial<SkillFile>) => setFiles((fs) => fs.map((f, j) => (j === i ? { ...f, ...patch } : f)))

  return (
    <div className="h-full overflow-y-auto">
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void save()
        }}
        className="mx-auto max-w-2xl space-y-6 px-4 pt-3 pb-8"
      >
        <header className="flex items-center gap-2">
          <Button asChild variant="ghost" size="icon" className="size-11 shrink-0">
            <Link to="/skills" aria-label={sk.back}>
              <ArrowLeft />
            </Link>
          </Button>
          <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">
            <span className="truncate">{creating ? sk.newSkill : sk.editTitle(skill.slug)}</span>
          </h1>
        </header>

        <Field id="slug" label={sk.slug} hint={sk.slugHint} error={errors.slug}>
          <Input id="slug" value={slug} onChange={(e) => setSlug(e.target.value)} autoCapitalize="none" autoCorrect="off" spellCheck={false} className="h-11 font-mono" />
        </Field>
        <Field id="name" label={sk.name} hint={sk.nameHint} error={errors.name}>
          <Input id="name" value={doc.name} placeholder={slug || undefined} onChange={(e) => setDoc({ ...doc, name: e.target.value })} className="h-11" />
        </Field>
        <Field id="description" label={sk.description} hint={sk.descriptionHint} error={errors.description}>
          <Textarea id="description" value={doc.description} onChange={(e) => setDoc({ ...doc, description: e.target.value })} className="min-h-20 field-sizing-content" />
        </Field>
        <Field id="body" label={sk.body} hint={doc.extra.length ? `${sk.bodyHint} ${sk.extraFrontmatter(doc.extra.length)}.` : sk.bodyHint}>
          <Textarea
            id="body"
            value={doc.instructions}
            onChange={(e) => setDoc({ ...doc, instructions: e.target.value })}
            spellCheck={false}
            className="min-h-64 field-sizing-content font-mono text-[13px] leading-relaxed"
          />
        </Field>

        <section className="space-y-3">
          <h2 className="text-sm font-bold">{sk.extraFiles}</h2>
          <p className="-mt-1 text-xs text-muted-foreground">{sk.extraFilesHint}</p>
          {files.length === 0 ? (
            <p className="fm-group px-3 py-2.5 text-sm text-muted-foreground">{sk.noFiles}</p>
          ) : (
            <ul className="space-y-3">
              {files.map((f, i) => (
                <li key={i} className="space-y-2 fm-group p-3">
                  <div className="flex items-start gap-2">
                    <div className="min-w-0 flex-1">
                      <Input
                        aria-label={`${sk.path} ${i + 1}`}
                        value={f.path}
                        placeholder={sk.pathPlaceholder}
                        onChange={(e) => setFile(i, { path: e.target.value })}
                        autoCapitalize="none"
                        spellCheck={false}
                        className="h-11 font-mono"
                      />
                      {errors.files?.[i] && <p className="mt-1 text-xs text-destructive">{errors.files[i]}</p>}
                    </div>
                    <Button type="button" variant="ghost" size="icon" className="size-11" aria-label={sk.removeFile(f.path)} onClick={() => setFiles((fs) => fs.filter((_, j) => j !== i))}>
                      <X />
                    </Button>
                  </div>
                  <Textarea
                    aria-label={`${sk.content} ${f.path || i + 1}`}
                    value={f.content}
                    onChange={(e) => setFile(i, { content: e.target.value })}
                    spellCheck={false}
                    className="min-h-28 field-sizing-content font-mono text-[13px]"
                  />
                </li>
              ))}
            </ul>
          )}
          <Button type="button" variant="outline" className="h-10" onClick={() => setFiles((fs) => [...fs, { path: '', content: '' }])}>
            <Plus /> {sk.addFile}
          </Button>
        </section>

        <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3 py-2">
          <span className="min-w-0">
            <span className="block text-sm font-medium">{sk.enabledByDefault}</span>
            <span className="block text-xs text-muted-foreground">{sk.enabledByDefaultHint}</span>
          </span>
          <Switch checked={byDefault} onCheckedChange={setByDefault} />
        </label>

        {refused && (
          <div role="alert" className="flex gap-2 border-2 border-danger px-3 py-2.5 text-sm">
            <TriangleAlert className="mt-0.5 size-4 shrink-0 text-danger" aria-hidden="true" />
            <span className="min-w-0 break-words">
              <span className="font-semibold">{sk.refused}: </span>
              <span className="font-mono text-xs">{refused}</span>
            </span>
          </div>
        )}

        <PrimaryAction label={creating ? sk.create : sk.save} onClick={() => void save()} loading={busy} />

        {skill && (
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              variant="outline"
              className="h-11"
              disabled={dup.isPending}
              onClick={() =>
                dup.mutate(skill.id, {
                  onSuccess: (c) => {
                    toast.success(sk.duplicated(c.slug))
                    void navigate(`/skills/${c.id}`)
                  },
                  onError: (e) => toast.error(skillError(e)),
                })
              }
            >
              <Copy /> {sk.duplicate}
            </Button>
            <Button type="button" variant="outline" className="h-11" disabled={exp.isPending} onClick={() => exp.mutate(skill, { onError: (e) => toast.error(skillError(e)) })}>
              <Download /> {sk.export}
            </Button>
            <Button type="button" variant="outline" className="h-11 text-destructive" disabled={del.isPending} onClick={() => void remove()}>
              <Trash2 /> {sk.delete}
            </Button>
          </div>
        )}
      </form>
    </div>
  )
}
