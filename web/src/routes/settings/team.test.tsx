import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, renderHook, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import type { SessionInfo, Task } from '@/api/types'

const state = vi.hoisted(() => ({ me: undefined as unknown, creds: undefined as unknown }))

vi.mock('@/api/hooks', () => ({
  useMe: () => ({ data: state.me }),
  errorMessage: (e: unknown) => String(e),
}))
vi.mock('@/api/team', async (orig) => {
  const real = await orig<typeof import('@/api/team')>()
  const idle = { mutate: vi.fn(), mutateAsync: vi.fn(), isPending: false }
  return {
    ...real,
    useCredentials: () => ({ data: state.creds }),
    usePutCredential: () => idle,
    useDeleteCredential: () => idle,
    useTestCredential: () => idle,
    useEnableTeam: () => idle,
    useTeamStatus: () => ({ data: { enabled: false, shared_key_fallback: false, secrets_key: true, server_keys: ['claude'] } }),
  }
})

const { useCanActOn, useRole } = await import('@/api/team')
const { MyKeysSection } = await import('./MyKeysSection')
const { TeamSection } = await import('./TeamSection')
const { StatusBadge } = await import('@/components/StatusBadge')
const { can, composerMode, GROUPS } = await import('@/lib/status')
const { matchesFilter } = await import('@/api/cache')

const member = { id: 2, email: 'm@x.co', name: 'Mia Member', role: 'member', initials: 'MM' }
const session = (user?: unknown, team = true) => ({ ok: true, kind: 'web', team_enabled: team, user }) as unknown as SessionInfo

beforeEach(() => {
  state.me = undefined
  state.creds = undefined
})

describe('needs_key', () => {
  it('is an active, retryable state without a composer', () => {
    expect(GROUPS.active).toContain('needs_key')
    expect(can('needs_key', 'retry')).toBe(true)
    expect(can('needs_key', 'cancel')).toBe(true)
    expect(composerMode('needs_key')).toBeNull()
  })
  it('has its own stamp', () => {
    render(<StatusBadge status="needs_key" />)
    expect(screen.getByText('Needs key')).toBeInTheDocument()
  })
})

describe('roles', () => {
  it('single-owner mode may do everything', () => {
    state.me = session(undefined, false)
    expect(renderHook(() => useRole()).result.current).toMatchObject({ team: false, isAdmin: true, canWrite: true })
    expect(renderHook(() => useCanActOn(null)).result.current).toBe(true)
  })
  it('members act on their own tasks only; viewers on none', () => {
    state.me = session(member)
    expect(renderHook(() => useCanActOn({ id: 2 })).result.current).toBe(true)
    expect(renderHook(() => useCanActOn({ id: 1 })).result.current).toBe(false)
    state.me = session({ ...member, role: 'viewer' })
    expect(renderHook(() => useRole()).result.current.canWrite).toBe(false)
    expect(renderHook(() => useCanActOn({ id: 2 })).result.current).toBe(false)
  })
})

describe('Mine / person filter in the task cache', () => {
  const t = (createdBy: number | null) => ({ id: 1, status: 'running', project_id: 'p', created_by: createdBy ? { id: createdBy, name: 'x', initials: 'X' } : null }) as unknown as Task
  it('keeps one person when createdBy is set', () => {
    expect(matchesFilter(t(2), { group: 'active', createdBy: 2 })).toBe(true)
    expect(matchesFilter(t(3), { group: 'active', createdBy: 2 })).toBe(false)
    expect(matchesFilter(t(null), { group: 'active' })).toBe(true)
  })
})

describe('Settings', () => {
  it('single-owner admins see only the Create a team card; local mode and members nothing', () => {
    const { rerender, container } = render(<TeamSection teamEnabled={false} isAdmin local={false} />, { wrapper: MemoryRouter })
    expect(screen.getByRole('heading', { name: 'Create a team' })).toBeInTheDocument()
    rerender(<TeamSection teamEnabled={false} isAdmin local />)
    expect(container).toBeEmptyDOMElement()
    rerender(<TeamSection teamEnabled isAdmin={false} local={false} />)
    expect(container).toBeEmptyDOMElement()
  })
  it('My keys lists every provider with only the last 4 characters', () => {
    state.me = session(member)
    state.creds = {
      shared_key_fallback: false,
      items: [{ provider: 'claude', kind: 'api_key', last4: '…4321', label: 'work', created_at: '2026-11-17T10:00:00Z', last_used_at: null }],
    }
    render(<MyKeysSection />, { wrapper: MemoryRouter })
    expect(screen.getByTestId('key-claude')).toHaveTextContent('…4321')
    expect(screen.getByTestId('key-codex')).toHaveTextContent('No key')
    expect(screen.getByTestId('key-cursor')).toHaveTextContent('No key')
    expect(screen.getByText(/needs key/i)).toBeInTheDocument()
  })
})
