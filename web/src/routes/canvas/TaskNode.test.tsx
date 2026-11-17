import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router'
import type { ApiEvent, Task, TaskView } from '@/api/types'

const state = vi.hoisted(() => ({
  view: null as unknown,
  events: [] as unknown[],
  answer: vi.fn(),
}))

vi.mock('@/api/hooks', () => ({
  useTask: () => ({ data: state.view }),
  useTaskEvents: () => ({ data: { items: state.events, local: [] }, isPending: false }),
  useAnswer: () => ({ mutate: state.answer, isPending: false }),
  useSendMessage: () => ({ mutate: vi.fn(), isPending: false }),
  useTaskAction: () => ({ mutate: vi.fn(), isPending: false }),
}))
vi.mock('@/realtime/RealtimeProvider', () => ({ useTaskSubscription: () => {} }))

const { TaskNode } = await import('./TaskNode')

let id = 0
function ev(type: string, payload: Record<string, unknown>): ApiEvent {
  id++
  return { id, task_id: 1, run_id: 3, seq: id, type, payload: { type, ...payload }, created_at: '2026-11-17T10:00:00Z' } as unknown as ApiEvent
}

function task(over: Partial<TaskView>): TaskView {
  return {
    id: 1,
    project_id: 'p',
    repository_id: 1,
    title: 'Retry webhooks',
    prompt: '',
    status: 'running',
    cost_usd_total: 0.1,
    turns: 1,
    source: 'web',
    agent: 'claude',
    node: null,
    created_at: '2026-11-17T10:00:00Z',
    updated_at: '2026-11-17T10:00:00Z',
    last_activity: null,
    last_activity_at: null,
    queued_message: null,
    latest_run: { id: 3, task_id: 1, kind: 'initial', input: '', last_seq: 0, cost_usd: 0, turns: 0, started_at: '2026-11-17T10:00:00Z', finished_at: null },
    ...over,
  } as TaskView
}

function show(view: TaskView) {
  state.view = view
  return render(
    <MemoryRouter>
      <TaskNode
        task={view as Task}
        win={{ x: 0, y: 0, w: 440, h: 320 }}
        host={{ name: 'Server', online: true, accent: '#000' }}
        focused={false}
        z={1}
        level="full"
        onDragStart={() => {}}
        onFocus={() => {}}
        onToggleMin={() => {}}
        onHide={() => {}}
      />
    </MemoryRouter>,
  )
}

const work = () => [
  ev('tool_use', { tool: 'Read', summary: 'Read src/a.ts' }),
  ev('tool_result', { tool: 'Read', ok: true }),
  ev('tool_use', { tool: 'Edit', summary: 'Edit src/b.ts' }),
  ev('tool_result', { tool: 'Edit', ok: true }),
  ev('usage', { cost_usd_total: 0.42, input_tokens: 1, output_tokens: 1, turns: 2 }),
  ev('tool_use', { tool: 'Bash', summary: 'Bash: npm test' }),
]

beforeEach(() => {
  state.answer.mockReset()
})

describe('TaskNode', () => {
  it('renders a running task', () => {
    state.events = work()
    show(task({ status: 'running' }))
    const node = screen.getByRole('region', { name: /Retry webhooks/ })
    expect(within(node).getByText('TESTING')).toBeInTheDocument()
    expect(within(node).getAllByText('npm test').length).toBeGreaterThan(0)
    expect(within(node).getByText('STEP 3')).toBeInTheDocument()
    expect(within(node).getAllByText('$0.42').length).toBeGreaterThan(0)
    expect(within(node).getByLabelText('READ: 1')).toBeInTheDocument()
    expect(within(node).getByLabelText('SHELL: 1')).toBeInTheDocument()
    expect(within(node).getByText('src/b.ts')).toBeInTheDocument()
    expect(within(node).getByRole('textbox', { name: 'Message…' })).toBeInTheDocument()
  })

  it('answers the open question inline', async () => {
    state.events = [...work(), ev('question', { question_id: 'q1', text: 'How many attempts?', options: ['3 attempts', '5 attempts'] })]
    show(task({ status: 'waiting_input', open_question: { id: '3-q1', task_id: 1, run_id: 3, text: 'How many attempts?', options: ['3 attempts', '5 attempts'], created_at: '2026-11-17T10:00:00Z' } }))
    expect(screen.getByText('ASKING YOU')).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Reply…' })).toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: '5 attempts' }))
    expect(state.answer).toHaveBeenCalledWith({ question_id: '3-q1', text: '5 attempts' }, expect.anything())
  })

  it('renders a finished task with its PR', () => {
    state.events = [...work(), ev('committed', { sha: 'abcdef1', files_changed: 2, insertions: 30, deletions: 4, branch: 'b' })]
    show(task({ status: 'pr_open', pr_number: 12, pr_url: 'https://github.com/o/r/pull/12', ci_status: 'success' }))
    expect(screen.getByText('PR #12')).toBeInTheDocument()
    expect(screen.getByText('CI success')).toBeInTheDocument()
    expect(screen.getByText('+30 −4')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Open pull request →' })).toHaveAttribute('href', 'https://github.com/o/r/pull/12')
    expect(screen.queryByRole('textbox')).toBeNull()
  })

  it('renders a failed task with its error', () => {
    state.events = [...work(), ev('error', { code: 'agent_crashed', message: 'exit status 2' })]
    show(task({ status: 'failed' }))
    expect(screen.getByText('FAILED')).toBeInTheDocument()
    expect(screen.getByText('exit status 2')).toBeInTheDocument()
  })
})
