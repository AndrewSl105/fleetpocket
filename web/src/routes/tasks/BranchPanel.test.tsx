import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { ApiError } from '@/api/errors'
import type { TaskView } from '@/api/types'

type Callbacks = { onSuccess?: () => void; onError?: (e: unknown) => void }

const state = vi.hoisted(() => ({
  result: null as unknown,
  calls: [] as string[],
  connected: true,
  confirm: vi.fn(),
}))

vi.mock('@/api/local', () => ({
  useBranchAction: () => ({
    isPending: false,
    variables: undefined,
    mutate: (kind: string, cb: Callbacks) => {
      state.calls.push(kind)
      if (state.result instanceof Error) cb.onError?.(state.result)
      else cb.onSuccess?.()
    },
  }),
  useLocalGitHub: () => ({ data: { connected: state.connected } }),
  useLocateRepo: () => ({ isPending: false, mutate: vi.fn() }),
  useLocalFolder: () => ({ data: undefined, isError: false }),
}))
vi.mock('@/components/confirm', () => ({ useConfirm: () => state.confirm }))

const { BranchPanel } = await import('./BranchPanel')

function task(over: Partial<TaskView> = {}): TaskView {
  return {
    id: 7,
    status: 'branch_ready',
    status_reason: null,
    local_branch: 'jm/7-rate-limit',
    branch: 'agent/7-rate-limit',
    repository_id: 11,
    repository: { id: 11, project_id: 'shop', full_name: 'shop-api', base_branch: 'main', installation_id: 0, is_default: true, source: 'local', path: '/Users/demo/code/shop-api', remote_url: 'git@github.com:demo/shop-api.git' },
    ...over,
  } as TaskView
}

beforeEach(() => {
  state.result = null
  state.calls = []
  state.connected = true
  state.confirm.mockReset()
})

describe('BranchPanel', () => {
  it('offers the branch actions on a delivered branch', () => {
    render(<BranchPanel task={task()} />)
    expect(screen.getByText('Branch jm/7-rate-limit is in your repository.')).toBeInTheDocument()
    for (const name of ['Merge into current branch', 'Check out', 'Discard', 'Push & open PR']) {
      expect(screen.getByRole('button', { name })).toBeInTheDocument()
    }
  })

  it('hides Push & open PR without a GitHub remote or connection', () => {
    state.connected = false
    const { unmount } = render(<BranchPanel task={task()} />)
    expect(screen.queryByRole('button', { name: 'Push & open PR' })).toBeNull()
    unmount()
    state.connected = true
    render(<BranchPanel task={task({ repository: { ...task().repository!, remote_url: '' } })} />)
    expect(screen.queryByRole('button', { name: 'Push & open PR' })).toBeNull()
  })

  it('merges, and shows a dirty working tree clearly', async () => {
    state.result = new ApiError(409, 'dirty', 'uncommitted changes')
    render(<BranchPanel task={task()} />)
    await userEvent.click(screen.getByRole('button', { name: 'Merge into current branch' }))
    expect(state.calls).toEqual(['merge'])
    expect(screen.getByRole('alert')).toHaveTextContent('Your working tree has uncommitted changes')
  })

  it('lists the files of a merge conflict', async () => {
    state.result = new ApiError(409, 'merge_conflict', 'conflict', undefined, ['src/a.ts', 'src/b.ts'])
    render(<BranchPanel task={task()} />)
    await userEvent.click(screen.getByRole('button', { name: 'Merge into current branch' }))
    const alert = screen.getByRole('alert')
    expect(alert).toHaveTextContent('Conflicting files')
    expect(alert).toHaveTextContent('src/a.ts')
    expect(alert).toHaveTextContent('src/b.ts')
  })

  it('discards only after confirming', async () => {
    state.confirm.mockResolvedValueOnce(false).mockResolvedValueOnce(true)
    render(<BranchPanel task={task()} />)
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(state.calls).toEqual([])
    await userEvent.click(screen.getByRole('button', { name: 'Discard' }))
    expect(state.confirm).toHaveBeenCalledWith(expect.stringContaining('jm/7-rate-limit'), 'Discard')
    expect(state.calls).toEqual(['discard'])
  })

  it('offers Locate… when a delivery waits for a moved folder', () => {
    render(<BranchPanel task={task({ status: 'pushing', local_branch: null, status_reason: 'repository folder not found: /Users/demo/code/old' })} />)
    expect(screen.getByRole('alert')).toHaveTextContent('repository folder not found: /Users/demo/code/old')
    expect(screen.getByRole('button', { name: /Locate…/ })).toBeInTheDocument()
  })

  it('renders nothing for other tasks', () => {
    const { container } = render(<BranchPanel task={task({ status: 'pr_open', local_branch: null })} />)
    expect(container).toBeEmptyDOMElement()
  })
})
