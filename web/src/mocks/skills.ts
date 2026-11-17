import { http, HttpResponse } from 'msw'
import type { Skill, SkillFile } from '@/api/types'
import { db } from './db'

// The Skill library for mock mode (docs/API.md "Skills"). Projects keep the
// enabled slugs in `project.skills`, like the API returns them.

type Guard = (request: Request) => Response | null
type Err = (status: number, code: string, message: string) => Response

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString()

type Row = Omit<Skill, 'projects'>

export const skillState: { rows: Row[]; nextId: number } = { rows: [], nextId: 1 }

const fm = (name: string, description: string, body: string) => `---\nname: ${name}\ndescription: ${description}\n---\n${body}`

const SEED: Array<[string, string, string, SkillFile[], boolean]> = [
  [
    'db-schema',
    'Draw the database schema as a Mermaid ER diagram and DBML, from the live database when DATABASE_URL is set, otherwise from migrations and ORM models.',
    '# Database schema diagram\n\n## With a database (`DATABASE_URL` is set)\n\n1. Read the tables, columns, keys and indexes from `information_schema`.\n2. Write `.fleetpocket/out/schema.mmd` (Mermaid `erDiagram`) and `schema.dbml`.\n',
    [],
    false,
  ],
  [
    'infra-map',
    'Draw a Mermaid flowchart of services, networks, volumes, ports and external dependencies from docker-compose, Kubernetes and Terraform files.',
    '# Infrastructure map\n\n1. Read docker-compose files (all overrides), Kubernetes manifests / Helm templates and Terraform modules.\n2. Write `.fleetpocket/out/infra.mmd` as a `flowchart LR`.\n',
    [],
    false,
  ],
  [
    'pr-checklist',
    'Before opening the pull request, run the project checks and write a short test plan into the PR body.',
    '# PR checklist\n\n1. Run the linters and the tests the README names.\n2. Add a **Test plan** section to the PR body using `templates/test-plan.md`.\n',
    [{ path: 'templates/test-plan.md', content: '## Test plan\n\n- [ ] Unit tests\n- [ ] Manual check: …\n' }],
    true,
  ],
  [
    'sequence-flow',
    'Draw a Mermaid sequence diagram for one endpoint, command or job named in the task, following the real call path through the code.',
    '# Sequence flow\n\n1. Take the endpoint, CLI command or background job from the task prompt.\n2. Follow the real call path and write `.fleetpocket/out/sequence.mmd`.\n',
    [],
    false,
  ],
  [
    'system-design-c4',
    "Draw the system's architecture as C4 Context and Container diagrams (plus Component for the main service) in Mermaid, derived from the code and deployment files.",
    '# System design (C4)\n\n1. Find what the system is made of: entrypoints, deployment files, environment variables, clients.\n2. Write `.fleetpocket/out/c4-context.mmd` and `c4-container.mmd`.\n',
    [],
    false,
  ],
]

export function seedSkills() {
  skillState.rows = SEED.map(([slug, description, body, files, def], i) => ({
    id: i + 1,
    slug,
    name: slug,
    description,
    body: fm(slug, description, body),
    files,
    enabled_by_default: def,
    created_at: ago(60 * 24 * 7),
    updated_at: ago(60 * 24 * 7),
  }))
  skillState.nextId = skillState.rows.length + 1
  const vatary = db.projects.find((p) => p.id === 'vatary')
  if (vatary) vatary.skills = ['db-schema']
  // Seeded tasks have run: they list what their last run got.
  for (const task of db.tasks) if ((db.runs.get(task.id) ?? []).length) task.skills = taskSkills(task.project_id, [], [])
}

export function knownSlugs(slugs: string[]): string[] {
  return slugs.filter((s) => skillState.rows.some((r) => r.slug === s))
}

/** What a new task of the project gets (project ∪ defaults, plus add, minus remove), like the scheduler. */
export function taskSkills(projectId: string, add: string[], remove: string[]): string[] {
  const p = db.projects.find((x) => x.id === projectId)
  const set = new Set([...(p?.skills ?? []), ...skillState.rows.filter((r) => r.enabled_by_default).map((r) => r.slug), ...add])
  for (const s of remove) set.delete(s)
  return knownSlugs([...set].sort())
}

function view(r: Row): Skill {
  return { ...r, projects: db.projects.filter((p) => !p.archived && p.skills?.includes(r.slug)).map((p) => p.id) }
}

/** Frontmatter `name:` and `description:` like skills.Frontmatter (a line parser, quotes trimmed). */
function frontmatter(md: string): { name: string; description: string } {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/.exec(md)
  const out = { name: '', description: '' }
  if (!m) return out
  for (const line of m[1].split('\n')) {
    const i = line.indexOf(':')
    if (i < 0) continue
    const k = line.slice(0, i).trim()
    const v = line.slice(i + 1).trim().replace(/^["']+|["']+$/g, '')
    if (k === 'name') out.name = v
    if (k === 'description') out.description = v
  }
  return out
}

// A stand-in for the gitleaks scan: a few token shapes.
const SECRET = /(sk-ant-[A-Za-z0-9_-]{16,}|AKIA[0-9A-Z]{16}|ghp_[A-Za-z0-9]{30,})/

type Input = { slug?: string; body?: string; files?: SkillFile[]; enabled_by_default?: boolean }

/** skills.Validate: slug, paths, limits, text, frontmatter, secrets. Returns the message of a 400 or the row fields. */
function validate(in_: Input): string | Pick<Row, 'slug' | 'name' | 'description' | 'body' | 'files' | 'enabled_by_default'> {
  const slug = in_.slug ?? ''
  if (!/^[a-z][a-z0-9-]{1,63}$/.test(slug)) return 'slug must match ^[a-z][a-z0-9-]{1,63}$'
  const body = in_.body ?? ''
  const files = (in_.files ?? []).filter((f) => f.path !== 'SKILL.md')
  const all = [{ path: 'SKILL.md', content: body }, ...files]
  if (all.length > 50) return 'at most 50 files'
  const seen = new Set<string>()
  let total = 0
  for (const f of all) {
    const p = f.path
    if (!p || p.startsWith('/') || p.includes('\\') || p.split('/').some((s) => s === '' || s === '.' || s === '..' || s === '.git')) return `bad path "${p}"`
    if (seen.has(p)) return `duplicate file ${p}`
    seen.add(p)
    if (f.content.includes('\0')) return `${p} is not a text file`
    total += new TextEncoder().encode(f.content).length
  }
  if (total > 1 << 20) return `files exceed ${1 << 20} bytes in total`
  const { name, description } = frontmatter(body)
  if (!name || !description) return 'SKILL.md frontmatter needs name and description'
  const findings = all.flatMap((f) =>
    f.content.split('\n').flatMap((line, i) => (SECRET.test(line) ? [`${f.path}:${i + 1} generic-api-key`] : [])),
  )
  if (findings.length) return `possible secrets: ${findings.join(', ')}`
  return { slug, name, description, body, files, enabled_by_default: in_.enabled_by_default ?? false }
}

function insert(in_: Input, err: Err): Response {
  const v = validate(in_)
  if (typeof v === 'string') return err(400, 'invalid', v)
  if (skillState.rows.some((r) => r.slug === v.slug)) return err(409, 'conflict', `skill ${v.slug} exists`)
  const now = new Date().toISOString()
  const row: Row = { id: skillState.nextId++, ...v, created_at: now, updated_at: now }
  skillState.rows.push(row)
  return HttpResponse.json(view(row), { status: 201 })
}

// ---- A stored (uncompressed) zip for GET /skills/{id}/export ----

const CRC = Array.from({ length: 256 }, (_, n) => {
  let c = n
  for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1
  return c >>> 0
})
function crc32(b: Uint8Array) {
  let c = 0xffffffff
  for (const x of b) c = CRC[(c ^ x) & 0xff] ^ (c >>> 8)
  return (c ^ 0xffffffff) >>> 0
}

function zip(files: Array<{ path: string; content: string }>): Uint8Array {
  const enc = new TextEncoder()
  const parts: Uint8Array[] = []
  const central: Uint8Array[] = []
  let offset = 0
  for (const f of files) {
    const name = enc.encode(f.path)
    const data = enc.encode(f.content)
    const crc = crc32(data)
    const local = new DataView(new ArrayBuffer(30))
    local.setUint32(0, 0x04034b50, true)
    local.setUint16(4, 20, true)
    local.setUint32(14, crc, true)
    local.setUint32(18, data.length, true)
    local.setUint32(22, data.length, true)
    local.setUint16(26, name.length, true)
    const head = new Uint8Array(local.buffer)
    const dir = new DataView(new ArrayBuffer(46))
    dir.setUint32(0, 0x02014b50, true)
    dir.setUint16(4, 20, true)
    dir.setUint16(6, 20, true)
    dir.setUint32(16, crc, true)
    dir.setUint32(20, data.length, true)
    dir.setUint32(24, data.length, true)
    dir.setUint16(28, name.length, true)
    dir.setUint32(42, offset, true)
    central.push(new Uint8Array(dir.buffer), name)
    parts.push(head, name, data)
    offset += head.length + name.length + data.length
  }
  const size = central.reduce((n, c) => n + c.length, 0)
  const end = new DataView(new ArrayBuffer(22))
  end.setUint32(0, 0x06054b50, true)
  end.setUint16(8, files.length, true)
  end.setUint16(10, files.length, true)
  end.setUint32(12, size, true)
  end.setUint32(16, offset, true)
  const all = [...parts, ...central, new Uint8Array(end.buffer)]
  const out = new Uint8Array(all.reduce((n, c) => n + c.length, 0))
  let at = 0
  for (const c of all) {
    out.set(c, at)
    at += c.length
  }
  return out
}

export function skillHandlers(API: string, guard: Guard, err: Err) {
  const find = (id: unknown) => skillState.rows.find((r) => r.id === Number(id))
  return [
    http.get(`${API}/skills`, ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const items = [...skillState.rows].sort((a, b) => a.slug.localeCompare(b.slug)).map(view)
      return HttpResponse.json({ items })
    }),
    http.post(`${API}/skills`, async ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      return insert((await request.json()) as Input, err)
    }),
    http.post(`${API}/skills/import`, async ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const form = await request.formData()
      const file = form.get('file')
      if (!(file instanceof File)) return err(400, 'invalid', 'file is required')
      if (file.size > 2 << 20) return err(400, 'invalid', 'file is too large')
      if (file.name.toLowerCase().endsWith('.zip')) return err(400, 'invalid', 'zip import is not simulated in mock mode; import a SKILL.md')
      const body = await file.text()
      const slug = String(form.get('slug') ?? '') || frontmatter(body).name.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '')
      return insert({ slug, body }, err)
    }),
    http.get(`${API}/skills/:id`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      return r ? HttpResponse.json(view(r)) : err(404, 'not_found', 'No such skill')
    }),
    http.put(`${API}/skills/:id`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such skill')
      const v = validate((await request.json()) as Input)
      if (typeof v === 'string') return err(400, 'invalid', v)
      if (skillState.rows.some((x) => x.slug === v.slug && x.id !== r.id)) return err(409, 'conflict', `skill ${v.slug} exists`)
      // Projects reference skills by id on the server: a rename follows.
      if (v.slug !== r.slug) for (const p of db.projects) p.skills = p.skills?.map((s) => (s === r.slug ? v.slug : s))
      Object.assign(r, v, { updated_at: new Date().toISOString() })
      return HttpResponse.json(view(r))
    }),
    http.delete(`${API}/skills/:id`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such skill')
      skillState.rows = skillState.rows.filter((x) => x.id !== r.id)
      for (const p of db.projects) p.skills = p.skills?.filter((s) => s !== r.slug)
      return HttpResponse.json({ ok: true })
    }),
    http.post(`${API}/skills/:id/duplicate`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such skill')
      let slug = `${r.slug}-copy`
      for (let n = 2; skillState.rows.some((x) => x.slug === slug); n++) slug = `${r.slug}-copy-${n}`
      const now = new Date().toISOString()
      const row: Row = { ...r, id: skillState.nextId++, slug, files: r.files.map((f) => ({ ...f })), enabled_by_default: false, created_at: now, updated_at: now }
      skillState.rows.push(row)
      return HttpResponse.json(view(row), { status: 201 })
    }),
    http.get(`${API}/skills/:id/export`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const r = find(params.id)
      if (!r) return err(404, 'not_found', 'No such skill')
      const bytes = zip([{ path: `${r.slug}/SKILL.md`, content: r.body }, ...r.files.map((f) => ({ path: `${r.slug}/${f.path}`, content: f.content }))])
      return new HttpResponse(bytes, {
        headers: { 'Content-Type': 'application/zip', 'Content-Disposition': `attachment; filename="${r.slug}.zip"` },
      })
    }),
  ]
}
