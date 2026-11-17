import { http, HttpResponse } from 'msw'
import type { Agent, AuditEntry, Credential, Invite, Me, Role, Task, TeamUser, UserRef } from '@/api/types'
import { MOCK_AGENTS } from './db'

// Team mode in mock mode (docs/API.md "Teams"). The team (users, invites,
// keys, settings) lives in localStorage so it survives the reload of an
// invite link; tasks stay in memory like the rest of the mock backend.
// Mock passwords are kept in clear and any 6-digit code except 000000 is a
// valid TOTP code, like the mock login.

const KEY = 'fleetpocket.mock.team'
const OWNER_PASSWORD = 'fleetpocket'

type MockUser = TeamUser & { password: string }
type MockInvite = Invite & { token: string }
type MockCred = Credential & { value: string }
type State = {
  enabled: boolean
  fallback: boolean
  users: MockUser[]
  invites: MockInvite[]
  creds: Record<string, MockCred[]>
  audit: AuditEntry[]
  next: number
}

const empty = (): State => ({ enabled: false, fallback: false, users: [], invites: [], creds: {}, audit: [], next: 1 })

function load(): State {
  try {
    const v = localStorage.getItem(KEY)
    return v ? { ...empty(), ...(JSON.parse(v) as State) } : empty()
  } catch {
    return empty()
  }
}

let state = load()

function save() {
  try {
    localStorage.setItem(KEY, JSON.stringify(state))
  } catch {
    // ignore
  }
}

/** Test helper: forget the mock team. */
export function resetTeam() {
  state = empty()
  save()
}

export const teamOn = () => state.enabled

const initials = (name: string) =>
  name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((w) => w[0]!.toUpperCase())
    .join('') || '?'

const nowIso = () => new Date().toISOString()

function audit(actor: number | null, action: string, target_type = '', target_id = '', meta: Record<string, unknown> = {}) {
  state.audit.unshift({ id: state.next++, at: nowIso(), actor_user_id: actor, action, target_type, target_id, meta })
  state.audit = state.audit.slice(0, 200)
}

// ---- the session's user ----

const SESSION_KEY = 'fleetpocket.mock.session'

/** The user of the mock session ("web:<id>"), in team mode. */
export function currentUser(): MockUser | undefined {
  if (!state.enabled) return undefined
  let v: string | null = null
  try {
    v = localStorage.getItem(SESSION_KEY)
  } catch {
    // ignore
  }
  const id = Number(v?.split(':')[1])
  return state.users.find((u) => u.id === id && !u.disabled_at)
}

function setSessionUser(id: number) {
  try {
    localStorage.setItem(SESSION_KEY, `web:${id}`)
  } catch {
    // ignore
  }
}

export function userRef(u: { id: number; name: string } | undefined): UserRef | null {
  return u ? { id: u.id, name: u.name, initials: initials(u.name) } : null
}

const meOf = (u: MockUser): Me => {
  const { password: _pw, active: _a, ...me } = u
  void _pw
  void _a
  return me
}

/** Extra /me fields: team_enabled, and the user in team mode. */
export function sessionExtras(): { team_enabled: boolean; user?: Me; agents?: Agent[] } {
  const u = currentUser()
  if (!u) return { team_enabled: state.enabled }
  return { team_enabled: true, user: meOf(u), agents: ['claude', 'codex', 'cursor'] }
}

const err = (status: number, code: string, message: string) => HttpResponse.json({ error: { code, message } }, { status })

// ---- tasks (called by handlers.ts) ----

/** Team mode: who creates a task and whether it needs a key (the resolve table, mock size). */
export function applyTeamToNewTask(task: Task): void {
  const u = currentUser()
  if (!u) return
  task.created_by = userRef(u)
  const personal = (state.creds[u.id] ?? []).some((c) => c.provider === task.agent)
  const shared = state.fallback && MOCK_AGENTS.agents.includes(task.agent)
  if (!task.node && !personal && !shared) {
    task.status = 'needs_key'
    task.status_reason = `Add your ${task.agent === 'claude' ? 'Claude' : task.agent === 'codex' ? 'Codex (OpenAI)' : 'Cursor'} key in My keys`
  }
}

/** Refusal for a write by the current user, or null (single-owner mode: never). */
export function refuseWrite(): Response | null {
  const u = currentUser()
  if (!u || u.role !== 'viewer') return null
  return err(403, 'forbidden', 'viewers cannot change anything')
}

/** Refusal for an action on a task by the current user, or null. */
export function refuseTaskAction(task: Task): Response | null {
  const u = currentUser()
  if (!u || u.role === 'admin') {
    if (u && task.created_by && task.created_by.id !== u.id) audit(u.id, 'task.admin_action', 'task', String(task.id))
    return null
  }
  if (u.role === 'member' && task.created_by?.id === u.id) return null
  return err(403, 'forbidden', 'only its creator or an admin can do this')
}

/** Does the current user have a key for the agent (or the shared fallback)? */
export function hasKeyFor(task: Task): boolean {
  const u = currentUser()
  if (!u) return true
  return (state.creds[task.created_by?.id ?? u.id] ?? []).some((c) => c.provider === task.agent) || (state.fallback && MOCK_AGENTS.agents.includes(task.agent))
}

function admin(): MockUser | Response {
  const u = currentUser()
  if (!state.enabled) return err(409, 'team_off', 'team mode is off')
  if (!u) return err(401, 'unauthorized', 'Not signed in')
  if (u.role !== 'admin') return err(403, 'forbidden', 'admins only')
  return u
}

function activeCount() {
  const act = state.users.filter((u) => !u.disabled_at)
  return { users: act.length, admins: act.filter((u) => u.role === 'admin').length }
}

const API = '*/api/v1'

export const teamHandlers = [
  http.get(`${API}/setup/status`, () => HttpResponse.json({ setup_required: false, github_configured: true, team_enabled: state.enabled })),

  // Login with an email in team mode; without one, the owner credentials sign in as user #1.
  http.post(`${API}/auth/login`, async ({ request }) => {
    if (!state.enabled) return undefined
    const body = (await request.clone().json()) as { email?: string; password: string; totp: string }
    if (!/^\d{6}$/.test(body.totp) || body.totp === '000000') return err(401, 'bad_credentials', 'Wrong email, password or code')
    const email = body.email?.trim().toLowerCase()
    const u = email
      ? state.users.find((x) => x.email === email && (x.password === body.password || (x.is_owner && body.password === OWNER_PASSWORD)))
      : body.password === OWNER_PASSWORD
        ? state.users.find((x) => x.is_owner)
        : undefined
    if (!u || u.disabled_at) return err(401, 'bad_credentials', 'Wrong email, password or code')
    u.last_login_at = nowIso()
    setSessionUser(u.id)
    save()
    return HttpResponse.json({ ok: true, kind: 'web', mode: 'server', ideas_enabled: false, default_agent: 'claude', ...sessionExtras() })
  }),

  http.get(`${API}/team`, () => {
    const u = currentUser()
    if (state.enabled && u?.role !== 'admin') return err(403, 'forbidden', 'admins only')
    const c = activeCount()
    return HttpResponse.json({
      enabled: state.enabled,
      shared_key_fallback: state.fallback,
      secrets_key: true,
      server_keys: MOCK_AGENTS.agents,
      ...(state.enabled ? { active_users: c.users, active_admins: c.admins } : {}),
    })
  }),
  http.post(`${API}/team/enable`, async ({ request }) => {
    if (state.enabled) return err(409, 'conflict', 'team mode is already on')
    const body = (await request.json()) as { email: string; name: string }
    const email = body.email.trim().toLowerCase()
    if (!/^[^@\s]+@[^@\s]+$/.test(email) || !body.name.trim()) return err(400, 'invalid', 'a valid email and a name are required')
    const owner: MockUser = {
      id: state.next++, email, name: body.name.trim(), role: 'admin', is_owner: true, github_login: null, commit_email: null,
      telegram_user_id: null, monthly_budget_usd: null, task_scope: null, disabled_at: null, created_at: nowIso(), last_login_at: nowIso(),
      initials: initials(body.name), telegram_linked: false, active: true, password: OWNER_PASSWORD,
    }
    state = { ...state, enabled: true, users: [owner] }
    setSessionUser(owner.id)
    audit(owner.id, 'team.enable', 'user', String(owner.id))
    save()
    return HttpResponse.json({ enabled: true, user: meOf(owner) })
  }),
  http.post(`${API}/team/disable`, () => {
    const a = admin()
    if (a instanceof Response) return a
    if (activeCount().users > 1) return err(409, 'conflict', 'disable or remove the other users first')
    state.enabled = false
    try {
      localStorage.setItem(SESSION_KEY, 'web')
    } catch {
      // ignore
    }
    save()
    return HttpResponse.json({ enabled: false })
  }),
  http.patch(`${API}/team`, async ({ request }) => {
    const a = admin()
    if (a instanceof Response) return a
    const body = (await request.json()) as { shared_key_fallback?: boolean }
    if (body.shared_key_fallback !== undefined) {
      state.fallback = body.shared_key_fallback
      audit(a.id, 'team.fallback', '', '', { enabled: state.fallback })
    }
    save()
    const c = activeCount()
    return HttpResponse.json({ enabled: true, shared_key_fallback: state.fallback, secrets_key: true, server_keys: MOCK_AGENTS.agents, active_users: c.users, active_admins: c.admins })
  }),

  http.get(`${API}/users`, () => {
    if (!state.enabled) return err(409, 'team_off', 'team mode is off')
    const u = currentUser()
    if (!u) return err(401, 'unauthorized', 'Not signed in')
    const items = state.users.map(({ password: _p, ...x }) => {
      void _p
      return u.role === 'admin' ? x : { ...x, email: '', telegram_linked: false }
    })
    return HttpResponse.json({ items })
  }),
  http.patch(`${API}/users/:id`, async ({ request, params }) => {
    const a = admin()
    if (a instanceof Response) return a
    const u = state.users.find((x) => x.id === Number(params.id))
    if (!u) return err(404, 'not_found', 'no such user')
    const body = (await request.json()) as { role?: Role; disabled?: boolean; monthly_budget_usd?: number; clear_budget?: boolean }
    const lastAdmin = u.role === 'admin' && !u.disabled_at && activeCount().admins <= 1
    if (u.is_owner && ((body.role && body.role !== 'admin') || body.disabled)) return err(409, 'conflict', 'the owner (user #1) is the break-glass admin')
    if (lastAdmin && ((body.role && body.role !== 'admin') || body.disabled)) return err(409, 'conflict', 'the last active admin cannot be demoted or disabled')
    if (body.role && body.role !== u.role) {
      audit(a.id, 'user.role', 'user', String(u.id), { from: u.role, to: body.role })
      u.role = body.role
    }
    if (body.monthly_budget_usd !== undefined) u.monthly_budget_usd = body.monthly_budget_usd
    if (body.clear_budget) u.monthly_budget_usd = null
    if (body.disabled === true) {
      u.disabled_at = nowIso()
      u.active = false
      state.creds[u.id] = []
      audit(a.id, 'user.disable', 'user', String(u.id))
    }
    if (body.disabled === false) {
      u.disabled_at = null
      u.active = true
    }
    save()
    const { password: _p, ...view } = u
    void _p
    return HttpResponse.json({ user: view, disable: body.disabled ? { cancelled_tasks: [], live_tasks: [] } : null })
  }),
  http.delete(`${API}/users/:id`, ({ params }) => {
    const a = admin()
    if (a instanceof Response) return a
    const u = state.users.find((x) => x.id === Number(params.id))
    if (!u) return err(404, 'not_found', 'no such user')
    if (u.is_owner || !u.disabled_at) return err(409, 'conflict', 'disable the user before removing them')
    state.users = state.users.filter((x) => x !== u)
    audit(a.id, 'user.remove', 'user', String(u.id))
    save()
    return HttpResponse.json({ ok: true })
  }),

  http.get(`${API}/invites`, () => {
    const a = admin()
    if (a instanceof Response) return a
    return HttpResponse.json({ items: state.invites.map(({ token: _t, ...i }) => (void _t, i)) })
  }),
  http.post(`${API}/invites`, async ({ request }) => {
    const a = admin()
    if (a instanceof Response) return a
    const body = (await request.json()) as { role: Role; email?: string | null; expires_in_hours?: number }
    const token = `inv-${crypto.randomUUID()}`
    const inv: MockInvite = {
      id: state.next++, role: body.role, email: body.email?.trim().toLowerCase() || null, created_by: a.id,
      expires_at: new Date(Date.now() + (body.expires_in_hours || 168) * 3600_000).toISOString(),
      used_at: null, used_by: null, revoked_at: null, created_at: nowIso(), token,
    }
    state.invites.unshift(inv)
    audit(a.id, 'invite.create', 'invite', String(inv.id), { role: inv.role })
    save()
    const { token: _t, ...invite } = inv
    void _t
    return HttpResponse.json({ invite, token, url: `${window.location.origin}/signup?token=${token}` }, { status: 201 })
  }),
  http.delete(`${API}/invites/:id`, ({ params }) => {
    const a = admin()
    if (a instanceof Response) return a
    const inv = state.invites.find((i) => i.id === Number(params.id))
    if (!inv || inv.used_at || inv.revoked_at) return err(404, 'not_found', 'no such open invite')
    inv.revoked_at = nowIso()
    save()
    return HttpResponse.json({ ok: true })
  }),

  http.post(`${API}/signup/start`, async ({ request }) => {
    const { token } = (await request.json()) as { token: string }
    const inv = state.invites.find((i) => i.token === token && !i.used_at && !i.revoked_at && new Date(i.expires_at) > new Date())
    if (!state.enabled || !inv) return err(410, 'invite_gone', 'this invite link is invalid, used, revoked or expired')
    const secret = 'JBSWY3DPEHPK3PXP'
    return HttpResponse.json({
      enrollment_id: `enr-${inv.id}`, role: inv.role, email: inv.email, totp_secret: secret,
      otpauth_uri: `otpauth://totp/Jungle%20Monkey:${encodeURIComponent(inv.email ?? 'team')}?secret=${secret}&issuer=Jungle%20Monkey`,
    })
  }),
  http.post(`${API}/signup`, async ({ request }) => {
    const b = (await request.json()) as { token: string; name: string; email: string; password: string; totp: string }
    const inv = state.invites.find((i) => i.token === b.token && !i.used_at && !i.revoked_at && new Date(i.expires_at) > new Date())
    if (!inv) return err(410, 'invite_gone', 'this invite link is invalid, used, revoked or expired')
    if (!/^\d{6}$/.test(b.totp) || b.totp === '000000') return err(400, 'bad_code', 'invalid or expired code')
    const email = b.email.trim().toLowerCase()
    if (inv.email && inv.email !== email) return err(409, 'conflict', 'this invite is for another email address')
    if ((b.password ?? '').length < 12) return err(400, 'invalid', 'the password needs at least 12 characters')
    if (state.users.some((u) => u.email === email)) return err(409, 'conflict', 'a user with this email already exists')
    const u: MockUser = {
      id: state.next++, email, name: b.name.trim(), role: inv.role, is_owner: false, github_login: null, commit_email: null,
      telegram_user_id: null, monthly_budget_usd: null, task_scope: null, disabled_at: null, created_at: nowIso(), last_login_at: nowIso(),
      initials: initials(b.name), telegram_linked: false, active: true, password: b.password,
    }
    state.users.push(u)
    Object.assign(inv, { used_at: nowIso(), used_by: u.id })
    audit(u.id, 'user.signup', 'user', String(u.id), { role: u.role })
    setSessionUser(u.id)
    save()
    return HttpResponse.json({ ok: true, kind: 'web', mode: 'server', ideas_enabled: false, default_agent: 'claude', ...sessionExtras() }, { status: 201 })
  }),

  http.patch(`${API}/me`, async ({ request }) => {
    const u = currentUser()
    if (!u) return err(state.enabled ? 401 : 409, state.enabled ? 'unauthorized' : 'team_off', 'team mode only')
    const b = (await request.json()) as Partial<{ name: string; email: string; github_login: string; commit_email: string; task_scope: 'mine' | 'everyone'; current_password: string; new_password: string }>
    if (b.name !== undefined) Object.assign(u, { name: b.name, initials: initials(b.name) })
    if (b.email !== undefined) u.email = b.email.toLowerCase()
    if (b.github_login !== undefined) u.github_login = b.github_login || null
    if (b.commit_email !== undefined) u.commit_email = b.commit_email || null
    if (b.task_scope) u.task_scope = b.task_scope
    if (b.new_password) {
      if (b.current_password !== u.password && !(u.is_owner && b.current_password === OWNER_PASSWORD)) return err(400, 'invalid', 'the current password is wrong')
      u.password = b.new_password
    }
    save()
    return HttpResponse.json(meOf(u))
  }),
  http.post(`${API}/me/totp`, () => {
    const u = currentUser()
    if (!u) return err(409, 'team_off', 'team mode only')
    return HttpResponse.json({ enrollment_id: 'reset', role: u.role, email: u.email, totp_secret: 'KRSXG5CTMVRXEZLU', otpauth_uri: 'otpauth://totp/Jungle%20Monkey:reset?secret=KRSXG5CTMVRXEZLU' })
  }),
  http.post(`${API}/me/totp/confirm`, async ({ request }) => {
    const b = (await request.json()) as { totp: string }
    if (!/^\d{6}$/.test(b.totp) || b.totp === '000000') return err(400, 'bad_code', 'invalid or expired code')
    return HttpResponse.json({ ok: true })
  }),

  http.get(`${API}/me/credentials`, () => {
    const u = currentUser()
    if (!u) return err(409, 'team_off', 'team mode only')
    return HttpResponse.json({ items: (state.creds[u.id] ?? []).map(({ value: _v, ...c }) => (void _v, c)), shared_key_fallback: state.fallback })
  }),
  http.put(`${API}/me/credentials/:provider`, async ({ request, params }) => {
    const u = currentUser()
    if (!u) return err(409, 'team_off', 'team mode only')
    const provider = params.provider as Agent
    const b = (await request.json()) as { kind: 'oauth_token' | 'api_key'; value: string; label?: string }
    if (b.kind === 'oauth_token' && provider !== 'claude') return err(400, 'invalid', 'oauth_token is for claude only')
    const value = b.value.trim()
    if (!value || /\s/.test(value)) return err(400, 'invalid', 'the key looks wrong')
    const c: MockCred = { provider, kind: b.kind, last4: `…${value.slice(-4)}`, label: b.label ?? '', created_at: nowIso(), last_used_at: null, value }
    state.creds[u.id] = [...(state.creds[u.id] ?? []).filter((x) => x.provider !== provider), c]
    audit(u.id, 'key.add', 'credential', provider, { kind: b.kind })
    save()
    const { value: _v, ...out } = c
    void _v
    return HttpResponse.json(out)
  }),
  http.delete(`${API}/me/credentials/:provider`, ({ params }) => {
    const u = currentUser()
    if (!u) return err(409, 'team_off', 'team mode only')
    state.creds[u.id] = (state.creds[u.id] ?? []).filter((x) => x.provider !== params.provider)
    audit(u.id, 'key.remove', 'credential', String(params.provider))
    save()
    return HttpResponse.json({ ok: true })
  }),
  http.post(`${API}/me/credentials/:provider/test`, ({ params }) => {
    const u = currentUser()
    const c = u && (state.creds[u.id] ?? []).find((x) => x.provider === params.provider)
    if (!c) return err(404, 'not_found', 'no key')
    return HttpResponse.json(c.kind === 'oauth_token' ? { status: 'not_testable', message: 'Subscription tokens cannot be checked without running the agent.' } : { status: 'ok', message: 'The key works.' })
  }),
  http.post(`${API}/me/telegram/link`, () => {
    if (!currentUser()) return err(409, 'team_off', 'team mode only')
    return HttpResponse.json({ code: '123456', command: '/link 123456', expires_at: new Date(Date.now() + 600_000).toISOString(), bot_enabled: true })
  }),
  http.delete(`${API}/me/telegram/link`, () => {
    const u = currentUser()
    if (!u) return err(409, 'team_off', 'team mode only')
    Object.assign(u, { telegram_user_id: null, telegram_linked: false })
    save()
    return HttpResponse.json({ ok: true })
  }),
  http.get(`${API}/audit`, ({ request }) => {
    const a = admin()
    if (a instanceof Response) return a
    const action = new URL(request.url).searchParams.get('action')
    return HttpResponse.json({ items: state.audit.filter((e) => !action || e.action === action), next_cursor: '' })
  }),
]
