// Ideas board hooks (docs/API.md "Ideas board"). A module of their own so
// they load with the lazy screens that use them, not in the initial bundle.
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, CSRF, uploadForm } from './client'
import { keys } from './keys'
import type { Idea, IdeaAttachment, IdeaInput, IdeaPatch, IdeaSettings, IdeaSettingsInput } from './types'

export type IdeaFilter = { archived?: boolean; project?: string; tag?: string; q?: string }

export const ideaKeys = {
  all: ['ideas'] as const,
  settings: ['settings', 'ideas'] as const,
  list: (f: IdeaFilter) => ['ideas', 'list', f] as const,
  detail: (id: number) => ['ideas', 'detail', id] as const,
  file: (id: number, attachmentId: number, thumb: boolean) => ['ideas', 'file', id, attachmentId, thumb] as const,
}

export function useIdeaSettings() {
  return useQuery({
    queryKey: ideaKeys.settings,
    queryFn: ({ signal }) => call(api.GET('/api/v1/settings/ideas', { signal })),
  })
}

export function useSaveIdeaSettings() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: IdeaSettingsInput) => call(api.PUT('/api/v1/settings/ideas', { params: { header: CSRF }, body })),
    onSuccess: (s: IdeaSettings) => {
      qc.setQueryData(ideaKeys.settings, s)
      // /me carries ideas_enabled (the nav entry and the header button).
      void qc.invalidateQueries({ queryKey: keys.me })
      void qc.invalidateQueries({ queryKey: ideaKeys.all })
    },
  })
}

export function useIdeas(f: IdeaFilter, enabled = true) {
  return useQuery({
    queryKey: ideaKeys.list(f),
    queryFn: async ({ signal }) =>
      (
        await call(
          api.GET('/api/v1/ideas', {
            params: { query: { archived: f.archived || undefined, project: f.project || undefined, tag: f.tag || undefined, q: f.q || undefined } },
            signal,
          }),
        )
      ).items,
    enabled,
    staleTime: 5_000,
  })
}

export function useIdea(id: number) {
  return useQuery({
    queryKey: ideaKeys.detail(id),
    queryFn: ({ signal }) => call(api.GET('/api/v1/ideas/{id}', { params: { path: { id } }, signal })),
  })
}

/** After any change: the idea itself, then every list. */
export function useIdeaResult() {
  const qc = useQueryClient()
  return (idea?: Idea) => {
    if (idea) qc.setQueryData(ideaKeys.detail(idea.id), idea)
    void qc.invalidateQueries({ queryKey: ['ideas', 'list'] })
  }
}

function formOf(fields: Record<string, string | undefined>, files: File[] = []): FormData {
  const form = new FormData()
  for (const [k, v] of Object.entries(fields)) if (v !== undefined && v !== '') form.append(k, v)
  for (const f of files) form.append('files[]', f, f.name || 'file')
  return form
}

export function useCreateIdea() {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: ({ files = [], ...body }: IdeaInput & { files?: File[] }) =>
      files.length
        ? uploadForm<Idea>(
            '/api/v1/ideas',
            formOf(
              {
                title: body.title,
                body: body.body,
                project_id: body.project_id ?? undefined,
                column: body.column,
                tags: body.tags?.join(','),
                links: body.links?.join('\n'),
              },
              files,
            ),
          )
        : call(api.POST('/api/v1/ideas', { params: { header: CSRF }, body })),
    onSuccess: done,
  })
}

export type Recording = { blob: Blob; mime: string; durationMs: number }

function voiceForm(r: Recording, fields: Record<string, string | undefined> = {}): FormData {
  const form = formOf({ ...fields, duration_ms: String(Math.round(r.durationMs)) })
  const ext = r.mime.includes('mp4') ? 'm4a' : r.mime.includes('ogg') ? 'ogg' : 'webm'
  form.append('audio', new File([r.blob], `voice-note.${ext}`, { type: r.mime }))
  return form
}

/** A hold-to-talk recording becomes a new idea (transcribed on the server). */
export function useVoiceIdea() {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: ({ rec, column, project }: { rec: Recording; column?: string; project?: string }) =>
      uploadForm<Idea>('/api/v1/ideas/voice', voiceForm(rec, { column, project_id: project })),
    onSuccess: done,
  })
}

export function useAddIdeaVoice(id: number) {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: (rec: Recording) => uploadForm<Idea>(`/api/v1/ideas/${id}/voice`, voiceForm(rec)),
    onSuccess: done,
  })
}

export function useAddIdeaFiles(id: number) {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: (files: File[]) => uploadForm<Idea>(`/api/v1/ideas/${id}/attachments`, formOf({}, files)),
    onSuccess: done,
  })
}

export function useAddIdeaLink(id: number) {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: (url: string) => call(api.POST('/api/v1/ideas/{id}/links', { params: { path: { id }, header: CSRF }, body: { url } })),
    onSuccess: done,
  })
}

export function useRemoveIdeaAttachment(id: number) {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: (attachmentId: number) =>
      call(api.DELETE('/api/v1/ideas/{id}/attachments/{attachmentId}', { params: { path: { id, attachmentId }, header: CSRF } })),
    onSuccess: done,
  })
}

export function useUpdateIdea(id: number) {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: (body: IdeaPatch) => call(api.PATCH('/api/v1/ideas/{id}', { params: { path: { id }, header: CSRF }, body })),
    onSuccess: done,
  })
}

/**
 * Moves a card; the board list is updated optimistically (the card goes
 * right after `after_id`, or on top) and refetched afterwards.
 */
export function useMoveIdea(listKey: readonly unknown[]) {
  const qc = useQueryClient()
  const done = useIdeaResult()
  return useMutation({
    mutationFn: ({ id, column, afterId }: { id: number; column: string; afterId: number | null }) =>
      call(api.POST('/api/v1/ideas/{id}/move', { params: { path: { id }, header: CSRF }, body: { column, after_id: afterId } })),
    onMutate: async ({ id, column, afterId }) => {
      await qc.cancelQueries({ queryKey: listKey })
      const before = qc.getQueryData<Idea[]>(listKey)
      if (Array.isArray(before)) qc.setQueryData(listKey, placeCard(before, id, column, afterId))
      return { before }
    },
    onError: (_e, _v, ctx) => {
      if (ctx?.before) qc.setQueryData(listKey, ctx.before)
    },
    onSettled: (idea) => done(idea),
  })
}

/** The list with card `id` moved into `column` right after `afterId` (null: on top). */
export function placeCard(list: Idea[], id: number, column: string, afterId: number | null): Idea[] {
  const card = list.find((i) => i.id === id)
  if (!card) return list
  const rest = list.filter((i) => i.id !== id)
  const moved = { ...card, column, auto_note: null }
  const at = afterId === null ? rest.findIndex((i) => i.column === column) : rest.findIndex((i) => i.id === afterId) + 1
  if (at < 0 || (afterId !== null && at === 0)) return [...rest, moved]
  return [...rest.slice(0, at), moved, ...rest.slice(at)]
}

export function useArchiveIdea() {
  const done = useIdeaResult()
  return useMutation({
    mutationFn: ({ id, archived }: { id: number; archived: boolean }) =>
      archived
        ? call(api.POST('/api/v1/ideas/{id}/archive', { params: { path: { id }, header: CSRF } }))
        : call(api.POST('/api/v1/ideas/{id}/unarchive', { params: { path: { id }, header: CSRF } })),
    onSuccess: done,
  })
}

export function useDeleteIdea() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => call(api.DELETE('/api/v1/ideas/{id}', { params: { path: { id }, header: CSRF } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ideaKeys.all }),
  })
}

/** An idea's file (or thumbnail) as a Blob, through the API client (Telegram Web's bearer token). */
export function fetchIdeaFile(id: number, a: Pick<IdeaAttachment, 'id'>, thumb = false): Promise<Blob> {
  return call(
    api.GET('/api/v1/ideas/{id}/attachments/{attachmentId}', {
      params: { path: { id, attachmentId: a.id }, query: thumb ? { thumb: '1' } : {} },
      parseAs: 'blob',
    }),
  ) as Promise<Blob>
}

export function useIdeaFile(id: number, a: IdeaAttachment, thumb: boolean) {
  return useQuery({
    queryKey: ideaKeys.file(id, a.id, thumb),
    queryFn: () => fetchIdeaFile(id, a, thumb),
    staleTime: Infinity,
    gcTime: 5 * 60_000,
    retry: 1,
  })
}
