import type { ApiEvent, Attachment, AutoFixPayload, TaskStatus } from '@/api/types'
import { fullQuestionId } from '@/api/events'

export type TimelineItem =
  | { kind: 'claude'; key: string; text: string; at: string }
  | {
      kind: 'user'
      key: string
      text: string
      at: string
      /** queued: waiting for the run to end; dropped: the task ended first; injected: legacy timelines. */
      note?: 'queued' | 'injected' | { dropped: string }
      pending?: boolean
      attachments?: Attachment[]
    }
  /** Images attached when the task was created (the prompt itself is on the Info tab). */
  | { kind: 'attachments'; key: string; attachments: Attachment[]; at: string }
  | { kind: 'tool'; key: string; tool: string; summary: string; ok: boolean | null; error?: string; at: string }
  | {
      kind: 'question'
      key: string
      questionId: string
      text: string
      options: string[]
      answer?: { text: string; via: string; pending?: boolean }
      at: string
    }
  | { kind: 'divider'; key: string; text: string; tone?: 'muted' | 'warn'; at: string }
  | { kind: 'status'; key: string; status: TaskStatus; reason?: string | null; at: string }
  | { kind: 'commit'; key: string; sha: string; files: number; insertions: number; deletions: number; at: string }
  | { kind: 'pr'; key: string; action: 'opened' | 'updated' | 'closed'; number: number; url?: string; merged?: boolean; at: string }
  | { kind: 'ci'; key: string; status: string; name: string; url?: string; at: string }
  | { kind: 'done'; key: string; summary: string; noChanges: boolean; at: string }
  | { kind: 'error'; key: string; title: string; message: string; at: string }
  /** One call through the MCP gateway (sizes and timing only). */
  | { kind: 'mcp'; key: string; server: string; tool: string; ok: boolean; ms: number; denied?: 'denied' | 'limit'; error?: string; at: string }
  /** The egress proxy refused a host (allowlist mode). */
  | { kind: 'egress'; key: string; host: string; at: string }
  /** A checkpoint the task can be restored to (docs/API.md "Checkpoints"). */
  | { kind: 'checkpoint'; key: string; eventId: number; n: number; reason: string; files: number; at: string }
  /** The owner restored a checkpoint. */
  | { kind: 'restored'; key: string; n: number; mode: string; instruction: string; at: string }
  /** A push after a restore rewrote the branch. */
  | { kind: 'force_push'; key: string; branch: string; from: string; at: string }
  /** Local repositories: the task branch was delivered into the owner's repository. */
  | { kind: 'branch_ready'; key: string; branch: string; commits: number; diffstat: string; updated: boolean; previous?: string; at: string }
  /** Local repositories: Merge / Check out / Discard / Push & open PR. */
  | { kind: 'branch_action'; key: string; action: 'merged' | 'checked_out' | 'discarded' | 'pushed'; branch: string; into?: string; fastForward?: boolean; at: string }
  /** A loop iteration starts (docs/API.md "Loops"): the runs below belong to it. */
  | { kind: 'iteration'; key: string; n: number; max: number; command?: string; at: string }
  /** The check that ended a loop iteration. */
  | { kind: 'check'; key: string; iteration: number; max: number; exitCode: number; timedOut: boolean; output: string; command: string; ms: number; at: string }
  /** The loop ended (state says why). */
  | { kind: 'loop_end'; key: string; state: string; reason?: string; at: string }
  /** Steps a restore discarded: kept, shown collapsed and greyed. */
  | { kind: 'abandoned'; key: string; items: TimelineItem[]; at: string }
  /** One CI auto-fix step (docs/API.md "CI auto-fix"). */
  | {
      kind: 'autofix'
      key: string
      phase: AutoFixPayload['phase']
      attempt: number
      maxAttempts: number
      checks: { name: string; url: string; conclusion: string }[]
      signature?: string
      attachments: Attachment[]
      reason?: string
      reasons: string[]
      fixes: number
      at: string
    }

/**
 * Interrupt ends the run (the backend pushes what it committed): a task whose
 * current run already got an interrupt is stopping until the run finishes.
 */
export function isStopping(events: ApiEvent[], status: TaskStatus): boolean {
  if (status !== 'running' && status !== 'waiting_input') return false
  for (let i = events.length - 1; i >= 0; i--) {
    const type = events[i].type
    if (type === 'interrupt') return true
    if (type === 'run_started' || type === 'run_finished') return false
  }
  return false
}

/**
 * Maps raw events (real, then optimistic local ones) to timeline items. Pure.
 * Turns, the latest activity and the runs list come from the API (Task.turns,
 * Task.last_activity, GET /tasks/{id}/runs), not from the loaded events.
 */
export function buildTimeline(events: ApiEvent[], local: ApiEvent[] = []): TimelineItem[] {
  const items: TimelineItem[] = []
  const questions = new Map<string, Extract<TimelineItem, { kind: 'question' }>>()
  const openTools = new Map<string, Extract<TimelineItem, { kind: 'tool' }>>()
  // Queued messages wait for the run to end: the move to `queued` starts them
  // as the next run; `message_dropped` means the task ended first.
  let waiting: Extract<TimelineItem, { kind: 'user' }>[] = []

  const all = [...events, ...local]
  for (const e of all) {
    const key = String(e.id)
    const at = e.created_at
    const pending = e.id < 0
    switch (e.type) {
      case 'assistant_text': {
        const text = e.payload.text
        if (text?.trim()) items.push({ kind: 'claude', key, text, at })
        break
      }
      case 'tool_use': {
        const { tool, summary } = e.payload
        const item: Extract<TimelineItem, { kind: 'tool' }> = { kind: 'tool', key, tool, summary: summary || tool, ok: null, at }
        items.push(item)
        openTools.set(tool, item)
        break
      }
      case 'tool_result': {
        const { tool, ok, summary } = e.payload
        const item = openTools.get(tool)
        if (item) {
          item.ok = ok
          if (!ok && summary) item.error = summary
          openTools.delete(tool)
        }
        break
      }
      case 'question': {
        const q = e.payload
        const questionId = fullQuestionId(e.run_id, q.question_id)
        const item: Extract<TimelineItem, { kind: 'question' }> = {
          kind: 'question',
          key,
          questionId,
          text: q.text,
          options: q.options ?? [],
          at,
        }
        items.push(item)
        questions.set(questionId, item)
        break
      }
      case 'answer': {
        const a = e.payload
        const q = questions.get(a.question_id) ?? [...questions.values()].find((x) => x.questionId.endsWith(`-${a.question_id}`))
        if (q) {
          // A real answer replaces a pending optimistic one.
          if (!q.answer || q.answer.pending) q.answer = { text: a.text, via: a.answered_via, pending }
        } else {
          items.push({ kind: 'user', key, text: a.text, at, pending })
        }
        break
      }
      case 'message': {
        const m = e.payload
        const item: Extract<TimelineItem, { kind: 'user' }> = { kind: 'user', key, text: m.text, at, pending }
        if (m.attachments?.length) item.attachments = m.attachments
        if (m.delivery === 'queued') {
          item.note = 'queued'
          waiting.push(item)
        } else if (m.delivery === 'injected') item.note = 'injected'
        items.push(item)
        break
      }
      case 'created': {
        const atts = e.payload.attachments
        if (atts.length) items.push({ kind: 'attachments', key, attachments: atts, at })
        break
      }
      case 'message_dropped':
        for (const w of waiting) w.note = { dropped: e.payload.reason }
        waiting = []
        break
      case 'status': {
        const s = e.payload
        if (s.status === 'queued') {
          for (const w of waiting) if (!w.pending) w.note = undefined
          waiting = waiting.filter((w) => w.pending)
        }
        items.push({ kind: 'status', key, status: s.status as TaskStatus, reason: s.reason, at })
        break
      }
      case 'run_started': {
        const r = e.payload
        items.push({ kind: 'divider', key, text: `run_started:${r.kind}`, at })
        break
      }
      case 'run_finished': {
        const r = e.payload
        openTools.clear()
        // The run's cost, when recorded: "|<usd>|<source>".
        const cost = r.cost_usd != null && r.cost_source ? `|${r.cost_usd}|${r.cost_source}` : ''
        items.push({ kind: 'divider', key, text: `run_finished:${r.exit_code ?? ''}${cost}`, at })
        break
      }
      case 'budget': {
        const b = e.payload
        items.push({ kind: 'divider', key, text: `budget:${JSON.stringify(b)}`, tone: b.phase === 'override' ? 'muted' : 'warn', at })
        break
      }
      case 'interrupt':
        items.push({ kind: 'divider', key, text: 'interrupt', at })
        break
      case 'paused':
        items.push({ kind: 'divider', key, text: 'paused', tone: 'warn', at })
        break
      case 'committed': {
        const c = e.payload
        items.push({ kind: 'commit', key, sha: c.sha, files: c.files_changed, insertions: c.insertions, deletions: c.deletions, at })
        break
      }
      case 'pr_opened':
      case 'pr_updated': {
        const pr = e.payload
        items.push({ kind: 'pr', key, action: e.type === 'pr_opened' ? 'opened' : 'updated', number: pr.number, url: pr.url, at })
        break
      }
      case 'pr_closed': {
        const pr = e.payload
        items.push({ kind: 'pr', key, action: 'closed', number: pr.number, merged: pr.merged, at })
        break
      }
      case 'ci': {
        const c = e.payload
        items.push({ kind: 'ci', key, status: c.status, name: c.name, url: c.url, at })
        break
      }
      case 'done': {
        const d = e.payload
        items.push({ kind: 'done', key, summary: d.summary, noChanges: d.no_changes, at })
        break
      }
      case 'error': {
        const er = e.payload
        items.push({ kind: 'error', key, title: er.code, message: er.message, at })
        break
      }
      case 'autofix': {
        const a = e.payload
        items.push({
          kind: 'autofix',
          key,
          phase: a.phase,
          attempt: a.attempt ?? 0,
          maxAttempts: a.max_attempts ?? 0,
          checks: a.checks ?? [],
          signature: a.signature,
          attachments: a.attachments ?? [],
          reason: a.reason,
          reasons: a.reasons ?? [],
          fixes: a.fixes ?? 0,
          at,
        })
        break
      }
      case 'mcp_call': {
        const m = e.payload
        items.push({ kind: 'mcp', key, server: m.server, tool: m.tool, ok: m.ok, ms: m.duration_ms ?? 0, denied: m.denied, error: m.error, at })
        break
      }
      case 'egress_blocked':
        items.push({ kind: 'egress', key, host: e.payload.host, at })
        break
      case 'integrations': {
        const p = e.payload
        items.push({ kind: 'divider', key, text: `integrations:${p.servers.join(', ')}|${p.egress}`, tone: p.warning ? 'warn' : 'muted', at })
        break
      }
      case 'diff_rejected':
        items.push({ kind: 'error', key, title: 'diff_rejected', message: e.payload.reason, at })
        break
      case 'checkpoint': {
        const c = e.payload
        items.push({ kind: 'checkpoint', key, eventId: e.id, n: c.n, reason: c.reason ?? 'turn', files: c.files_changed, at })
        break
      }
      case 'checkpoint_restored': {
        const r = e.payload
        items.push({ kind: 'restored', key, n: r.n, mode: r.mode, instruction: r.instruction, at })
        break
      }
      case 'branch_force_pushed':
        items.push({ kind: 'force_push', key, branch: e.payload.branch, from: e.payload.from, at })
        break
      case 'branch_ready': {
        const b = e.payload
        items.push({ kind: 'branch_ready', key, branch: b.branch, commits: b.commits, diffstat: b.diffstat, updated: b.updated, previous: b.previous, at })
        break
      }
      case 'branch_action': {
        const b = e.payload
        items.push({ kind: 'branch_action', key, action: b.action, branch: b.branch, into: b.into, fastForward: b.fast_forward, at })
        break
      }
      case 'loop': {
        const l = e.payload
        if (l.phase === 'iteration') items.push({ kind: 'iteration', key, n: l.iteration, max: l.max_iterations ?? l.iteration, command: l.check_command, at })
        else items.push({ kind: 'loop_end', key, state: l.state ?? 'stopped', reason: l.reason, at })
        break
      }
      case 'check': {
        const c = e.payload
        items.push({
          kind: 'check',
          key,
          iteration: c.iteration ?? 1,
          max: c.max_iterations ?? c.iteration ?? 1,
          exitCode: c.exit_code,
          timedOut: c.timed_out,
          output: c.output,
          command: c.command,
          ms: c.duration_ms,
          at,
        })
        break
      }
      default:
        // ready, started, heartbeat and unknown types are not shown.
        break
    }
  }

  return groupAbandoned(items, abandonedRanges(all))
}

/**
 * Event id ranges a restore discarded: after the restored checkpoint's
 * event, before the checkpoint_restored event (both exclusive).
 */
export function abandonedRanges(events: ApiEvent[]): Array<[number, number]> {
  const out: Array<[number, number]> = []
  for (const e of events) {
    if (e.type === 'checkpoint_restored' && e.payload.checkpoint_event_id != null && e.id > 0) {
      out.push([e.payload.checkpoint_event_id, e.id])
    }
  }
  return out
}

/** Folds each run of consecutive discarded items into one `abandoned` item. */
export function groupAbandoned(items: TimelineItem[], ranges: Array<[number, number]>): TimelineItem[] {
  if (ranges.length === 0) return items
  const discarded = (key: string) => {
    const id = Number(key)
    return Number.isInteger(id) && id > 0 && ranges.some(([from, to]) => id > from && id < to)
  }
  const out: TimelineItem[] = []
  let group: Extract<TimelineItem, { kind: 'abandoned' }> | null = null
  for (const item of items) {
    if (!discarded(item.key)) {
      group = null
      out.push(item)
      continue
    }
    if (!group) {
      group = { kind: 'abandoned', key: `abandoned-${item.key}`, items: [], at: item.at }
      out.push(group)
    }
    group.items.push(item)
  }
  return out
}
