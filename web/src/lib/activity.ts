import type { TaskStatus } from '@/api/types'
import type { TimelineItem } from '@/lib/timeline'
import { t } from '@/i18n/en'

/** What a task is doing, for the canvas node view. Pure. */
export type ActivityKind = 'read' | 'web' | 'edit' | 'shell' | 'test' | 'think' | 'ask' | 'git' | 'done' | 'failed' | 'idle'
export type Lane = 'read' | 'web' | 'edit' | 'shell' | 'git'
/** via: the subagent doing the work (Claude), absent for the lead agent. */
export type Activity = { kind: ActivityKind; verb: string; target: string; path?: string; mode?: 'R' | 'W'; via?: string }

export const LANES: Lane[] = ['read', 'web', 'edit', 'shell', 'git']
/** Blocks drawn per lane. */
export const LANE_BLOCKS = 12

const v = t.canvas.node.verbs

// Tool names as the agents emit them: Claude's own, plus what agent/src/codex-mapper.ts
// and agent/src/cursor-mapper.ts translate Codex and Cursor tools to.
const READ_TOOLS = new Set(['Read', 'Grep', 'Glob', 'LS', 'NotebookRead'])
const EDIT_TOOLS = new Set(['Write', 'Edit', 'MultiEdit', 'NotebookEdit', 'Delete'])
// Agent (formerly Task) starts a subagent.
const THINK_TOOLS = new Set(['TodoWrite', 'Task', 'Agent'])

const TEST_RE = /\b(go test|npm (run )?test|pnpm (run )?test|yarn (run )?test|vitest|jest|pytest|cargo test|make test|playwright)\b/

export function isTestCommand(cmd: string): boolean {
  return TEST_RE.test(cmd)
}

/** The first path-looking token of a tool summary (after the tool name), if any. */
export function extractPath(summary: string, tool?: string): string | undefined {
  const rest = tool && summary.startsWith(tool) ? summary.slice(tool.length) : summary
  for (const raw of rest.split(/\s+/)) {
    const tok = raw.replace(/^[:'"(]+|[:,;'")]+$/g, '')
    if (!tok || tok === '.' || tok === '..') continue
    if (!/^[\w@~.\-/+]+$/.test(tok)) continue
    if (tok.includes('/') || /\.[A-Za-z0-9]{1,10}$/.test(tok)) return tok
  }
  return undefined
}

function host(url: string): string {
  try {
    return new URL(url).host || url
  } catch {
    return url
  }
}

/** "<server>.<tool>" (Codex, Cursor) or "mcp__<server>__<tool>" (Claude): the server name. */
export function mcpServer(tool: string): string | undefined {
  const m = /^mcp__(.+?)__/.exec(tool)
  if (m) return m[1]
  if (tool === 'mcp_tool_call') return 'mcp'
  const dot = tool.indexOf('.')
  return dot > 0 ? tool.slice(0, dot) : undefined
}

function argOf(summary: string, tool: string): string {
  const s = summary.startsWith(tool) ? summary.slice(tool.length) : summary
  return s.replace(/^:?\s*/, '').trim()
}

export function classifyTool(tool: string, summary: string): Activity {
  if (READ_TOOLS.has(tool)) {
    const path = extractPath(summary, tool)
    return { kind: 'read', verb: v.read, target: path ?? argOf(summary, tool), path, mode: path ? 'R' : undefined }
  }
  if (EDIT_TOOLS.has(tool)) {
    const path = extractPath(summary, tool)
    return { kind: 'edit', verb: v.write, target: path ?? argOf(summary, tool), path, mode: path ? 'W' : undefined }
  }
  if (tool === 'WebFetch') return { kind: 'web', verb: v.fetch, target: host(argOf(summary, tool)) }
  if (tool === 'WebSearch') return { kind: 'web', verb: v.fetch, target: argOf(summary, tool) }
  if (tool === 'Bash') {
    const cmd = argOf(summary, tool)
    return isTestCommand(cmd) ? { kind: 'test', verb: v.test, target: cmd } : { kind: 'shell', verb: v.run, target: cmd }
  }
  if (THINK_TOOLS.has(tool)) return { kind: 'think', verb: v.think, target: argOf(summary, tool) }
  if (tool === 'AskUserQuestion') return { kind: 'ask', verb: v.ask, target: argOf(summary, tool) }
  const server = mcpServer(tool)
  if (server) return { kind: 'web', verb: v.fetch, target: server }
  return { kind: 'shell', verb: tool, target: argOf(summary, tool) }
}

/** One timeline item as an activity; null for items that are not work (user messages, dividers…). */
export function classify(item: TimelineItem): Activity | null {
  switch (item.kind) {
    case 'tool': {
      const a = classifyTool(item.tool, item.summary)
      return item.subagent ? { ...a, via: item.subagent } : a
    }
    case 'claude':
      return { kind: 'think', verb: v.think, target: firstLine(item.text) }
    case 'question':
      return { kind: 'ask', verb: v.ask, target: item.text }
    case 'commit':
      return { kind: 'git', verb: v.commit, target: item.sha.slice(0, 7) }
    case 'pr':
      return { kind: 'git', verb: v.prOpen, target: `#${item.number}` }
    case 'branch_ready':
      return { kind: 'git', verb: v.branchReady, target: item.branch }
    case 'error':
      return { kind: 'failed', verb: v.failed, target: item.message }
    default:
      return null
  }
}

function firstLine(s: string): string {
  return s.trim().split('\n')[0] ?? ''
}

export function laneOf(kind: ActivityKind): Lane | null {
  switch (kind) {
    case 'read':
    case 'web':
    case 'edit':
    case 'shell':
    case 'git':
      return kind
    case 'test':
      return 'shell'
    default:
      return null
  }
}

/** Why the task stopped: the latest status reason or error in the timeline. */
function lastReason(items: TimelineItem[], fallback?: string | null): string {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i]
    if (it.kind === 'error') return it.message
    if (it.kind === 'status' && it.reason) return it.reason
  }
  return fallback ?? ''
}

/**
 * The activity to show now. Settled statuses win; while the task runs it is the
 * latest work item (assistant text after a tool counts as thinking).
 */
export function currentActivity(
  items: TimelineItem[],
  task: {
    status: TaskStatus
    status_reason?: string | null
    pr_number?: number | null
    branch?: string | null
    local_branch?: string | null
    title: string
  },
  openQuestion?: string,
): Activity {
  switch (task.status) {
    case 'waiting_input': {
      const q = openQuestion ?? [...items].reverse().find((i) => i.kind === 'question' && !i.answer)
      const text = typeof q === 'string' ? q : q && q.kind === 'question' ? q.text : ''
      return { kind: 'ask', verb: v.ask, target: text }
    }
    case 'pushing':
      return { kind: 'git', verb: v.push, target: task.branch ?? '' }
    case 'pr_open':
      return { kind: 'done', verb: v.prOpen, target: task.pr_number ? `#${task.pr_number}` : task.title }
    case 'branch_ready':
      return { kind: 'done', verb: v.branchReady, target: task.local_branch ?? task.title }
    case 'completed':
      return { kind: 'done', verb: v.done, target: task.title }
    case 'failed':
      return { kind: 'failed', verb: v.failed, target: lastReason(items, task.status_reason) }
    case 'cancelled':
      return { kind: 'failed', verb: v.cancelled, target: lastReason(items, task.status_reason) }
    case 'paused':
      return { kind: 'idle', verb: v.paused, target: lastReason(items, task.status_reason) }
    case 'queued':
      return { kind: 'idle', verb: v.queued, target: task.status_reason ?? '' }
    case 'preparing':
      return { kind: 'idle', verb: v.preparing, target: '' }
    case 'running':
      break
  }
  for (let i = items.length - 1; i >= 0; i--) {
    const a = classify(items[i])
    if (a) return a
  }
  return { kind: 'think', verb: v.think, target: '' }
}

export type LaneRow = { lane: Lane; count: number; blocks: string[] }

/** Per lane: how many items, and the keys of the newest LANE_BLOCKS (oldest first). */
export function laneCounts(items: TimelineItem[]): LaneRow[] {
  const rows = new Map<Lane, LaneRow>(LANES.map((l) => [l, { lane: l, count: 0, blocks: [] }]))
  for (const it of items) {
    const a = classify(it)
    const lane = a && laneOf(a.kind)
    if (!lane) continue
    const r = rows.get(lane)!
    r.count++
    r.blocks.push(it.key)
    if (r.blocks.length > LANE_BLOCKS) r.blocks.shift()
  }
  return LANES.map((l) => rows.get(l)!)
}

export type Touched = { path: string; mode: 'R' | 'W' }

/** The `n` most recent distinct file paths, newest first, with how they were last touched. */
export function touchedFiles(items: TimelineItem[], n = 3): Touched[] {
  const out: Touched[] = []
  for (let i = items.length - 1; i >= 0 && out.length < n; i--) {
    const it = items[i]
    if (it.kind !== 'tool') continue
    const a = classifyTool(it.tool, it.summary)
    if (!a.path || !a.mode) continue
    if (out.some((x) => x.path === a.path)) continue
    out.push({ path: a.path, mode: a.mode })
  }
  return out
}

export function stepCount(items: TimelineItem[]): number {
  return items.reduce((n, it) => n + (it.kind === 'tool' ? 1 : 0), 0)
}

export function diffTotals(items: TimelineItem[]): { files: number; insertions: number; deletions: number } {
  const d = { files: 0, insertions: 0, deletions: 0 }
  for (const it of items) {
    if (it.kind !== 'commit') continue
    d.files += it.files
    d.insertions += it.insertions
    d.deletions += it.deletions
  }
  return d
}
