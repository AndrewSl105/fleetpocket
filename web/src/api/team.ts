import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, call, CSRF } from './client'
import { keys } from './keys'
import { useMe } from './hooks'
import type { Agent, Me, Role } from './types'

// Team mode (docs/API.md "Teams"). Every call goes through client.ts.

export const teamKeys = {
  status: ['team', 'status'] as const,
  users: ['team', 'users'] as const,
  invites: ['team', 'invites'] as const,
  audit: (action: string) => ['team', 'audit', action] as const,
  credentials: ['me', 'credentials'] as const,
}

/** The signed-in user in team mode; undefined in single-owner mode (or while /me loads). */
export function useCurrentUser(): Me | undefined {
  return useMe().data?.user
}

/** True when team mode is on. */
export function useTeamEnabled(): boolean {
  return !!useMe().data?.team_enabled
}

/** What the signed-in person may do; in single-owner mode the owner may do everything. */
export function useRole(): { role: Role; team: boolean; canWrite: boolean; isAdmin: boolean; userId?: number } {
  const me = useMe().data
  const user = me?.user
  const role: Role = me?.team_enabled && user ? user.role : 'admin'
  return { role, team: !!me?.team_enabled, canWrite: role !== 'viewer', isAdmin: role === 'admin', userId: user?.id }
}

/** May the signed-in person act on a task (own, or admin)? */
export function useCanActOn(createdBy?: { id: number } | null): boolean {
  const r = useRole()
  if (!r.team || r.isAdmin) return true
  return r.canWrite && !!createdBy && createdBy.id === r.userId
}

export function useTeamStatus(enabled = true) {
  return useQuery({
    queryKey: teamKeys.status,
    queryFn: ({ signal }) => call(api.GET('/api/v1/team', { signal })),
    enabled,
  })
}

function useInvalidateMe() {
  const qc = useQueryClient()
  return () => {
    void qc.invalidateQueries({ queryKey: keys.me })
    void qc.invalidateQueries({ queryKey: ['team'] })
  }
}

export function useEnableTeam() {
  const done = useInvalidateMe()
  return useMutation({
    mutationFn: (body: { email: string; name: string }) => call(api.POST('/api/v1/team/enable', { params: { header: CSRF }, body })),
    onSuccess: done,
  })
}

export function useDisableTeam() {
  const done = useInvalidateMe()
  return useMutation({
    mutationFn: () => call(api.POST('/api/v1/team/disable', { params: { header: CSRF } })),
    onSuccess: done,
  })
}

export function useSetFallback() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (on: boolean) => call(api.PATCH('/api/v1/team', { params: { header: CSRF }, body: { shared_key_fallback: on } })),
    onSuccess: (data) => qc.setQueryData(teamKeys.status, data),
  })
}

/** Everyone in the team (members and viewers get names and roles only). */
export function useUsers(enabled = true) {
  return useQuery({
    queryKey: teamKeys.users,
    queryFn: ({ signal }) => call(api.GET('/api/v1/users', { signal })),
    select: (d) => d.items,
    enabled,
    staleTime: 60_000,
  })
}

export function useUpdateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...body }: { id: number; role?: Role; disabled?: boolean; live_tasks?: 'finish' | 'cancel'; monthly_budget_usd?: number; clear_budget?: boolean }) =>
      call(api.PATCH('/api/v1/users/{id}', { params: { path: { id }, header: CSRF }, body })),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['team'] })
      void qc.invalidateQueries({ queryKey: keys.tasks.all })
    },
  })
}

export function useRemoveUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => call(api.DELETE('/api/v1/users/{id}', { params: { path: { id }, header: CSRF } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['team'] }),
  })
}

export function useInvites(enabled = true) {
  return useQuery({
    queryKey: teamKeys.invites,
    queryFn: ({ signal }) => call(api.GET('/api/v1/invites', { signal })),
    select: (d) => d.items,
    enabled,
  })
}

export function useCreateInvite() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: { role: Role; email?: string | null; expires_in_hours?: number }) =>
      call(api.POST('/api/v1/invites', { params: { header: CSRF }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: teamKeys.invites }),
  })
}

export function useRevokeInvite() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => call(api.DELETE('/api/v1/invites/{id}', { params: { path: { id }, header: CSRF } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: teamKeys.invites }),
  })
}

export function useAudit(action: string, enabled = true) {
  return useQuery({
    queryKey: teamKeys.audit(action),
    queryFn: ({ signal }) => call(api.GET('/api/v1/audit', { params: { query: { action: action || undefined, limit: 50 } }, signal })),
    enabled,
  })
}

// ---- me ----

export function useUpdateProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (body: {
      name?: string
      email?: string
      github_login?: string
      commit_email?: string
      task_scope?: 'mine' | 'everyone'
      current_password?: string
      new_password?: string
    }) => call(api.PATCH('/api/v1/me', { params: { header: CSRF }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.me }),
  })
}

export function useStartTotpReset() {
  return useMutation({ mutationFn: () => call(api.POST('/api/v1/me/totp', { params: { header: CSRF } })) })
}

export function useConfirmTotpReset() {
  return useMutation({
    mutationFn: (body: { enrollment_id: string; password: string; totp: string }) =>
      call(api.POST('/api/v1/me/totp/confirm', { params: { header: CSRF }, body })),
  })
}

export function useCredentials(enabled = true) {
  return useQuery({
    queryKey: teamKeys.credentials,
    queryFn: ({ signal }) => call(api.GET('/api/v1/me/credentials', { signal })),
    enabled,
  })
}

export function usePutCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ provider, ...body }: { provider: Agent; kind: 'oauth_token' | 'api_key'; value: string; label?: string }) =>
      call(api.PUT('/api/v1/me/credentials/{provider}', { params: { path: { provider }, header: CSRF }, body })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: teamKeys.credentials }),
  })
}

export function useDeleteCredential() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (provider: Agent) => call(api.DELETE('/api/v1/me/credentials/{provider}', { params: { path: { provider }, header: CSRF } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: teamKeys.credentials }),
  })
}

export function useTestCredential() {
  return useMutation({
    mutationFn: (provider: Agent) => call(api.POST('/api/v1/me/credentials/{provider}/test', { params: { path: { provider }, header: CSRF } })),
  })
}

export function useTelegramLinkCode() {
  return useMutation({ mutationFn: () => call(api.POST('/api/v1/me/telegram/link', { params: { header: CSRF } })) })
}

export function useTelegramUnlink() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => call(api.DELETE('/api/v1/me/telegram/link', { params: { header: CSRF } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.me }),
  })
}

export function useShareNode() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, shared }: { id: string; shared: boolean }) =>
      call(api.PUT('/api/v1/nodes/{id}/shared', { params: { path: { id }, header: CSRF }, body: { shared } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: keys.nodes }),
  })
}

export function usePromoteSkill() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => call(api.POST('/api/v1/skills/{id}/promote', { params: { path: { id }, header: CSRF } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['skills'] }),
  })
}

// ---- signup (no session) ----

export function signupStart(token: string) {
  return call(api.POST('/api/v1/signup/start', { params: { header: CSRF }, body: { token } }))
}

export function signup(body: { token: string; enrollment_id: string; name: string; email: string; password: string; totp: string }) {
  return call(api.POST('/api/v1/signup', { params: { header: CSRF }, body }))
}
