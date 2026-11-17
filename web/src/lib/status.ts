import {
  CircleCheck,
  CircleDashed,
  CircleHelp,
  CirclePause,
  CircleSlash,
  CircleX,
  GitPullRequest,
  Loader,
  Upload,
  Wrench,
  type LucideIcon,
} from 'lucide-react'
import type { TaskStatus } from '@/api/types'
import { t } from '@/i18n/en'

/** Field Manual stamp tones (fm-stamp[data-tone] in src/index.css). */
export type Tone = 'signal' | 'ask' | 'olive' | 'danger' | 'paused' | 'plain'

export const STATUS: Record<TaskStatus, { label: string; tone: Tone; icon: LucideIcon; blink?: boolean }> = {
  queued: { label: t.status.queued, tone: 'plain', icon: CircleDashed },
  preparing: { label: t.status.preparing, tone: 'signal', icon: Wrench },
  running: { label: t.status.running, tone: 'signal', icon: Loader, blink: true },
  waiting_input: { label: t.status.waiting_input, tone: 'ask', icon: CircleHelp },
  paused: { label: t.status.paused, tone: 'paused', icon: CirclePause },
  pushing: { label: t.status.pushing, tone: 'signal', icon: Upload },
  pr_open: { label: t.status.pr_open, tone: 'olive', icon: GitPullRequest },
  completed: { label: t.status.completed, tone: 'olive', icon: CircleCheck },
  failed: { label: t.status.failed, tone: 'danger', icon: CircleX },
  cancelled: { label: t.status.cancelled, tone: 'paused', icon: CircleSlash },
}

export const ALL_STATUSES = Object.keys(STATUS) as TaskStatus[]

export type TaskGroup = 'active' | 'pr_open' | 'done' | 'failed'

export const GROUPS: Record<TaskGroup, TaskStatus[]> = {
  active: ['queued', 'preparing', 'running', 'waiting_input', 'paused', 'pushing'],
  pr_open: ['pr_open'],
  done: ['completed'],
  failed: ['failed', 'cancelled'],
}

export const ACTIVE_STATUSES = GROUPS.active

// Actions per status, from the status table in docs/API.md.
export type TaskAction = 'cancel' | 'message' | 'interrupt' | 'answer' | 'retry' | 'merge'

const ACTIONS: Record<TaskStatus, TaskAction[]> = {
  queued: ['message', 'cancel'],
  preparing: ['message', 'cancel'],
  running: ['message', 'interrupt', 'cancel'],
  waiting_input: ['answer', 'message', 'interrupt', 'cancel'],
  paused: ['answer', 'message', 'cancel'],
  pushing: ['message', 'cancel'],
  pr_open: ['merge', 'message', 'cancel'],
  completed: ['message'],
  failed: ['retry'],
  cancelled: ['retry'],
}

export function can(status: TaskStatus, action: TaskAction): boolean {
  return ACTIONS[status].includes(action)
}

/**
 * How a composer message is delivered, or null when the composer is hidden
 * (409 states). A run handles one prompt, so nothing reaches a live session:
 * while a run is pending or active the message is queued and starts the next
 * run when the current one ends; otherwise it starts a new run right away.
 */
export function composerMode(status: TaskStatus): 'queue' | 'followup' | null {
  if (!can(status, 'message')) return null
  return status === 'paused' || status === 'pr_open' || status === 'completed' ? 'followup' : 'queue'
}
