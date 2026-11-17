import { http, HttpResponse } from 'msw'
import type { ApiEvent, Checkpoint, CheckpointPreview, CheckpointSettings, Task } from '@/api/types'
import { addEvent, db, makeTask, pendingRuns, startRun, taskView } from './db'
import { emitStatus } from './simulator'

// Checkpoints for mock mode (docs/API.md "Checkpoints").

export const checkpointState: { settings: CheckpointSettings } = { settings: { enabled: false } }

const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString()

type CheckpointPayload = { n: number; ref_sha: string; turn: number; files_changed: number; reason?: 'turn' | 'question' }

function checkpointEvents(taskId: number): ApiEvent[] {
  return (db.events.get(taskId) ?? []).filter((e) => e.type === 'checkpoint')
}

function unavailable(task: Task): string | undefined {
  if (['queued', 'preparing', 'running', 'waiting_input', 'pushing'].includes(task.status)) return 'The task is busy; restore once the current run has ended.'
  if (task.status === 'completed' && task.pr_number != null) return 'The pull request is closed; start a new task instead.'
  return undefined
}

/** The mock uses the checkpoint's event id as its id. */
function checkpointOf(task: Task, e: ApiEvent): Checkpoint {
  const p = e.payload as unknown as CheckpointPayload
  const reason = unavailable(task)
  return {
    id: e.id,
    task_id: task.id,
    run_id: e.run_id ?? 0,
    event_id: e.id,
    n: p.n,
    ref_sha: p.ref_sha,
    turn: p.turn,
    files_changed: p.files_changed,
    reason: p.reason ?? 'turn',
    created_at: e.created_at,
    ref: `refs/jm/checkpoints/${e.run_id}/${p.n}`,
    mode: task.agent === 'claude' ? 'fork' : 'summary',
    available: !reason,
    ...(reason ? { unavailable_reason: reason } : {}),
  }
}

const DIFF = `diff --git a/db/schema.sql b/db/schema.sql
index 3b18e51..a1c9f02 100644
--- a/db/schema.sql
+++ b/db/schema.sql
@@ -12,3 +12,9 @@ CREATE TABLE customers (
+CREATE TABLE orders (
+  id bigserial PRIMARY KEY,
+  customer_id bigint NOT NULL REFERENCES customers(id),
+  total_cents bigint NOT NULL
+);
+CREATE INDEX orders_customer ON orders (customer_id);
diff --git a/src/orders/repo.ts b/src/orders/repo.ts
new file mode 100644
--- /dev/null
+++ b/src/orders/repo.ts
@@ -0,0 +1,4 @@
+export async function listOrders(customerId: number) {
+  return db.query('SELECT * FROM orders WHERE customer_id = $1', [customerId])
+}
+
`

/** A task with three checkpoints and one restore: run 2 is the abandoned branch. */
export function seedCheckpoints() {
  // Off, as on a fresh install; this task ran while it was on.
  checkpointState.settings = { enabled: false }
  const cp = (taskId: number, n: number, turn: number, files: number, at: string, reason: 'turn' | 'question' = 'turn') =>
    addEvent(taskId, 'checkpoint', { n, ref_sha: `c0ffee${n}${'0'.repeat(33)}`, turn, session_ref: `s-orders/m${turn}`, files_changed: files, reason }, true, at)
  const t = makeTask({ project_id: 'vatary', title: 'Add an orders table', status: 'queued', created_at: ago(95), cost_usd_total: 1.12 })
  t.prompt = 'Add an orders table linked to customers, with a repository function to list a customer’s orders.'
  startRun(t.id, 'initial', t.prompt, ago(94))
  addEvent(t.id, 'started', { session_id: 's-orders', model: 'claude-opus-5-5' }, true, ago(94))
  addEvent(t.id, 'assistant_text', { text: 'I’ll add the table to db/schema.sql and a repository function next to the customers one.' }, true, ago(93))
  addEvent(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit db/schema.sql' }, true, ago(92))
  addEvent(t.id, 'tool_result', { tool: 'Edit', ok: true }, true, ago(92))
  cp(t.id, 1, 2, 1, ago(92))
  addEvent(t.id, 'tool_use', { tool: 'Write', summary: 'Write src/orders/repo.ts' }, true, ago(90))
  addEvent(t.id, 'tool_result', { tool: 'Write', ok: true }, true, ago(90))
  cp(t.id, 2, 4, 1, ago(90))
  addEvent(t.id, 'committed', { sha: '9f2c41e7d0b3', files_changed: 2, insertions: 10, deletions: 0 }, true, ago(88))
  addEvent(t.id, 'done', { session_id: 's-orders', summary: 'Added the orders table and `listOrders`.', no_changes: false, cost_usd_total: 0.52 }, true, ago(88))
  addEvent(t.id, 'pr_opened', { number: 61, url: 'https://github.com/andrewsl105/vatary-server/pull/61' }, false, ago(87))

  // A follow-up that went the wrong way, then a restore to checkpoint 2.
  const r2 = startRun(t.id, 'followup', 'Also backfill orders from the legacy CSV', ago(60))
  addEvent(t.id, 'message', { text: 'Also backfill orders from the legacy CSV', delivery: 'new_run' }, false, ago(60))
  addEvent(t.id, 'tool_use', { tool: 'Write', summary: 'Write scripts/backfill_orders.ts' }, true, ago(58))
  addEvent(t.id, 'tool_result', { tool: 'Write', ok: true }, true, ago(58))
  cp(t.id, 1, 1, 1, ago(58))
  addEvent(t.id, 'tool_use', { tool: 'Edit', summary: 'Edit db/schema.sql' }, true, ago(57))
  addEvent(t.id, 'tool_result', { tool: 'Edit', ok: true }, true, ago(57))
  addEvent(t.id, 'committed', { sha: '4be0aa1c93f2', files_changed: 2, insertions: 48, deletions: 3 }, true, ago(55))
  addEvent(t.id, 'pr_updated', { number: 61, url: 'https://github.com/andrewsl105/vatary-server/pull/61' }, false, ago(54))
  const cp2 = checkpointEvents(t.id).find((e) => (e.payload as unknown as CheckpointPayload).n === 2 && e.run_id !== r2.id)!
  addEvent(t.id, 'checkpoint_restored', {
    checkpoint_id: cp2.id, checkpoint_event_id: cp2.id, n: 2, run_id: cp2.run_id, restore_run_id: db.nextRunId, ref_sha: (cp2.payload as unknown as CheckpointPayload).ref_sha,
    mode: 'fork', instruction: 'Skip the backfill; add a migration file instead of editing schema.sql', via: 'web',
  }, false, ago(41))
  startRun(t.id, 'restore', 'Continue the task from here.', ago(40))
  addEvent(t.id, 'started', { session_id: 's-orders-fork', model: 'claude-opus-5-5' }, true, ago(40))
  addEvent(t.id, 'tool_use', { tool: 'Write', summary: 'Write db/migrations/0004_orders.sql' }, true, ago(38))
  addEvent(t.id, 'tool_result', { tool: 'Write', ok: true }, true, ago(38))
  cp(t.id, 1, 1, 1, ago(38))
  addEvent(t.id, 'committed', { sha: '71d3e0b5aa20', files_changed: 3, insertions: 16, deletions: 0 }, true, ago(36))
  addEvent(t.id, 'branch_force_pushed', { branch: t.branch, from: '4be0aa1c93f2a7b1', reason: 'restore', number: 61 }, false, ago(35))
  addEvent(t.id, 'pr_updated', { number: 61, url: 'https://github.com/andrewsl105/vatary-server/pull/61' }, false, ago(35))
  Object.assign(t, {
    status: 'pr_open', pr_number: 61, pr_url: 'https://github.com/andrewsl105/vatary-server/pull/61', ci_status: 'success',
    turns: 14, updated_at: ago(35),
  })
}

type Guard = (request: Request) => Response | null
type Err = (status: number, code: string, message: string) => Response

export function checkpointHandlers(API: string, guard: Guard, err: Err, pushEvent: (taskId: number, e: ApiEvent) => void) {
  const find = (id: unknown) => db.tasks.find((t) => t.id === Number(id))
  return [
    http.get(`${API}/settings/checkpoints`, ({ request }) => guard(request) ?? HttpResponse.json(checkpointState.settings)),
    http.put(`${API}/settings/checkpoints`, async ({ request }) => {
      const denied = guard(request)
      if (denied) return denied
      const body = (await request.json()) as CheckpointSettings
      checkpointState.settings = { enabled: !!body.enabled }
      return HttpResponse.json(checkpointState.settings)
    }),
    http.get(`${API}/tasks/:id/checkpoints`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = find(params.id)
      if (!task) return err(404, 'not_found', 'No such task')
      return HttpResponse.json({ items: checkpointEvents(task.id).map((e) => checkpointOf(task, e)) })
    }),
    http.get(`${API}/tasks/:id/checkpoints/:cid`, ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = find(params.id)
      const e = task && checkpointEvents(task.id).find((x) => x.id === Number(params.cid))
      if (!task || !e) return err(404, 'not_found', 'No such checkpoint')
      const later = (db.events.get(task.id) ?? []).filter((x) => x.id > e.id)
      const count = (...types: string[]) => later.filter((x) => types.includes(x.type)).length
      const preview: CheckpointPreview = {
        ...checkpointOf(task, e),
        discarded: { runs: count('run_started'), steps: count('tool_use'), messages: count('message', 'answer'), commits: count('committed') },
        diff: count('tool_use', 'committed') ? DIFF : '',
        truncated: false,
      }
      return HttpResponse.json(preview)
    }),
    http.post(`${API}/tasks/:id/checkpoints/:cid/restore`, async ({ request, params }) => {
      const denied = guard(request)
      if (denied) return denied
      const task = find(params.id)
      const e = task && checkpointEvents(task.id).find((x) => x.id === Number(params.cid))
      if (!task || !e) return err(404, 'not_found', 'No such checkpoint')
      const reason = unavailable(task)
      if (reason) return err(409, 'conflict', reason)
      const body = (await request.json()) as { instruction?: string }
      const p = e.payload as unknown as CheckpointPayload
      const instruction = (body.instruction ?? '').trim()
      pushEvent(task.id, addEvent(task.id, 'checkpoint_restored', {
        checkpoint_id: e.id, checkpoint_event_id: e.id, n: p.n, run_id: e.run_id, restore_run_id: 0, ref_sha: p.ref_sha,
        mode: task.agent === 'claude' ? 'fork' : 'summary', instruction, via: 'web',
      }))
      pendingRuns.set(task.id, { kind: 'restore', input: instruction || 'Continue the task from here.' })
      emitStatus(task, 'queued')
      return HttpResponse.json(taskView(task))
    }),
  ]
}
