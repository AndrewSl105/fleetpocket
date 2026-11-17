import { http, HttpResponse } from 'msw'
import type { Idea, IdeaAttachment, IdeaColumn, IdeaInput, IdeaPatch, IdeaSettingsInput, Task } from '@/api/types'
import { addEvent, db } from './db'

// The Ideas board for mock mode (docs/API.md "Ideas board"). Off by default
// like the server; turning it on shows four seeded cards.

type Guard = (request: Request) => Response | null
type Err = (status: number, code: string, message: string) => Response

const DEFAULT_COLUMNS: IdeaColumn[] = [
  { id: 'inbox', name: 'Inbox' },
  { id: 'shaping', name: 'Shaping' },
  { id: 'ready', name: 'Ready' },
  { id: 'in_progress', name: 'In progress' },
  { id: 'done', name: 'Done' },
]
const FIXED = DEFAULT_COLUMNS.map((c) => c.id)

export const ideaState = {
  settings: { enabled: false, columns: DEFAULT_COLUMNS },
  ideas: [] as Idea[],
  files: new Map<number, Blob>(),
  nextId: 1,
  nextAttachment: 1,
  seeded: false,
}

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString()

/** The server's title rule: the first sentence, at most eight words. */
export function titleFrom(text: string): string {
  const first = text.trim().split('\n')[0].split(/(?<=[.!?…])\s/)[0].replace(/[.!?…]$/, '')
  const words = first.split(/\s+/).filter(Boolean)
  return words.slice(0, 8).join(' ') + (words.length > 8 ? '…' : '')
}

function attachment(fields: Partial<IdeaAttachment> & Pick<IdeaAttachment, 'kind' | 'name'>): IdeaAttachment {
  return { id: ideaState.nextAttachment++, mime: '', size: 0, width: 0, height: 0, duration_ms: null, transcript: null, url: null, title: null, has_thumb: false, created_at: new Date().toISOString(), ...fields }
}

function topOf(column: string): number {
  const ps = ideaState.ideas.filter((i) => i.column === column && !i.archived_at).map((i) => i.position)
  return ps.length ? Math.min(...ps) - 1024 : 0
}

function make(fields: Partial<Idea> & Pick<Idea, 'title'>): Idea {
  const at = fields.created_at ?? new Date().toISOString()
  const column = fields.column ?? 'inbox'
  const idea: Idea = {
    id: ideaState.nextId++,
    project_id: null,
    body: '',
    column,
    position: topOf(column),
    tags: [],
    auto_note: null,
    source: 'web',
    archived_at: null,
    created_at: at,
    updated_at: at,
    attachments: [],
    voice_notes: [],
    linked_task_ids: [],
    linked_tasks: [],
    failed: false,
    ...fields,
  }
  ideaState.ideas.push(idea)
  return idea
}

function seed() {
  if (ideaState.seeded) return
  ideaState.seeded = true
  make({ title: 'Keyboard shortcuts for the canvas', body: 'J/K to move between windows, Enter to open the task.', tags: ['ux'], created_at: ago(30) })
  make({
    title: 'Weekly cost digest in Telegram',
    body: 'Every Monday: spend per project and the most expensive task.',
    column: 'shaping',
    tags: ['telegram', 'costs'],
    source: 'telegram',
    voice_notes: [attachment({ kind: 'voice', name: 'voice.ogg', mime: 'audio/ogg', size: 2048, duration_ms: 7000, transcript: 'Every Monday: spend per project and the most expensive task.' })],
    created_at: ago(120),
  })
  const failed = db.tasks.find((t) => t.title === 'Upgrade to React 19')
  make({
    title: 'Upgrade to React 19',
    body: 'The new compiler should make the timeline cheaper.',
    column: 'in_progress',
    project_id: 'bioma',
    tags: ['deps'],
    auto_note: failed ? `Moved to In progress: task #${failed.id} started from it` : null,
    ...(failed ? { linked_task_ids: [failed.id] } : {}),
    attachments: [attachment({ kind: 'link', name: 'react.dev', url: 'https://react.dev/blog', title: 'React Blog' })],
    created_at: ago(60 * 26),
  })
  make({ title: 'Dark mode for the landing page', column: 'done', auto_note: 'Moved to Done: task #3, PR merged', created_at: ago(60 * 50) })
}

/** An idea as the API returns it: linked tasks and the ✕ stamp from the tasks' current state. */
function view(i: Idea): Idea {
  const tasks = i.linked_task_ids.map((id) => db.tasks.find((t) => t.id === id)).filter((t): t is Task => !!t)
  const last = tasks[tasks.length - 1]
  return { ...i, linked_tasks: tasks.map((t) => ({ id: t.id, title: t.title, status: t.status, pr_url: t.pr_url ?? null, pr_number: t.pr_number ?? null })), failed: last?.status === 'failed' }
}

function sorted(list: Idea[]): Idea[] {
  const order = new Map(ideaState.settings.columns.map((c, n) => [c.id, n]))
  return [...list].sort((a, b) => (order.get(a.column) ?? 99) - (order.get(b.column) ?? 99) || a.position - b.position || a.id - b.id)
}

function moveTo(i: Idea, column: string, afterId: number | null): Idea | string {
  const rest = sorted(ideaState.ideas.filter((x) => x.column === column && !x.archived_at && x.id !== i.id))
  let pos: number
  if (afterId === null) pos = rest.length ? rest[0].position - 1024 : 0
  else {
    const k = rest.findIndex((x) => x.id === afterId)
    if (k < 0) return 'after_id is not a card of that column'
    pos = k + 1 < rest.length ? (rest[k].position + rest[k + 1].position) / 2 : rest[k].position + 1024
  }
  Object.assign(i, { column, position: pos, auto_note: null, updated_at: new Date().toISOString() })
  return i
}

/** POST /tasks with idea_id: link, move to In progress, `idea` event on the task. */
export function linkIdeaTask(ideaId: number, task: Task): string | null {
  seed()
  const i = ideaState.ideas.find((x) => x.id === ideaId)
  if (!ideaState.settings.enabled) return 'ideas_disabled'
  if (!i) return 'not_found'
  i.linked_task_ids = [...i.linked_task_ids, task.id]
  const name = ideaState.settings.columns.find((c) => c.id === 'in_progress')?.name ?? 'In progress'
  const moved = i.column !== 'in_progress'
  if (moved) Object.assign(i, { column: 'in_progress', position: topOf('in_progress') })
  i.auto_note = `Moved to ${name}: task #${task.id} started from it`
  addEvent(task.id, 'idea', { idea_id: i.id, title: i.title, action: 'started', column: 'in_progress', column_name: name, moved, reason: `task #${task.id} started from it` })
  return null
}

export function ideasEnabled(): boolean {
  return ideaState.settings.enabled
}

const FAKE_TRANSCRIPT = 'Rewrite the onboarding flow. It is too long and people drop off before the first task.'

export function ideaHandlers(API: string, guard: Guard, err: Err) {
  const off = () => (ideaState.settings.enabled ? null : err(409, 'ideas_disabled', 'the ideas board is off; turn it on in Settings'))
  const find = (id: unknown) => ideaState.ideas.find((i) => i.id === Number(id))
  const json = (i: Idea, status = 200) => HttpResponse.json(view(i), { status })
  const withGuards = (request: Request) => guard(request) ?? off()

  return [
    http.get(`${API}/settings/ideas`, ({ request }) => guard(request) ?? HttpResponse.json(ideaState.settings)),
    http.put(`${API}/settings/ideas`, async ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const body = (await request.json()) as IdeaSettingsInput
      if (body.columns) {
        if (body.columns.length > 8) return err(400, 'invalid', 'at most 8 columns')
        const ids = body.columns.map((c) => c.id).filter(Boolean)
        if (FIXED.some((f) => !ids.includes(f))) return err(400, 'invalid', 'the default columns cannot be removed (rename them instead)')
        const gone = ideaState.settings.columns.filter((c) => !ids.includes(c.id))
        if (gone.some((c) => ideaState.ideas.some((i) => i.column === c.id))) return err(409, 'conflict', 'that column still has cards; move or delete them first')
        ideaState.settings.columns = body.columns.map((c) => ({ id: c.id ?? `c${Math.random().toString(16).slice(2, 10)}`, name: c.name.trim() }))
      }
      ideaState.settings.enabled = body.enabled
      if (body.enabled) seed()
      return HttpResponse.json(ideaState.settings)
    }),
    http.get(`${API}/ideas`, ({ request }) => {
      const denied = withGuards(request)
      if (denied) return denied
      seed()
      const q = new URL(request.url).searchParams
      const text = (q.get('q') ?? '').toLowerCase()
      const archived = q.get('archived') === 'true'
      const items = ideaState.ideas.filter(
        (i) =>
          !!i.archived_at === archived &&
          (!q.get('project') || i.project_id === q.get('project')) &&
          (!q.get('tag') || i.tags.includes((q.get('tag') ?? '').toLowerCase())) &&
          (!text || `${i.title} ${i.body} ${i.voice_notes.map((v) => v.transcript).join(' ')}`.toLowerCase().includes(text)),
      )
      return HttpResponse.json({ items: (archived ? items.reverse() : sorted(items)).map(view) })
    }),
    http.post(`${API}/ideas`, async ({ request }) => {
      const denied = withGuards(request)
      if (denied) return denied
      let body: IdeaInput
      const files: File[] = []
      if ((request.headers.get('Content-Type') ?? '').startsWith('multipart/form-data')) {
        const form = await request.formData()
        const s = (k: string) => (form.get(k) as string | null) ?? undefined
        body = { title: s('title'), body: s('body'), project_id: s('project_id') ?? null, column: s('column'), tags: s('tags')?.split(','), links: s('links')?.split('\n') }
        for (const f of form.getAll('files[]')) if (typeof f !== 'string') files.push(f)
      } else body = (await request.json()) as IdeaInput
      const column = body.column || 'inbox'
      if (!ideaState.settings.columns.some((c) => c.id === column)) return err(400, 'invalid', `unknown column "${column}"`)
      const links = (body.links ?? []).map((l) => l.trim()).filter(Boolean)
      for (const l of links) if (!/^https?:\/\/\S+$/.test(l)) return err(400, 'invalid', 'the link must be an http or https URL')
      const text = (body.body ?? '').trim()
      const atts = [
        ...files.map((f) => {
          const a = attachment({ kind: 'file', name: f.name || 'file', mime: f.type || 'application/octet-stream', size: f.size, has_thumb: f.type.startsWith('image/') })
          ideaState.files.set(a.id, f)
          return a
        }),
        ...links.map((u) => attachment({ kind: 'link', name: new URL(u).host, url: u, title: new URL(u).host })),
      ]
      const title = (body.title ?? '').trim() || titleFrom(text) || atts[0]?.title || atts[0]?.name
      if (!title) return err(400, 'invalid', 'an idea needs a title, text, a file, a link or a voice note')
      const i = make({ title, body: text, column, project_id: body.project_id || null, tags: (body.tags ?? []).map((t) => t.trim().toLowerCase()).filter(Boolean), attachments: atts })
      return json(i, 201)
    }),
    http.post(`${API}/ideas/voice`, async ({ request }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const form = await request.formData()
      const audio = form.get('audio')
      if (!audio || typeof audio === 'string') return err(400, 'invalid', 'the audio part is missing')
      const v = attachment({ kind: 'voice', name: audio.name, mime: audio.type.split(';')[0] || 'audio/webm', size: audio.size, duration_ms: Number(form.get('duration_ms') ?? 0), transcript: FAKE_TRANSCRIPT })
      ideaState.files.set(v.id, audio)
      const project = form.get('project_id')
      const i = make({ title: titleFrom(FAKE_TRANSCRIPT), body: FAKE_TRANSCRIPT, voice_notes: [v], project_id: typeof project === 'string' && project ? project : null })
      return json(i, 201)
    }),
    http.get(`${API}/ideas/:id`, ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      seed()
      const i = find(params.id)
      return i ? json(i) : err(404, 'not_found', 'not found')
    }),
    http.patch(`${API}/ideas/:id`, async ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      const p = (await request.json()) as IdeaPatch
      if (p.title !== undefined) {
        if (!p.title.trim()) return err(400, 'invalid', 'the title cannot be empty')
        i.title = p.title.trim()
      }
      if (p.body !== undefined) i.body = p.body.trim()
      if (p.tags !== undefined) i.tags = p.tags.map((t) => t.trim().toLowerCase()).filter(Boolean)
      if (p.project_id !== undefined) i.project_id = p.project_id || null
      i.updated_at = new Date().toISOString()
      return json(i)
    }),
    http.delete(`${API}/ideas/:id`, ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      if (!i.archived_at) return err(409, 'conflict', 'archive the idea before deleting it')
      ideaState.ideas = ideaState.ideas.filter((x) => x.id !== i.id)
      return new HttpResponse(null, { status: 204 })
    }),
    http.post(`${API}/ideas/:id/move`, async ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      const b = (await request.json()) as { column: string; after_id?: number | null }
      if (!ideaState.settings.columns.some((c) => c.id === b.column)) return err(400, 'invalid', `unknown column "${b.column}"`)
      const r = moveTo(i, b.column, b.after_id ?? null)
      return typeof r === 'string' ? err(404, 'not_found', r) : json(r)
    }),
    http.post(`${API}/ideas/:id/archive`, ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      i.archived_at = new Date().toISOString()
      return json(i)
    }),
    http.post(`${API}/ideas/:id/unarchive`, ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      Object.assign(i, { archived_at: null, position: topOf(i.column) })
      return json(i)
    }),
    http.post(`${API}/ideas/:id/attachments`, async ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      const form = await request.formData()
      for (const f of form.getAll('files[]')) {
        if (typeof f === 'string') continue
        const a = attachment({ kind: 'file', name: f.name, mime: f.type, size: f.size, has_thumb: f.type.startsWith('image/') })
        ideaState.files.set(a.id, f)
        i.attachments = [...i.attachments, a]
      }
      return json(i)
    }),
    http.post(`${API}/ideas/:id/voice`, async ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      const form = await request.formData()
      const audio = form.get('audio')
      if (!audio || typeof audio === 'string') return err(400, 'invalid', 'the audio part is missing')
      const v = attachment({ kind: 'voice', name: audio.name, mime: audio.type.split(';')[0], size: audio.size, duration_ms: Number(form.get('duration_ms') ?? 0), transcript: FAKE_TRANSCRIPT })
      ideaState.files.set(v.id, audio)
      i.voice_notes = [...i.voice_notes, v]
      i.body = i.body ? `${i.body}\n\n${FAKE_TRANSCRIPT}` : FAKE_TRANSCRIPT
      return json(i)
    }),
    http.post(`${API}/ideas/:id/links`, async ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      const { url } = (await request.json()) as { url: string }
      if (!/^https?:\/\/\S+$/.test(url)) return err(400, 'invalid', 'the link must be an http or https URL')
      i.attachments = [...i.attachments, attachment({ kind: 'link', name: new URL(url).host, url, title: new URL(url).host })]
      return json(i)
    }),
    http.get(`${API}/ideas/:id/attachments/:aid`, ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const blob = ideaState.files.get(Number(params.aid))
      return blob ? new HttpResponse(blob, { headers: { 'Content-Type': blob.type || 'application/octet-stream' } }) : err(404, 'not_found', 'no such file')
    }),
    http.delete(`${API}/ideas/:id/attachments/:aid`, ({ request, params }) => {
      const denied = withGuards(request)
      if (denied) return denied
      const i = find(params.id)
      if (!i) return err(404, 'not_found', 'not found')
      const aid = Number(params.aid)
      i.attachments = i.attachments.filter((a) => a.id !== aid)
      i.voice_notes = i.voice_notes.filter((a) => a.id !== aid)
      ideaState.files.delete(aid)
      return json(i)
    }),
  ]
}
