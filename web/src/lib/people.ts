import { useCallback, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useMe } from '@/api/hooks'
import { keys } from '@/api/keys'
import { api, call, CSRF } from '@/api/client'
import type { SessionInfo } from '@/api/types'
import { storage } from './storage'

export type Scope = 'mine' | 'everyone'

/**
 * The Mine | Everyone filter and the person filter (team mode). Mine |
 * Everyone is saved on the server per user (`task_scope`; default Everyone for
 * admins, Mine for others); the person pick is remembered in this browser.
 * `createdBy` is the user id lists should keep, undefined for everyone.
 */
export function usePeopleScope() {
  const me = useMe().data
  const qc = useQueryClient()
  // Not useUpdateProfile: this runs in the initial bundle, api/team.ts does not.
  const save = useMutation({ mutationFn: (task_scope: Scope) => call(api.PATCH('/api/v1/me', { params: { header: CSRF }, body: { task_scope } })) })
  const user = me?.team_enabled ? me.user : undefined
  const scope: Scope = user ? (user.task_scope ?? (user.role === 'admin' ? 'everyone' : 'mine')) : 'everyone'
  const personKey = user ? `fleetpocket.person.${user.id}` : ''
  const [person, setPersonState] = useState<number | undefined>(() => {
    const v = personKey ? Number(storage.get(personKey)) : NaN
    return Number.isFinite(v) && v > 0 ? v : undefined
  })

  const setScope = useCallback(
    (next: Scope) => {
      qc.setQueryData<SessionInfo>(keys.me, (prev) => (prev?.user ? { ...prev, user: { ...prev.user, task_scope: next } } : prev))
      save.mutate(next)
    },
    [qc, save],
  )

  const setPerson = useCallback(
    (id: number | undefined) => {
      setPersonState(id)
      if (personKey) storage.set(personKey, id ? String(id) : null)
    },
    [personKey],
  )

  const createdBy = !user ? undefined : scope === 'mine' ? user.id : person
  return { team: !!user, scope, setScope, person, setPerson, createdBy, userId: user?.id }
}
