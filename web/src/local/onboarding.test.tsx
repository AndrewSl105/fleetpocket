import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { FolderListing, LocalRepoSuggestion } from '@/api/types'

const state = vi.hoisted(() => ({
  health: { isPending: false, isFetching: false, data: { ok: true, checks: { docker: 'ok' } as Record<string, string> }, refetch: vi.fn() },
  me: { isPending: false, isFetching: false, data: { agents: ['claude'] as string[] }, refetch: vi.fn() },
  suggestions: { items: [] as LocalRepoSuggestion[], start: null as string | null },
  folders: {} as Record<string, FolderListing>,
  add: vi.fn(),
}))

vi.mock('@/api/hooks', () => ({
  errorMessage: (e: unknown) => String(e),
  useHealth: () => state.health,
  useMe: () => state.me,
  useCreateTask: () => ({ mutate: vi.fn(), isPending: false }),
}))
vi.mock('@/api/local', () => ({
  useLocalSuggestions: () => ({ isPending: false, isError: false, data: state.suggestions }),
  useAddLocalRepo: () => ({ mutate: state.add, isPending: false }),
  useLocalFolder: (path: string) => ({ data: state.folders[path || '~'], isError: false, isFetching: false, isPlaceholderData: false }),
  useLocalGitHub: () => ({ isPending: false, data: { connected: false, device_flow: true, gh_cli: false, error: '', login: '', scopes: [] } }),
}))

const { DockerStep, AgentStep, FolderStep, initialOnboardingStep } = await import('./OnboardingPage')
const { FolderBrowser } = await import('./FolderBrowser')
const { shouldOnboard } = await import('./onboarding')

const home = '/Users/me'
function listing(path: string, isGit: boolean, entries: Array<[string, boolean]>): FolderListing {
  return {
    path,
    home,
    parent: path === home ? null : path.slice(0, path.lastIndexOf('/')),
    is_git: isGit,
    entries: entries.map(([name, git]) => ({ name, path: `${path}/${name}`, is_git: git })),
  }
}

beforeEach(() => {
  state.health.data = { ok: true, checks: { docker: 'ok' } }
  state.me.data = { agents: ['claude'] }
  state.add.mockReset()
  state.suggestions = { items: [], start: null }
})

describe('onboarding', () => {
  it('starts at Docker, or at the folder from "Add a folder"', () => {
    expect(initialOnboardingStep(new URLSearchParams(''))).toBe('docker')
    expect(initialOnboardingStep(new URLSearchParams('step=folder'))).toBe('folder')
  })

  it('redirects only local mode without a local repository, from the first screen', () => {
    expect(shouldOnboard('local', false, '/')).toBe(true)
    expect(shouldOnboard('local', true, '/')).toBe(false)
    expect(shouldOnboard('local', undefined, '/')).toBe(false)
    expect(shouldOnboard('local', false, '/tasks/3')).toBe(false)
    expect(shouldOnboard('server', false, '/')).toBe(false)
  })

  it('Docker step: ✓ when running, else how to fix it', async () => {
    const next = vi.fn()
    const { unmount } = render(<DockerStep onNext={next} />)
    expect(screen.getByText('Docker is running.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Next' }))
    expect(next).toHaveBeenCalled()
    unmount()
    state.health.data = { ok: false, checks: { docker: 'unreachable' } }
    render(<DockerStep onNext={next} />)
    expect(screen.getByText(/Docker is not reachable \(unreachable\)/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Check again' }))
    expect(state.health.refetch).toHaveBeenCalled()
  })

  it('Agent step: names the detected logins, or explains how to add one', () => {
    state.me.data = { agents: ['claude', 'codex'] }
    const { unmount } = render(<AgentStep onNext={() => {}} />)
    expect(screen.getByText('Agent login found: Claude, Codex.')).toBeInTheDocument()
    unmount()
    state.me.data = { agents: [] }
    render(<AgentStep onNext={() => {}} />)
    expect(screen.getByText('No agent login found yet.')).toBeInTheDocument()
    expect(screen.getByText(/ANTHROPIC_API_KEY/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Next' })).toBeDisabled()
  })

  it('Folder step: suggestions with branch, dirty state and "This folder" first', async () => {
    state.suggestions = {
      start: `${home}/code/app`,
      items: [
        { name: 'site', path: `${home}/code/site`, branch: 'dev', dirty: true, last_commit: new Date().toISOString(), added: false },
        { name: 'app', path: `${home}/code/app`, branch: 'main', dirty: false, last_commit: new Date().toISOString(), added: true },
      ],
    }
    render(<FolderStep onAdded={() => {}} />)
    const rows = screen.getAllByRole('button', { name: /code\// })
    expect(rows[0]).toHaveTextContent('app')
    expect(rows[0]).toHaveTextContent('This folder')
    expect(rows[0]).toHaveTextContent('added')
    expect(rows[1]).toHaveTextContent('⎇ dev')
    expect(rows[1]).toHaveTextContent('uncommitted changes')
    await userEvent.click(rows[1])
    expect(state.add).toHaveBeenCalledWith({ path: `${home}/code/site` }, expect.anything())
  })
})

describe('FolderBrowser', () => {
  it('walks folders, marks git repositories and uses only a repository', async () => {
    state.folders = {
      '~': listing(home, false, [['code', false]]),
      [home]: listing(home, false, [['code', false]]),
      [`${home}/code`]: listing(`${home}/code`, false, [['app', true], ['notes', false]]),
      [`${home}/code/app`]: listing(`${home}/code/app`, true, []),
    }
    const onPick = vi.fn()
    render(
      <MemoryRouter>
        <FolderBrowser onPick={onPick} />
      </MemoryRouter>,
    )
    expect(screen.getByRole('button', { name: 'Up one folder' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: /code/ }))
    expect(screen.getByRole('button', { name: /app.*git/ })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Use this folder' })).toBeDisabled()
    expect(screen.getByText('This folder is not a git repository.')).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: /app/ }))
    expect(screen.getByRole('navigation', { name: 'Breadcrumb' })).toHaveTextContent('~/code/app')
    await userEvent.click(screen.getByRole('button', { name: 'Use this folder' }))
    expect(onPick).toHaveBeenCalledWith(`${home}/code/app`)
    await userEvent.click(screen.getByRole('button', { name: '~' }))
    expect(screen.getByRole('button', { name: /code/ })).toBeInTheDocument()
  })
})
