// Skill library hooks (docs/API.md "Skills"). A module of their own so they
// load with the lazy screens that use them, not in the initial bundle.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, CSRF, uploadForm } from './client'
import { keys } from './keys'
import { isApiError } from './errors'
import { errorMessage } from './hooks'
import type { Skill, SkillInput } from './types'
import { sk } from '@/i18n/skills'

const skillKeys = {
  all: ['skills'] as const,
  list: ['skills', 'list'] as const,
  detail: (id: number) => ['skills', 'detail', id] as const,
}

/** All skills, by slug. Refetched on every mount: project edits change `projects`. */
export function useSkills() {
  return useQuery({
    queryKey: skillKeys.list,
    queryFn: async ({ signal }) => (await call(api.GET('/api/v1/skills', { signal }))).items,
    staleTime: 0,
  })
}

export function useSkill(id: number | undefined) {
  return useQuery({
    queryKey: skillKeys.detail(id ?? 0),
    queryFn: ({ signal }) => call(api.GET('/api/v1/skills/{id}', { params: { path: { id: id ?? 0 } }, signal })),
    enabled: id !== undefined,
  })
}

/** After any change: the list, the detail and the projects (delete and defaults change what they use). */
function useSkillResult() {
  const qc = useQueryClient()
  return (skill?: Skill) => {
    if (skill) qc.setQueryData(skillKeys.detail(skill.id), skill)
    void qc.invalidateQueries({ queryKey: skillKeys.list })
    void qc.invalidateQueries({ queryKey: keys.projects.all })
  }
}

/** The body PUT/POST accept: exactly SkillInput (additionalProperties: false). */
export function skillInput(v: SkillInput): SkillInput {
  return { slug: v.slug, body: v.body, files: v.files ?? [], enabled_by_default: v.enabled_by_default ?? false }
}

export function useCreateSkill() {
  const done = useSkillResult()
  return useMutation({
    mutationFn: (body: SkillInput) => call(api.POST('/api/v1/skills', { params: { header: CSRF }, body: skillInput(body) })),
    onSuccess: done,
  })
}

export function useUpdateSkill(id: number) {
  const done = useSkillResult()
  return useMutation({
    mutationFn: (body: SkillInput) =>
      call(api.PUT('/api/v1/skills/{id}', { params: { path: { id }, header: CSRF }, body: skillInput(body) })),
    onSuccess: done,
  })
}

export function useDeleteSkill() {
  const qc = useQueryClient()
  const done = useSkillResult()
  return useMutation({
    mutationFn: (id: number) => call(api.DELETE('/api/v1/skills/{id}', { params: { path: { id }, header: CSRF } })),
    onSuccess: (_d, id) => {
      qc.removeQueries({ queryKey: skillKeys.detail(id) })
      done()
    },
  })
}

export function useDuplicateSkill() {
  const done = useSkillResult()
  return useMutation({
    mutationFn: (id: number) => call(api.POST('/api/v1/skills/{id}/duplicate', { params: { path: { id }, header: CSRF } })),
    onSuccess: done,
  })
}

/** POST /skills/import: a SKILL.md or a .zip of a skill folder (multipart, through uploadForm for the CSRF header and bearer token). */
export function useImportSkill() {
  const done = useSkillResult()
  return useMutation({
    mutationFn: ({ file, slug }: { file: File; slug?: string }) => {
      const form = new FormData()
      form.append('file', file, file.name)
      if (slug?.trim()) form.append('slug', slug.trim())
      return uploadForm<Skill>('/api/v1/skills/import', form)
    },
    onSuccess: done,
  })
}

/**
 * GET /skills/{id}/export as a Blob through the API client (Telegram Web
 * authenticates with a bearer token, which a plain link cannot carry), then
 * saved as `<slug>.zip`.
 */
export function useExportSkill() {
  return useMutation({
    mutationFn: async (skill: Skill) => {
      const blob = (await call(api.GET('/api/v1/skills/{id}/export', { params: { path: { id: skill.id } }, parseAs: 'blob' }))) as Blob
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = `${skill.slug}.zip`
      document.body.appendChild(link)
      link.click()
      link.remove()
      setTimeout(() => URL.revokeObjectURL(url), 60_000)
    },
  })
}

/** The API's message for a refused skill: 409 is a taken slug, 400 carries the validation or secrets-scan finding. */
export function skillError(e: unknown): string {
  if (isApiError(e, 'conflict')) return sk.slugTaken
  if (isApiError(e, 'invalid')) return e.message || sk.invalid
  return errorMessage(e)
}
