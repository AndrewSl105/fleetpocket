import type { MCPServer, MCPServerInput } from '@/api/types'

export type Kind = MCPServer['kind']
export type ToolsMode = MCPServer['tools_mode']

/** One secret row: a stored one shows its hint until the owner types a new value. */
export type SecretRow = { key: string; value: string; hint?: string }

export type ServerForm = {
  name: string
  kind: Kind
  transport: 'http' | 'sse'
  url: string
  runtime: 'npm' | 'pip' | 'image'
  pkg: string
  command: string
  args: string
  secrets: SecretRow[]
  toolsMode: ToolsMode
  tools: string
  readOnly: boolean
  maxCalls: string
  enabled: boolean
}

export const NAME_RE = /^[a-z][a-z0-9_-]{0,31}$/

export function emptyForm(): ServerForm {
  return {
    name: '',
    kind: 'remote',
    transport: 'http',
    url: '',
    runtime: 'npm',
    pkg: '',
    command: '',
    args: '',
    secrets: [{ key: 'bearer', value: '' }],
    toolsMode: 'all',
    tools: '',
    readOnly: false,
    maxCalls: '',
    enabled: true,
  }
}

export function toForm(s: MCPServer): ServerForm {
  return {
    name: s.name,
    kind: s.kind,
    transport: s.transport === 'sse' ? 'sse' : 'http',
    url: s.url ?? '',
    runtime: s.runtime ?? 'npm',
    pkg: s.package ?? '',
    command: s.command ?? '',
    args: s.args.join('\n'),
    secrets: Object.entries(s.secret_hints).map(([key, hint]) => ({ key, value: '', hint })),
    toolsMode: s.tools_mode,
    tools: s.tools.join(', '),
    readOnly: s.read_only,
    maxCalls: s.max_calls ? String(s.max_calls) : '',
    enabled: s.enabled,
  }
}

/** Default secret rows when the kind changes on a new server. */
export function secretsForKind(kind: Kind): SecretRow[] {
  if (kind === 'postgres') return [{ key: 'DATABASE_URL', value: '' }]
  if (kind === 'remote') return [{ key: 'bearer', value: '' }]
  return [{ key: '', value: '' }]
}

function validURL(v: string): boolean {
  try {
    const u = new URL(v)
    return (u.protocol === 'https:' || u.protocol === 'http:') && !!u.hostname && !u.username && !u.password
  } catch {
    return false
  }
}

/** Problems that block saving, by field; empty when the form is valid. */
export function validate(f: ServerForm): Partial<Record<'name' | 'url' | 'pkg' | 'maxCalls' | 'secrets', string>> {
  const e: Partial<Record<'name' | 'url' | 'pkg' | 'maxCalls' | 'secrets', string>> = {}
  if (!NAME_RE.test(f.name)) e.name = 'Lowercase letters, digits, - and _ (start with a letter)'
  if (f.kind === 'remote' && !validURL(f.url.trim())) e.url = 'An http(s) URL without credentials'
  if (f.kind === 'stdio' && !f.pkg.trim()) e.pkg = 'Required'
  if (f.maxCalls.trim() && !(Number.isInteger(Number(f.maxCalls)) && Number(f.maxCalls) >= 1)) e.maxCalls = 'A whole number, at least 1'
  const keys = f.secrets.map((s) => s.key.trim()).filter(Boolean)
  if (new Set(keys).size !== keys.length) e.secrets = 'Each name once'
  return e
}

/**
 * The API body. Secrets are write-only: a typed value is sent; a stored row
 * left empty is not sent (kept); a stored row the owner removed is sent as
 * null (deleted).
 */
export function toInput(f: ServerForm, initial: SecretRow[] = [], projectId?: string | null): MCPServerInput {
  const secrets: Record<string, string | null> = {}
  for (const s of f.secrets) {
    const k = s.key.trim()
    if (k && s.value) secrets[k] = s.value
  }
  const kept = new Set(f.secrets.map((s) => s.key.trim()))
  for (const s of initial) if (!kept.has(s.key)) secrets[s.key] = null
  const lines = (v: string) =>
    v
      .split('\n')
      .map((x) => x.trim())
      .filter(Boolean)
  const body: MCPServerInput = {
    name: f.name.trim(),
    kind: f.kind,
    secrets,
    tools_mode: f.toolsMode,
    tools: f.toolsMode === 'all' ? [] : f.tools.split(',').map((x) => x.trim()).filter(Boolean),
    read_only: f.readOnly,
    max_calls: f.maxCalls.trim() ? Number(f.maxCalls) : null,
    enabled: f.enabled,
  }
  if (projectId !== undefined) body.project_id = projectId
  if (f.kind === 'remote') Object.assign(body, { transport: f.transport, url: f.url.trim() })
  if (f.kind === 'stdio') Object.assign(body, { runtime: f.runtime, package: f.pkg.trim(), command: f.command.trim() || null, args: lines(f.args) })
  return body
}

/** Allowlist entries typed one per line (or comma-separated), lowercased, without duplicates. */
export function parseHosts(text: string): string[] {
  const out: string[] = []
  for (const raw of text.split(/[\n,]/)) {
    const h = raw.trim().toLowerCase().replace(/\.$/, '')
    if (h && !out.includes(h)) out.push(h)
  }
  return out
}

const HOST_RE = /^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(:\d{1,5})?$/

/** Entries the server would reject (mirrors internal/egress NormalizeHost for hostnames). */
export function invalidHosts(hosts: string[]): string[] {
  return hosts.filter((h) => !HOST_RE.test(h))
}
