import { http, HttpResponse } from 'msw'
import type { Egress, Integrations, MCPServer, MCPServerInput } from '@/api/types'
import { addEvent, db, makeTask, startRun } from './db'

// MCP servers and egress for mock mode (docs/API.md "Integrations").

const now = Date.now()
const ago = (min: number) => new Date(now - min * 60_000).toISOString()

export const PRESETS: Integrations['presets'] = [
  { id: 'agent', label: "The task's agent API", hosts: ['api.anthropic.com', '*.anthropic.com', 'api.openai.com', '*.cursor.sh'] },
  { id: 'npm', label: 'npm', hosts: ['registry.npmjs.org', '*.npmjs.org', 'registry.yarnpkg.com'] },
  { id: 'pypi', label: 'PyPI', hosts: ['pypi.org', 'files.pythonhosted.org'] },
  { id: 'go', label: 'Go module proxy', hosts: ['proxy.golang.org', 'sum.golang.org'] },
  { id: 'crates', label: 'crates.io', hosts: ['crates.io', '*.crates.io'] },
  { id: 'dockerhub', label: 'Docker Hub', hosts: ['registry-1.docker.io', 'auth.docker.io'] },
  { id: 'github', label: 'GitHub (dependency downloads)', hosts: ['github.com', 'codeload.github.com', '*.githubusercontent.com'] },
]

const AGENTS: Integrations['agents'] = [
  { agent: 'claude', mcp: true, proxy: 'yes', note: 'Agent SDK mcpServers (HTTP)' },
  { agent: 'codex', mcp: true, proxy: 'yes', note: 'Codex config mcp_servers (Streamable HTTP, bearer token from the environment)' },
  { agent: 'cursor', mcp: true, proxy: 'unverified', note: 'Cursor SDK mcpServers (HTTP); proxy through NODE_USE_ENV_PROXY, not verified' },
]

type State = { servers: MCPServer[]; globals: Map<string, number[]>; egress: Map<string, Egress>; nextId: number }
export const mcp: State = { servers: [], globals: new Map(), egress: new Map(), nextId: 1 }

function hint(v: string): string {
  return v.length >= 12 ? `…${v.slice(-4)}` : v.length >= 6 ? `…${v.slice(-2)}` : '…'
}

function server(fields: Partial<MCPServer> & Pick<MCPServer, 'name' | 'kind'>): MCPServer {
  const s: MCPServer = {
    id: mcp.nextId++,
    project_id: null,
    transport: null,
    url: null,
    runtime: null,
    package: null,
    command: null,
    args: [],
    secret_hints: {},
    tools_mode: 'all',
    tools: [],
    read_only: fields.kind === 'postgres',
    max_calls: null,
    enabled: true,
    last_used_at: null,
    last_status: null,
    last_error: null,
    created_at: ago(60 * 24 * 3),
    updated_at: ago(60 * 24 * 3),
    ...fields,
  }
  mcp.servers.push(s)
  return s
}

function egressOf(project: string): Egress {
  return mcp.egress.get(project) ?? { mode: 'open', presets: [], hosts: [] }
}

export function integrationsOf(project: string): Integrations {
  const own = mcp.servers.filter((s) => s.project_id === project)
  const globals = mcp.servers.filter((s) => s.project_id === null)
  const ids = mcp.globals.get(project) ?? []
  const p = db.projects.find((x) => x.id === project)
  return {
    secrets_enabled: true,
    egress: egressOf(project),
    egress_supported: !p?.node,
    presets: PRESETS,
    servers: own,
    global_servers: globals,
    global_server_ids: ids,
    active: [...own, ...globals.filter((g) => ids.includes(g.id))].filter((s) => s.enabled).sort((a, b) => a.name.localeCompare(b.name)),
    agents: AGENTS,
    gateway_url: 'https://jm.example.com',
  }
}

export function seedIntegrations() {
  mcp.servers = []
  mcp.globals = new Map()
  mcp.egress = new Map()
  mcp.nextId = 1
  server({
    project_id: 'vatary',
    name: 'linear',
    kind: 'remote',
    transport: 'http',
    url: 'https://mcp.linear.app/mcp',
    secret_hints: { bearer: '…7f2c' },
    tools_mode: 'deny',
    tools: ['delete_*'],
    max_calls: 50,
    last_used_at: ago(42),
    last_status: 'ok',
  })
  server({
    project_id: 'vatary',
    name: 'db',
    kind: 'postgres',
    secret_hints: { DATABASE_URL: '…prod' },
    last_used_at: ago(43),
    last_status: 'ok',
  })
  server({
    project_id: 'vatary',
    name: 'playwright',
    kind: 'stdio',
    runtime: 'npm',
    package: '@playwright/mcp',
    args: ['--headless'],
    enabled: false,
  })
  const sentry = server({
    name: 'sentry',
    kind: 'remote',
    transport: 'http',
    url: 'https://mcp.sentry.dev/mcp',
    secret_hints: { Authorization: '…a91e' },
    tools_mode: 'allow',
    tools: ['get_issue', 'search_*'],
    last_used_at: ago(60 * 5),
    last_status: 'error',
    last_error: '401 Unauthorized: token expired',
  })
  server({ name: 'github-issues', kind: 'remote', transport: 'http', url: 'https://api.githubcopilot.com/mcp/', secret_hints: { bearer: '…09ab' } })
  mcp.globals.set('vatary', [sentry.id])
  mcp.egress.set('vatary', { mode: 'allowlist', presets: ['agent', 'npm', 'github'], hosts: ['api.stripe.com'] })

  // A task that used the gateway and hit the allowlist.
  const t = makeTask({ project_id: 'vatary', title: 'Close stale Linear issues fixed by merged PRs', status: 'queued', created_at: ago(45) })
  startRun(t.id, 'initial', t.prompt, ago(45))
  addEvent(t.id, 'integrations', { servers: ['db', 'linear', 'sentry'], egress: 'allowlist' }, false, ago(45))
  addEvent(t.id, 'assistant_text', { text: 'Listing open Linear issues and matching them against merged PRs.' }, true, ago(44))
  addEvent(t.id, 'mcp_call', { server: 'linear', tool: 'list_issues', ok: true, duration_ms: 412, args_bytes: 58, result_bytes: 18234 }, false, ago(44))
  addEvent(t.id, 'mcp_call', { server: 'db', tool: 'query', ok: true, duration_ms: 37, args_bytes: 96, result_bytes: 2210 }, false, ago(43))
  addEvent(t.id, 'mcp_call', { server: 'linear', tool: 'delete_issue', ok: false, duration_ms: 0, args_bytes: 22, result_bytes: 0, denied: 'denied', error: 'tool not allowed for this project' }, false, ago(43))
  addEvent(t.id, 'tool_use', { tool: 'Bash', summary: 'Bash curl -s https://status.linear.app/api' }, true, ago(42))
  addEvent(t.id, 'tool_result', { tool: 'Bash', ok: false, summary: 'curl: (56) CONNECT tunnel failed, response 403' }, true, ago(42))
  addEvent(t.id, 'egress_blocked', { host: 'status.linear.app' }, false, ago(42))
  addEvent(t.id, 'mcp_call', { server: 'linear', tool: 'update_issue', ok: true, duration_ms: 288, args_bytes: 140, result_bytes: 912 }, false, ago(40))
  addEvent(t.id, 'usage', { cost_usd_total: 0.48, input_tokens: 61000, output_tokens: 2900, turns: 9 }, true, ago(39))
  addEvent(t.id, 'done', { session_id: 's-mcp', summary: 'Closed **4** Linear issues fixed by merged PRs (#38, #39, #41).', no_changes: true, cost_usd_total: 0.48 }, true, ago(39))
  addEvent(t.id, 'run_finished', { run_id: 0, exit_code: 0 }, false, ago(39))
  Object.assign(t, { status: 'completed', status_reason: 'finished with no changes', finished_at: ago(39), updated_at: ago(39), cost_usd_total: 0.48 })
}

type Guard = (request: Request) => Response | null
type Err = (status: number, code: string, message: string) => Response

const NAME = /^[a-z][a-z0-9_-]{0,31}$/

function apply(s: MCPServer, body: MCPServerInput) {
  Object.assign(s, {
    name: body.name,
    kind: body.kind,
    transport: body.kind === 'remote' ? (body.transport ?? 'http') : null,
    url: body.url ?? null,
    runtime: body.runtime ?? null,
    package: body.package ?? null,
    command: body.command ?? null,
    args: body.args ?? [],
    tools_mode: body.tools_mode ?? 'all',
    tools: body.tools ?? [],
    read_only: body.read_only ?? body.kind === 'postgres',
    max_calls: body.max_calls ?? null,
    enabled: body.enabled ?? true,
    updated_at: new Date().toISOString(),
  })
  const hints = { ...s.secret_hints }
  for (const [k, v] of Object.entries(body.secrets ?? {})) {
    if (v === null) delete hints[k]
    else if (v) hints[k] = hint(v)
  }
  s.secret_hints = hints
}

export function integrationHandlers(API: string, guard: Guard, err: Err) {
  return [
    http.get(`${API}/mcp-servers`, ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const project = new URL(request.url).searchParams.get('project')
      return HttpResponse.json({ items: mcp.servers.filter((s) => s.project_id === (project || null)) })
    }),
    http.post(`${API}/mcp-servers`, async ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const body = (await request.json()) as MCPServerInput
      if (!NAME.test(body.name)) return err(400, 'invalid', 'invalid MCP server: name')
      const project = body.project_id ?? null
      if (mcp.servers.some((s) => s.project_id === project && s.name === body.name)) return err(409, 'conflict', `an MCP server named "${body.name}" exists`)
      const s = server({ name: body.name, kind: body.kind, project_id: project, created_at: new Date().toISOString() })
      apply(s, body)
      return HttpResponse.json(s, { status: 201 })
    }),
    http.put(`${API}/mcp-servers/:id`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const s = mcp.servers.find((x) => x.id === Number(params.id))
      if (!s) return err(404, 'not_found', 'not found')
      apply(s, (await request.json()) as MCPServerInput)
      return HttpResponse.json(s)
    }),
    http.delete(`${API}/mcp-servers/:id`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const before = mcp.servers.length
      mcp.servers = mcp.servers.filter((x) => x.id !== Number(params.id))
      if (mcp.servers.length === before) return err(404, 'not_found', 'not found')
      return new HttpResponse(null, { status: 204 })
    }),
    http.post(`${API}/mcp-servers/:id/test`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const s = mcp.servers.find((x) => x.id === Number(params.id))
      if (!s) return err(404, 'not_found', 'not found')
      s.last_used_at = new Date().toISOString()
      if (s.last_error) {
        s.last_status = 'error'
        return HttpResponse.json({ ok: false, error: s.last_error, tools: [] })
      }
      s.last_status = 'ok'
      const names = s.kind === 'postgres' ? ['query', 'list_tables'] : ['list_issues', 'get_issue', 'update_issue', 'delete_issue']
      const allowed = (n: string) => {
        const match = s.tools.some((p) => new RegExp(`^${p.replace(/[.+^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*').replace(/\?/g, '.')}$`).test(n))
        return s.tools_mode === 'allow' ? match : s.tools_mode === 'deny' ? !match : true
      }
      return HttpResponse.json({ ok: true, tools: names.map((n) => ({ name: n, description: '', allowed: allowed(n), read_only: n.startsWith('list') || n.startsWith('get') || n === 'query' })) })
    }),
    http.get(`${API}/projects/:id/integrations`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      if (!db.projects.some((p) => p.id === params.id)) return err(404, 'not_found', 'not found')
      return HttpResponse.json(integrationsOf(String(params.id)))
    }),
    http.put(`${API}/projects/:id/integrations`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const id = String(params.id)
      const body = (await request.json()) as { egress?: Egress; global_server_ids?: number[] }
      if (body.egress) {
        if (body.egress.mode !== 'open' && body.egress.mode !== 'allowlist') return err(400, 'invalid', 'egress.mode must be "open" or "allowlist"')
        mcp.egress.set(id, { mode: body.egress.mode, presets: body.egress.presets ?? [], hosts: (body.egress.hosts ?? []).map((h) => h.toLowerCase()) })
      }
      if (body.global_server_ids) mcp.globals.set(id, body.global_server_ids)
      return HttpResponse.json(integrationsOf(id))
    }),
    http.post(`${API}/projects/:id/egress/allow`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const id = String(params.id)
      const { host } = (await request.json()) as { host: string }
      const e = egressOf(id)
      const next = { ...e, hosts: e.hosts.includes(host) ? e.hosts : [...e.hosts, host] }
      mcp.egress.set(id, next)
      return HttpResponse.json(next)
    }),
  ]
}
