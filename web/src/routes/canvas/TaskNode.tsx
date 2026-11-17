import { memo, useEffect, useMemo, useRef, useState, useSyncExternalStore, type PointerEvent as ReactPointerEvent } from 'react'
import { Maximize2, Minus, X } from 'lucide-react'
import type { ApiEvent, Task, TaskStatus, TaskView } from '@/api/types'
import { useAnswer, useTask, useTaskEvents } from '@/api/hooks'
import { useTaskSubscription } from '@/realtime/RealtimeProvider'
import { buildTimeline, isStopping, type TimelineItem } from '@/lib/timeline'
import { STATUS, type Tone } from '@/lib/status'
import { duration, costLabel, usd } from '@/lib/format'
import {
  currentActivity,
  diffTotals,
  laneCounts,
  laneOf,
  stepCount,
  touchedFiles,
  type Activity,
  type ActivityKind,
  type LaneRow,
  type Touched,
} from '@/lib/activity'
import { withOpenQuestion } from '@/routes/tasks/Timeline'
import { t } from '@/i18n/en'
import { BarButton, Prompt } from './TerminalWindow'
import { TITLE_H, type DetailLevel, type WindowState } from './layout'

const tc = t.canvas
const tn = t.canvas.node

const LIVE: TaskStatus[] = ['running', 'waiting_input', 'preparing', 'pushing']
const FINISHED: TaskStatus[] = ['pr_open', 'completed']
/** The node derives everything from the latest items only. */
const MAX_ITEMS = 200

const LIGHT: Record<Tone, string> = {
  signal: 'var(--signal)',
  ask: 'var(--signal)',
  olive: 'var(--olive-text)',
  danger: 'var(--danger)',
  paused: 'var(--ink-muted)',
  plain: 'var(--ink-muted)',
}

type Props = {
  task: Task
  win: WindowState
  host: { name: string; online: boolean; accent: string }
  focused: boolean
  z: number
  /** From the board zoom (layout.detailLevel): how much of the node to draw. */
  level: DetailLevel
  onDragStart: (e: ReactPointerEvent, kind: 'move' | 'resize') => void
  onFocus: () => void
  onToggleMin: () => void
  onHide: () => void
}

export const TaskNode = memo(function TaskNode({ task, win, host, focused, z, level, onDragStart, onFocus, onToggleMin, onHide }: Props) {
  const full = level === 'full'
  const startMove = (e: ReactPointerEvent) => {
    if ((e.target as HTMLElement).closest('button,a,input')) return
    onDragStart(e, 'move')
  }
  return (
    <section
      aria-labelledby={`node-${task.id}-title`}
      data-focused={focused}
      data-status={task.status}
      data-level={level}
      onPointerDownCapture={onFocus}
      className="fp-node absolute"
      style={{ left: win.x, top: win.y, width: win.w, height: win.min ? TITLE_H : win.h, zIndex: z, '--host': host.accent } as React.CSSProperties}
    >
      <div className="fp-node-frame flex h-full flex-col overflow-hidden">
        {full && (
          <header
            className="fp-node-bar flex shrink-0 cursor-grab items-center gap-2 pr-1 pl-3 select-none active:cursor-grabbing"
            style={{ height: TITLE_H }}
            onPointerDown={startMove}
            onDoubleClick={onToggleMin}
          >
            <Light status={task.status} />
            <h3 id={`node-${task.id}-title`} className="min-w-0 flex-1 truncate text-[13px] font-bold" title={task.title}>
              #{task.id} · {task.title}
            </h3>
            <span className="shrink-0 text-[11px]">{t.agents[task.agent]}</span>
            <div className="flex shrink-0">
              <BarButton label={tc.open} asLink={`/tasks/${task.id}`}>
                <Maximize2 className="size-3.5" />
              </BarButton>
              <BarButton label={win.min ? tc.restore : tc.minimize} onClick={onToggleMin}>
                <Minus className="size-3.5" />
              </BarButton>
              <BarButton label={tc.close} onClick={onHide} close>
                <X className="size-3.5" />
              </BarButton>
            </div>
          </header>
        )}
        {!full && (
          <h3 id={`node-${task.id}-title`} className="sr-only">
            {task.title}
          </h3>
        )}
        {(!full || !win.min) && <NodeBody task={task} level={level} host={host} onPointerDown={full ? undefined : startMove} />}
      </div>
      {full && !win.min && (
        <div className="absolute right-0 bottom-0 size-4 cursor-nwse-resize" onPointerDown={(e) => onDragStart(e, 'resize')} aria-hidden="true" />
      )}
    </section>
  )
})

function Light({ status }: { status: TaskStatus }) {
  const s = STATUS[status]
  return (
    <span
      className="fp-node-led size-2.5 shrink-0"
      style={{ background: LIGHT[s.tone] }}
      data-live={LIVE.includes(status)}
      role="img"
      aria-label={s.label}
      title={s.label}
    />
  )
}

/** The latest cumulative cost the runner reported (usage events), else the task's total. */
function latestCost(events: ApiEvent[], fallback: number): number {
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i]
    if (e.type === 'usage') return e.payload.cost_usd_total
  }
  return fallback
}

/** Updates at most once per `ms`: what the aria-live region announces. */
function useThrottled<T>(value: T, ms: number): T {
  const [shown, setShown] = useState(value)
  const last = useRef(0)
  useEffect(() => {
    const id = window.setTimeout(
      () => {
        last.current = Date.now()
        setShown(value)
      },
      Math.max(0, last.current + ms - Date.now()),
    )
    return () => window.clearTimeout(id)
  }, [value, ms])
  return shown
}

/** The live part: follows the task's events while the node is on the board. */
function NodeBody({ task: listed, level, host, onPointerDown }: { task: Task; level: DetailLevel; host: Props['host']; onPointerDown?: (e: ReactPointerEvent) => void }) {
  const taskQ = useTask(listed.id)
  const events = useTaskEvents(listed.id)
  useTaskSubscription(listed.id)
  const view = taskQ.data
  const task: Task | TaskView = view ?? listed
  const raw = events.data?.items

  const items = useMemo(() => {
    const all = buildTimeline(raw ?? [], events.data?.local ?? [])
    return (view ? withOpenQuestion(all, view) : all).slice(-MAX_ITEMS)
  }, [raw, events.data?.local, view])

  const openQ = view?.open_question && !view.open_question.answer ? view.open_question : null
  const derived = useMemo(
    () => ({
      now: currentActivity(items, task, openQ?.text),
      lanes: laneCounts(items),
      touched: touchedFiles(items),
      steps: stepCount(items),
      diff: diffTotals(items),
    }),
    [items, task, openQ?.text],
  )
  const cost = useMemo(() => latestCost(raw ?? [], task.cost_usd_total), [raw, task.cost_usd_total])
  const announced = useThrottled(tn.activity(derived.now.verb, derived.now.target), 2000)
  const live = <p className="sr-only" aria-live="polite">{announced}</p>

  if (level !== 'full') {
    return (
      <div className="fp-node-compact flex min-h-0 flex-1 cursor-grab items-start p-3 active:cursor-grabbing" onPointerDown={onPointerDown} title={task.title}>
        {live}
        {level === 'dot' && <span className="fp-node-dot" style={{ background: LIGHT[STATUS[task.status].tone] }} role="img" aria-label={`${task.title}: ${STATUS[task.status].label}`} />}
        {level === 'chip' && (
          <span className="flex min-w-0 items-center gap-2">
            <Light status={task.status} />
            <span className="truncate">
              #{task.id} · {derived.now.verb}
            </span>
          </span>
        )}
        {level === 'strip' && (
          <span className="flex w-full min-w-0 items-center gap-2">
            <Light status={task.status} />
            <span className="shrink-0">#{task.id}</span>
            <span className="shrink-0 font-semibold">{derived.now.verb}</span>
            <span className="min-w-0 flex-1 truncate fp-node-muted">{derived.now.target}</span>
            <span className="shrink-0 tabular-nums">{usd(cost, task.cost_estimated, task.cost_unknown)}</span>
          </span>
        )}
      </div>
    )
  }

  const finished = FINISHED.includes(task.status)
  const stopping = view ? isStopping(raw ?? [], view.status) : false
  const question = openQ ? items.findLast((i): i is Extract<TimelineItem, { kind: 'question' }> => i.kind === 'question' && i.questionId === openQ.id) : undefined
  const runStart = view?.latest_run?.started_at ?? task.created_at
  const runEnd = view?.latest_run?.finished_at ?? task.finished_at ?? null

  return (
    <>
      {live}
      <div className="fp-node-body flex min-h-0 flex-1 flex-col gap-2.5 overflow-hidden px-3 py-2.5" onWheel={(e) => e.stopPropagation()}>
        {!host.online && <p className="fp-node-muted text-xs">{t.hosts.waitingFor(host.name)}</p>}
        <NowPanel now={derived.now} taskId={task.id} question={task.status === 'waiting_input' ? question : undefined} />
        {finished ? (
          <Finished task={task} files={derived.diff.files || derived.touched.length} diff={derived.diff} cost={cost} />
        ) : (
          <Lanes lanes={derived.lanes} active={laneOf(derived.now.kind)} />
        )}
        {derived.touched.length > 0 && <TouchedList touched={derived.touched} />}
        <footer className="fp-node-muted mt-auto flex shrink-0 items-center gap-3 font-mono text-[11px] tabular-nums">
          <span>{tn.step(derived.steps)}</span>
          <span aria-label={`${tn.cost}: ${costLabel(cost, task.cost_estimated, task.cost_unknown)}`}>{usd(cost, task.cost_estimated, task.cost_unknown)}</span>
          <span className="ml-auto" aria-label={tn.elapsed}>
            {runEnd ? duration(runStart, runEnd) : <Ticking from={runStart} />}
          </span>
        </footer>
      </div>
      {view && LIVE.includes(view.status) && <Prompt task={view} stopping={stopping} />}
    </>
  )
}

// One shared one-second clock for every running node.
const clock = { now: Date.now(), subs: new Set<() => void>(), id: 0 }
function subscribeClock(cb: () => void) {
  clock.subs.add(cb)
  if (!clock.id) {
    clock.id = window.setInterval(() => {
      clock.now = Date.now()
      clock.subs.forEach((f) => f())
    }, 1000)
  }
  return () => {
    clock.subs.delete(cb)
    if (!clock.subs.size) {
      window.clearInterval(clock.id)
      clock.id = 0
    }
  }
}

function Ticking({ from }: { from: string }) {
  const now = useSyncExternalStore(subscribeClock, () => clock.now)
  return <>{duration(from, new Date(now).toISOString())}</>
}

function NowPanel({ now, taskId, question }: { now: Activity; taskId: number; question?: Extract<TimelineItem, { kind: 'question' }> }) {
  return (
    <div className="fp-node-now flex shrink-0 gap-3" data-kind={now.kind}>
      <Glyph kind={now.kind} />
      <div className="min-w-0 flex-1">
        <p className="font-mono text-[12px] font-semibold tracking-[1px] uppercase">{now.verb}</p>
        {question ? <InlineQuestion item={question} taskId={taskId} /> : <p className="fp-node-muted truncate font-mono text-[12px]" title={now.target}>{now.target}</p>}
      </div>
    </div>
  )
}

/** The open question with its answers as buttons and a free-text reply. */
function InlineQuestion({ item, taskId }: { item: Extract<TimelineItem, { kind: 'question' }>; taskId: number }) {
  const answer = useAnswer(taskId)
  const [text, setText] = useState('')
  const interactive = !item.answer && !answer.isPending
  const send = (v: string) => {
    if (!v.trim() || !interactive) return
    answer.mutate({ question_id: item.questionId, text: v.trim() }, { onSuccess: () => setText('') })
  }
  return (
    <div className="mt-0.5">
      <p className="text-[13px] leading-snug">{item.text}</p>
      {item.answer ? (
        <p className="fp-node-muted mt-1 font-mono text-[12px]">❯ {item.answer.text}</p>
      ) : (
        <>
          {item.options.length > 0 && (
            <div className="mt-1.5 flex flex-wrap gap-1.5">
              {item.options.map((o) => (
                <button key={o} type="button" disabled={!interactive} onClick={() => send(o)} className="fp-node-option">
                  {o}
                </button>
              ))}
            </div>
          )}
          <form
            className="mt-1.5 flex"
            onSubmit={(e) => {
              e.preventDefault()
              send(text)
            }}
          >
            <input value={text} onChange={(e) => setText(e.target.value)} placeholder={tn.reply} aria-label={tn.reply} disabled={!interactive} className="fp-node-input min-w-0 flex-1" />
          </form>
        </>
      )}
    </div>
  )
}

function Lanes({ lanes, active }: { lanes: LaneRow[]; active: string | null }) {
  return (
    <ul className="flex min-h-0 flex-col gap-1 overflow-hidden" aria-label={tn.lanesLabel}>
      {lanes.map((l) => (
        <li key={l.lane} className="flex items-center gap-2 font-mono text-[10px]" aria-label={tn.laneCount(tn.lanes[l.lane], l.count)}>
          <span className="fp-node-muted w-10 shrink-0 tracking-[1px]" aria-hidden="true">
            {tn.lanes[l.lane]}
          </span>
          <span className="w-5 shrink-0 text-right tabular-nums" aria-hidden="true">
            {l.count}
          </span>
          <span className="flex gap-[3px]" aria-hidden="true">
            {l.blocks.map((k, i) => (
              <span key={k} className="fp-node-block" data-lane={l.lane} data-newest={l.lane === active && i === l.blocks.length - 1} />
            ))}
          </span>
        </li>
      ))}
    </ul>
  )
}

function TouchedList({ touched }: { touched: Touched[] }) {
  return (
    <div className="shrink-0">
      <p className="fp-node-muted font-mono text-[10px] tracking-[1px]">{tn.touched}</p>
      <ul className="mt-1 flex flex-col gap-0.5">
        {touched.map((f) => (
          <li key={f.path} className="fp-node-touched flex min-w-0 items-center gap-2 font-mono text-[11px]">
            <span className="fp-node-badge" data-mode={f.mode} title={f.mode === 'W' ? tn.modeWrite : tn.modeRead}>
              {f.mode}
            </span>
            <span className="truncate" title={f.path}>
              {f.path}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

function Finished({ task, files, diff, cost }: { task: Task; files: number; diff: { insertions: number; deletions: number }; cost: number }) {
  return (
    <div className="flex shrink-0 flex-col gap-2">
      {task.pr_number ? (
        <div className="flex gap-1.5">
          <span className="fm-stamp" data-tone="olive">
            {tn.pr(task.pr_number)}
          </span>
          {task.ci_status && (
            <span className="fm-stamp" data-tone={task.ci_status === 'failure' ? 'danger' : task.ci_status === 'success' ? 'olive' : 'plain'}>
              {tn.ci(task.ci_status)}
            </span>
          )}
        </div>
      ) : null}
      <dl className="fp-node-grid grid grid-cols-3">
        <div>
          <dt>{tn.files}</dt>
          <dd>{files}</dd>
        </div>
        <div>
          <dt>{tn.diff}</dt>
          <dd>
            +{diff.insertions} −{diff.deletions}
          </dd>
        </div>
        <div>
          <dt>{tn.costCell}</dt>
          <dd>{usd(cost, task.cost_estimated, task.cost_unknown)}</dd>
        </div>
      </dl>
      {task.pr_url && (
        <a href={task.pr_url} target="_blank" rel="noreferrer" className="text-[13px] font-semibold text-[var(--link)] underline underline-offset-2">
          {tn.openPr}
        </a>
      )}
    </div>
  )
}

/** Small inline SVG per activity; the motion is CSS (canvas.css), off under reduced motion. */
function Glyph({ kind }: { kind: ActivityKind }) {
  return (
    <span className="fp-glyph" data-kind={kind} aria-hidden="true">
      <svg viewBox="0 0 32 32" width="32" height="32" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="square">
        {GLYPHS[kind]}
      </svg>
    </span>
  )
}

const GLYPHS: Record<ActivityKind, React.ReactNode> = {
  read: (
    <>
      <path d="M4 10h9l2 3h13v14H4z" />
      <g className="g-bob">
        <path d="M10 4h12v12H10z" fill="var(--paper-raised)" />
        <path d="M13 8h6M13 11h6" />
      </g>
    </>
  ),
  web: (
    <>
      <circle cx="16" cy="16" r="10" />
      <ellipse className="g-squash" cx="16" cy="16" rx="5" ry="10" />
      <path d="M6 16h20" />
      <g className="g-orbit">
        <circle cx="16" cy="3" r="2" fill="var(--signal)" stroke="none" />
      </g>
    </>
  ),
  shell: (
    <>
      <path d="M3 6h26v20H3z" />
      <path d="M8 12l4 4-4 4" />
      <path className="g-blink" d="M15 21h7" />
    </>
  ),
  test: (
    <>
      <path d="M3 12h26v8H3z" />
      <path className="g-fill" d="M5 16h22" stroke="var(--signal)" strokeWidth="4" strokeLinecap="butt" />
    </>
  ),
  edit: (
    <>
      <path d="M8 21l13-13 4 4-13 13H8z" />
      <path className="g-draw" d="M4 29h24" stroke="var(--signal)" />
    </>
  ),
  think: (
    <>
      <circle className="g-dot g-dot1" cx="8" cy="16" r="2.5" fill="currentColor" stroke="none" />
      <circle className="g-dot g-dot2" cx="16" cy="16" r="2.5" fill="currentColor" stroke="none" />
      <circle className="g-dot g-dot3" cx="24" cy="16" r="2.5" fill="currentColor" stroke="none" />
    </>
  ),
  ask: (
    <>
      <path d="M3 3h26v26H3z" strokeDasharray="4 3" />
      <text className="g-bounce" x="16" y="22" textAnchor="middle" fontSize="16" fontWeight="700" fill="currentColor" stroke="none">
        ?
      </text>
    </>
  ),
  git: (
    <g className="g-rise">
      <path d="M16 26V8M9 15l7-7 7 7" />
    </g>
  ),
  done: <path d="M7 16l6 6 12-12" strokeWidth="3" />,
  failed: <path d="M8 8l16 16M24 8L8 24" strokeWidth="3" />,
  idle: <path d="M12 9v14M20 9v14" strokeWidth="3" />,
}
