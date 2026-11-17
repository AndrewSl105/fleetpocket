import type { ApiEvent, Task } from '@/api/types'
import { addEvent, db, pendingRuns, setStatus, startRun } from './db'
import { deliveryBranch, DIFFSTAT, isLocalTask, repoOfTask, repoState } from './localdb'

// Drives running tasks in mock mode so the live timeline has something to show.

type Push = { event: (taskId: number, e: ApiEvent) => void; task: (t: Task) => void }
let push: Push = { event: () => {}, task: () => {} }

export function setPush(p: Push) {
  push = p
}

function emit(taskId: number, type: string, payload: Record<string, unknown>, runner = true) {
  push.event(taskId, addEvent(taskId, type, payload, runner))
}

function emitStatus(task: Task, status: Task['status'], reason?: string) {
  const events = db.events.get(task.id) ?? []
  const before = events.length
  setStatus(task, status, reason)
  // setStatus may add more than one event (a drained queued message).
  for (const e of (db.events.get(task.id) ?? []).slice(before)) push.event(task.id, e)
  push.task(task)
}

/** Ends the current run early: done, then push like a finished run. */
function interruptRun(task: Task) {
  setTimeout(() => {
    emit(task.id, 'done', { session_id: 's', summary: 'Interrupted by the user.', no_changes: false, cost_usd_total: task.cost_usd_total })
    progress.set(task.id, SCRIPT.length + 1)
    emitStatus(task, 'pushing')
  }, 800)
}

const SCRIPT: Array<(task: Task) => void> = [
  (t) => emit(t.id, 'tool_use', { tool: 'Grep', summary: 'Grep "LIMIT" src/audit' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Grep', ok: true }),
  (t) => emit(t.id, 'assistant_text', { text: 'The endpoint loads the whole table. I will switch it to **keyset pagination** on `(created_at, id)`.' }),
  (t) => emit(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit src/audit/routes.ts' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Edit', ok: true }),
  (t) => emit(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit src/audit/repository.ts' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Edit', ok: true }),
  (t) => {
    t.cost_usd_total = Math.round((t.cost_usd_total + 0.21) * 100) / 100
    emit(t.id, 'usage', { cost_usd_total: t.cost_usd_total, input_tokens: 52000, output_tokens: 3100, turns: 9 })
    push.task(t)
  },
  (t) => emit(t.id, 'tool_use', { tool: 'Bash', summary: 'Bash pnpm test audit' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Bash', ok: false, summary: 'FAIL src/audit/routes.test.ts › returns next cursor' }),
  (t) => emit(t.id, 'assistant_text', { text: 'One test expects `next_cursor` to be empty on the last page. Fixing the boundary check.' }),
  (t) => emit(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit src/audit/routes.ts' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Edit', ok: true }),
  (t) => emit(t.id, 'tool_use', { tool: 'Bash', summary: 'Bash pnpm test audit' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Bash', ok: true }),
]

/**
 * Local-repository tasks (mock local mode): a short run that asks one question
 * on its first run, then commits; delivery makes the task `branch_ready`.
 */
const LOCAL_SCRIPT: Array<(task: Task) => void> = [
  (t) => emit(t.id, 'tool_use', { tool: 'Read', summary: 'Read src/auth/login.ts' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Read', ok: true }),
  (t) => {
    if (asked.has(t.id)) return
    asked.add(t.id)
    const run = (db.runs.get(t.id) ?? []).at(-1)
    const text = 'Should failed logins be limited per IP address or per account?'
    const options = ['Per IP address', 'Per account']
    emit(t.id, 'question', { question_id: 'q1', text, options })
    db.questions.set(t.id, [
      ...(db.questions.get(t.id) ?? []),
      { id: `${run?.id ?? 0}-q1`, task_id: t.id, run_id: run?.id ?? 0, text, options, answer: null, answered_via: null, created_at: new Date().toISOString(), answered_at: null },
    ])
    emitStatus(t, 'waiting_input')
  },
  (t) => emit(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit src/auth/login.ts' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Edit', ok: true }),
  (t) => {
    t.cost_usd_total = Math.round((t.cost_usd_total + 0.18) * 100) / 100
    emit(t.id, 'usage', { cost_usd_total: t.cost_usd_total, input_tokens: 38000, output_tokens: 2200, turns: 6 })
    push.task(t)
  },
  (t) => emit(t.id, 'tool_use', { tool: 'Bash', summary: 'Bash npm test auth' }),
  (t) => emit(t.id, 'tool_result', { tool: 'Bash', ok: true }),
]
const asked = new Set<number>()

/**
 * Delivery into the owner's repository (docs/API.md "Local repositories"):
 * fetch as jm/<id>-<slug> and become branch_ready; a follow-up fast-forwards
 * the delivered branch. Waits (pushing) while the folder is missing.
 */
function deliverLocal(task: Task) {
  const repo = repoOfTask(task)
  const git = repo && repoState(repo.path)
  if (!repo || !git) {
    const reason = `repository folder not found: ${repo?.path ?? ''}`
    if (task.status_reason !== reason) emitStatus(task, 'pushing', reason)
    return
  }
  const updated = !!task.local_branch && git.branches.has(task.local_branch) && git.branch !== task.local_branch
  const previous = task.local_branch && !updated && git.branches.has(task.local_branch) ? task.local_branch : undefined
  const branch = updated ? task.local_branch! : deliveryBranch(task, git)
  git.branches.add(branch)
  task.local_branch = branch
  const commits = (db.events.get(task.id) ?? []).filter((e) => e.type === 'committed').length || 1
  emit(task.id, 'branch_ready', { branch, commits, diffstat: DIFFSTAT, updated, ...(previous ? { previous } : {}) }, false)
  emitStatus(task, 'branch_ready')
}

const progress = new Map<number, number>()
let timer: ReturnType<typeof setInterval> | undefined

export function startSimulator(intervalMs = 2500) {
  if (timer) return
  timer = setInterval(() => {
    for (const task of db.tasks.filter((t) => t.status === 'running')) {
      const step = progress.get(task.id) ?? 0
      const script = isLocalTask(task) ? LOCAL_SCRIPT : SCRIPT
      if (step < script.length) {
        progress.set(task.id, step + 1)
        script[step](task)
      } else if (step === script.length) {
        emit(task.id, 'committed', { sha: Math.random().toString(16).slice(2, 12), files_changed: 3, insertions: 74, deletions: 19 })
        if (task.loop && task.loop_state === 'running') {
          // A loop's check passes in the first iteration in mock mode.
          const n = task.loop_iteration || 1
          emit(task.id, 'check', { command: task.loop.check_command, exit_code: 0, output: 'ok', duration_ms: 2100, timed_out: false, iteration: n, max_iterations: task.loop.max_iterations })
          emit(task.id, 'done', { session_id: 's', summary: 'The check passes.', no_changes: false, cost_usd_total: task.cost_usd_total })
          Object.assign(task, { loop_state: 'passed' })
          emit(task.id, 'loop', { phase: 'stopped', state: 'passed', iteration: n, max_iterations: task.loop.max_iterations, reason: `check passed in iteration ${n}/${task.loop.max_iterations}` }, false)
        } else {
          const summary = script === LOCAL_SCRIPT ? 'Limited failed logins with a token bucket; tests pass.' : 'Switched the audit log to keyset pagination; tests pass.'
          emit(task.id, 'done', { session_id: 's', summary, no_changes: false, cost_usd_total: task.cost_usd_total })
        }
        emitStatus(task, 'pushing')
        progress.set(task.id, step + 1)
      }
    }
    for (const task of db.tasks.filter((t) => t.status === 'pushing' && isLocalTask(t))) deliverLocal(task)
    for (const task of db.tasks.filter((t) => t.status === 'pushing' && !isLocalTask(t))) {
      const n = 40 + task.id
      Object.assign(task, { pr_number: n, pr_url: `https://github.com/andrewsl105/example/pull/${n}`, ci_status: 'pending' })
      emit(task.id, 'pr_opened', { number: n, url: task.pr_url }, false)
      emitStatus(task, 'pr_open')
    }
    // A task that waits for a monthly budget stays queued (the owner's override clears budget_wait).
    for (const task of db.tasks.filter((t) => (t.status === 'queued' && !t.budget_wait) || t.status === 'preparing')) {
      const next = pendingRuns.get(task.id)
      if (task.status === 'preparing' && next) {
        pendingRuns.delete(task.id)
        startRun(task.id, next.kind, next.input)
        const events = db.events.get(task.id)!
        push.event(task.id, events[events.length - 1])
        progress.set(task.id, 0)
      }
      // A local task's follow-up (POST /message) starts its script again.
      if (task.status === 'queued' && isLocalTask(task)) progress.set(task.id, 0)
      emitStatus(task, task.status === 'queued' ? 'preparing' : 'running')
    }
  }, intervalMs)
}

export function resumeAfterAnswer(task: Task) {
  progress.set(task.id, Math.max(progress.get(task.id) ?? 0, 0))
  emitStatus(task, 'running')
}

export function stopSimulator() {
  clearInterval(timer)
  timer = undefined
  progress.clear()
  asked.clear()
}

export { emitStatus, interruptRun }
