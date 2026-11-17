import { useEffect, useMemo, useRef, useState, type ClipboardEvent, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { Archive, Columns3, MoreVertical, Paperclip, Search } from 'lucide-react'
import { toast } from 'sonner'
import {
  closestCorners,
  DndContext,
  DragOverlay,
  KeyboardSensor,
  PointerSensor,
  useDroppable,
  useSensor,
  useSensors,
  type Announcements,
  type DragEndEvent,
  type DragStartEvent,
} from '@dnd-kit/core'
import { SortableContext, sortableKeyboardCoordinates, useSortable, verticalListSortingStrategy } from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import { ideaKeys, useArchiveIdea, useCreateIdea, useIdeas, useIdeaSettings, useMoveIdea, useVoiceIdea, type Recording } from '@/api/ideas'
import { errorMessage, useProjects } from '@/api/hooks'
import type { Idea, IdeaColumn } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '@/components/ui/dropdown-menu'
import { afterIdAt, byColumn, splitLinks, tagsOf } from '@/lib/ideas'
import { cn } from '@/lib/utils'
import { haptic } from '@/telegram/haptics'
import { id as tx } from '@/i18n/ideas'
import { canRecord, HoldToTalk } from './HoldToTalk'
import { IdeaCardBody, MoveSheet } from './ideaParts'
import { ColumnsDialog } from './ColumnsDialog'

function useWide(): boolean {
  const query = '(min-width: 1024px)'
  const [wide, setWide] = useState(() => typeof window !== 'undefined' && window.matchMedia?.(query).matches)
  useEffect(() => {
    const m = window.matchMedia?.(query)
    if (!m) return
    const on = () => setWide(m.matches)
    m.addEventListener('change', on)
    return () => m.removeEventListener('change', on)
  }, [])
  return !!wide
}

/** The Ideas board: capture (hold-to-talk, text, links, files) and the columns. */
export default function IdeasPage() {
  const [params, setParams] = useSearchParams()
  const filter = { q: params.get('q') ?? '', project: params.get('project') ?? '', tag: params.get('tag') ?? '' }
  const settings = useIdeaSettings()
  const on = settings.data?.enabled === true
  const list = useIdeas(filter, on)
  const all = useIdeas({}, on && !!(filter.q || filter.project || filter.tag))
  const projects = useProjects()
  const [columnsOpen, setColumnsOpen] = useState(false)
  const setParam = (k: string, v: string) => {
    const p = new URLSearchParams(params)
    if (v) p.set(k, v)
    else p.delete(k)
    setParams(p, { replace: true })
  }
  const tags = tagsOf(all.data ?? list.data ?? [])

  if (settings.isPending) return <PageSkeleton />
  if (!on) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
        <p className="text-muted-foreground">{tx.off}</p>
        <Button asChild className="h-11">
          <Link to="/settings#ideas">{tx.turnOn}</Link>
        </Button>
      </div>
    )
  }
  const columns = settings.data?.columns ?? []
  return (
    <div className="flex h-full flex-col overflow-y-auto">
      <div className="mx-auto w-full max-w-7xl space-y-3 px-4 pt-4 pb-6">
        <div className="flex items-center gap-2">
          <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">{tx.title}</h1>
          <Button asChild variant="outline" size="icon" className="size-11" aria-label={tx.archived}>
            <Link to="/ideas/archive">
              <Archive />
            </Link>
          </Button>
          <Button variant="outline" size="icon" className="size-11" aria-label={tx.columns} onClick={() => setColumnsOpen(true)}>
            <Columns3 />
          </Button>
        </div>
        <Capture project={filter.project} />
        <div className="flex flex-wrap gap-2">
          <div className="relative min-w-40 flex-1">
            <Search className="pointer-events-none absolute top-3.5 left-2.5 size-4 text-muted-foreground" aria-hidden="true" />
            <Input value={filter.q} onChange={(e) => setParam('q', e.target.value)} placeholder={tx.search} aria-label={tx.search} className="h-11 pl-8" />
          </div>
          <select value={filter.project} onChange={(e) => setParam('project', e.target.value)} aria-label={tx.project} className="h-11 fm-field bg-transparent px-2 text-sm">
            <option value="">{tx.allProjects}</option>
            {projects.data?.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name}
              </option>
            ))}
          </select>
          <select value={filter.tag} onChange={(e) => setParam('tag', e.target.value)} aria-label={tx.tags} className="h-11 fm-field bg-transparent px-2 text-sm">
            <option value="">{tx.allTags}</option>
            {tags.map((t) => (
              <option key={t} value={t}>
                #{t}
              </option>
            ))}
          </select>
        </div>
        {list.isPending ? (
          <PageSkeleton />
        ) : list.isError ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{errorMessage(list.error) || tx.loadError}</p>
        ) : (
          <Board ideas={list.data} columns={columns} listKey={ideaKeys.list(filter)} />
        )}
      </div>
      {columnsOpen && settings.data && <ColumnsDialog settings={settings.data} onClose={() => setColumnsOpen(false)} />}
    </div>
  )
}

function PageSkeleton() {
  return (
    <div className="space-y-2 p-4">
      {[0, 1, 2].map((i) => (
        <Skeleton key={i} className="h-20" />
      ))}
    </div>
  )
}

/** Hold-to-talk plus a quick text field that also takes pasted links and screenshots. */
function Capture({ project }: { project: string }) {
  const voice = useVoiceIdea()
  const create = useCreateIdea()
  const [text, setText] = useState('')
  const [files, setFiles] = useState<File[]>([])
  const fileInput = useRef<HTMLInputElement>(null)

  async function record(rec: Recording) {
    try {
      const idea = await voice.mutateAsync({ rec, project: project || undefined })
      toast.success(tx.savedVoice(idea.title))
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  async function add() {
    const { text: rest, links } = splitLinks(text)
    if (!rest && !links.length && !files.length) return
    try {
      await create.mutateAsync({ body: rest, links, files, project_id: project || null })
      setText('')
      setFiles([])
      haptic.success()
      toast.success(tx.added)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  function paste(e: ClipboardEvent) {
    const pasted = [...e.clipboardData.files]
    if (pasted.length) {
      e.preventDefault()
      setFiles((f) => [...f, ...pasted])
    }
  }

  return (
    <div className="grid gap-3 md:grid-cols-[minmax(0,18rem)_1fr]">
      {canRecord() && <HoldToTalk onRecorded={record} busy={voice.isPending} />}
      <form
        className="space-y-2"
        onSubmit={(e) => {
          e.preventDefault()
          void add()
        }}
      >
        <div className="flex gap-2">
          <Input value={text} onChange={(e) => setText(e.target.value)} onPaste={paste} placeholder={tx.quickAdd} aria-label={tx.quickAdd} className="h-11 flex-1" />
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            data-testid="idea-files"
            onChange={(e) => {
              setFiles((f) => [...f, ...(e.target.files ? [...e.target.files] : [])])
              e.target.value = ''
            }}
          />
          <Button type="button" variant="outline" size="icon" className="size-11" aria-label={tx.addFiles} onClick={() => fileInput.current?.click()}>
            <Paperclip />
          </Button>
          <Button type="submit" className="h-11" disabled={create.isPending}>
            {tx.add}
          </Button>
        </div>
        {files.length > 0 && (
          <ul className="flex flex-wrap gap-1.5 text-xs">
            {files.map((f, i) => (
              <li key={`${f.name}-${i}`} className="fm-stamp normal-case">
                {f.name || 'pasted image'}
                <button type="button" className="ml-1" aria-label={tx.remove(f.name)} onClick={() => setFiles((all) => all.filter((_, j) => j !== i))}>
                  ×
                </button>
              </li>
            ))}
          </ul>
        )}
      </form>
    </div>
  )
}

/** The columns: side by side with drag and drop on wide screens, one at a time on phones. */
function Board({ ideas, columns, listKey }: { ideas: Idea[]; columns: IdeaColumn[]; listKey: readonly unknown[] }) {
  const wide = useWide()
  const move = useMoveIdea(listKey)
  const archive = useArchiveIdea()
  const [moving, setMoving] = useState<Idea | null>(null)
  const grouped = useMemo(() => byColumn(ideas, columns), [ideas, columns])
  const name = (id: string) => columns.find((c) => c.id === id)?.name ?? id

  const doMove = (idea: Idea, column: string, afterId: number | null) => {
    move.mutate(
      { id: idea.id, column, afterId },
      {
        onSuccess: () => {
          haptic.select()
          if (column !== idea.column) toast.success(tx.moved(name(column)))
        },
        onError: (e) => toast.error(errorMessage(e)),
      },
    )
  }
  const menu = (idea: Idea) => <CardMenu idea={idea} onMove={() => setMoving(idea)} onArchive={() => archive.mutate({ id: idea.id, archived: true })} />

  return (
    <>
      {wide ? (
        <DesktopBoard grouped={grouped} columns={columns} ideas={ideas} onMove={doMove} menu={menu} />
      ) : (
        <MobileBoard grouped={grouped} columns={columns} onLongPress={setMoving} menu={menu} />
      )}
      <MoveSheet
        idea={moving}
        columns={columns}
        onClose={() => setMoving(null)}
        onMove={(column) => {
          if (moving) doMove(moving, column, null)
          setMoving(null)
        }}
      />
    </>
  )
}

function CardMenu({ idea, onMove, onArchive }: { idea: Idea; onMove: () => void; onArchive: () => void }) {
  const navigate = useNavigate()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" className="size-11 shrink-0" aria-label={tx.cardActions(idea.title)}>
          <MoreVertical />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-40">
        <DropdownMenuItem className="h-10" onSelect={() => void navigate(`/ideas/${idea.id}`)}>
          {tx.open}
        </DropdownMenuItem>
        <DropdownMenuItem className="h-10" onSelect={onMove}>
          {tx.moveTo}
        </DropdownMenuItem>
        <DropdownMenuItem className="h-10" onSelect={onArchive}>
          {tx.archiveIdea}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

type BoardProps = { grouped: Map<string, Idea[]>; columns: IdeaColumn[]; menu: (idea: Idea) => ReactNode }

function DesktopBoard({ grouped, columns, ideas, onMove, menu }: BoardProps & { ideas: Idea[]; onMove: (idea: Idea, column: string, afterId: number | null) => void }) {
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 6 } }), useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }))
  const [active, setActive] = useState<Idea | null>(null)
  const byId = (id: unknown) => ideas.find((i) => i.id === id)
  const columnOf = (overId: unknown): string | undefined =>
    typeof overId === 'string' && overId.startsWith('col:') ? overId.slice(4) : byId(overId)?.column
  const colName = (id?: string) => columns.find((c) => c.id === id)?.name ?? ''
  const announcements: Announcements = {
    onDragStart: ({ active }) => `Picked up ${byId(active.id)?.title ?? 'card'}.`,
    onDragOver: ({ active, over }) => (over ? `${byId(active.id)?.title ?? 'Card'} is over ${colName(columnOf(over.id))}.` : undefined),
    onDragEnd: ({ active, over }) => (over ? `Dropped ${byId(active.id)?.title ?? 'card'} in ${colName(columnOf(over.id))}.` : 'Dropped.'),
    onDragCancel: ({ active }) => `Moving ${byId(active.id)?.title ?? 'card'} was cancelled.`,
  }

  function end(e: DragEndEvent) {
    setActive(null)
    const card = byId(e.active.id)
    if (!card || !e.over) return
    const column = columnOf(e.over.id)
    if (!column) return
    const cards = grouped.get(column) ?? []
    let index = cards.length
    if (typeof e.over.id === 'number') {
      index = cards.filter((c) => c.id !== card.id).findIndex((c) => c.id === e.over?.id)
      // Dropping on a card below the dragged one (same column) puts it after that card.
      const from = cards.findIndex((c) => c.id === card.id)
      const to = cards.findIndex((c) => c.id === e.over?.id)
      if (from >= 0 && to > from) index += 1
    }
    const afterId = afterIdAt(cards, card.id, index)
    const unchanged = card.column === column && afterIdAt(cards, card.id, cards.findIndex((c) => c.id === card.id)) === afterId
    if (!unchanged) onMove(card, column, afterId)
  }

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCorners}
      accessibility={{ announcements }}
      onDragStart={(e: DragStartEvent) => setActive(byId(e.active.id) ?? null)}
      onDragCancel={() => setActive(null)}
      onDragEnd={end}
    >
      <div className="flex gap-3 overflow-x-auto pb-2" data-testid="idea-board">
        {columns.map((c) => (
          <DesktopColumn key={c.id} column={c} cards={grouped.get(c.id) ?? []} menu={menu} />
        ))}
      </div>
      <DragOverlay>{active && <div className="w-72 fm-card p-3">{<IdeaCardBody idea={active} />}</div>}</DragOverlay>
    </DndContext>
  )
}

function DesktopColumn({ column, cards, menu }: { column: IdeaColumn; cards: Idea[]; menu: BoardProps['menu'] }) {
  const { setNodeRef, isOver } = useDroppable({ id: `col:${column.id}` })
  return (
    <section className="flex w-72 shrink-0 flex-col fm-panel" aria-label={column.name} data-testid={`column-${column.id}`}>
      <h2 className="fm-strip flex h-9 items-center justify-between px-2.5">
        <span>{column.name}</span>
        <span className="font-normal text-muted-foreground">{cards.length}</span>
      </h2>
      <SortableContext id={column.id} items={cards.map((c) => c.id)} strategy={verticalListSortingStrategy}>
        <ul ref={setNodeRef} className={cn('flex min-h-24 flex-1 flex-col gap-2 p-2', isOver && 'bg-grid')}>
          {cards.map((idea) => (
            <SortableCard key={idea.id} idea={idea} menu={menu} />
          ))}
          {cards.length === 0 && <li className="py-6 text-center text-xs text-muted-foreground">{tx.emptyColumn}</li>}
        </ul>
      </SortableContext>
    </section>
  )
}

function SortableCard({ idea, menu }: { idea: Idea; menu: BoardProps['menu'] }) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: idea.id })
  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      className={cn('flex items-start gap-1 fm-card', isDragging && 'opacity-40')}
      data-testid="idea-card"
    >
      <div {...attributes} {...listeners} className="min-w-0 flex-1 cursor-grab p-3 focus-visible:outline-2" aria-roledescription="draggable card" aria-label={idea.title}>
        <Link to={`/ideas/${idea.id}`} className="block" draggable={false} onClick={(e) => isDragging && e.preventDefault()}>
          <IdeaCardBody idea={idea} />
        </Link>
      </div>
      {menu(idea)}
    </li>
  )
}

/** Phones: a segmented switcher, swipe between columns, long-press a card for "Move to…". */
function MobileBoard({ grouped, columns, onLongPress, menu }: BoardProps & { onLongPress: (idea: Idea) => void }) {
  const [current, setCurrent] = useState(columns[0]?.id ?? '')
  const col = columns.find((c) => c.id === current) ?? columns[0]
  const touch = useRef<{ x: number; y: number } | null>(null)
  const index = columns.findIndex((c) => c.id === col?.id)
  const go = (i: number) => {
    const next = columns[Math.max(0, Math.min(columns.length - 1, i))]
    if (next && next.id !== col?.id) {
      haptic.select()
      setCurrent(next.id)
    }
  }
  if (!col) return null
  const cards = grouped.get(col.id) ?? []
  return (
    <div>
      <div role="group" aria-label={tx.columns} className="-mx-4 flex overflow-x-auto border-y border-rule px-4">
        {columns.map((c) => (
          <button
            key={c.id}
            type="button"
            aria-pressed={c.id === col.id}
            onClick={() => setCurrent(c.id)}
            className="fm-tab flex h-11 shrink-0 items-center gap-1.5 px-3 text-[12px]"
            aria-selected={c.id === col.id}
          >
            {c.name}
            <span className="text-muted-foreground">{grouped.get(c.id)?.length ?? 0}</span>
          </button>
        ))}
      </div>
      <ul
        className="mt-3 min-h-[40vh] space-y-2"
        aria-label={col.name}
        data-testid={`column-${col.id}`}
        onTouchStart={(e) => (touch.current = { x: e.touches[0].clientX, y: e.touches[0].clientY })}
        onTouchEnd={(e) => {
          const s = touch.current
          touch.current = null
          if (!s) return
          const dx = e.changedTouches[0].clientX - s.x
          const dy = e.changedTouches[0].clientY - s.y
          if (Math.abs(dx) > 60 && Math.abs(dy) < 40) go(index + (dx < 0 ? 1 : -1))
        }}
      >
        {cards.map((idea) => (
          <MobileCard key={idea.id} idea={idea} onLongPress={() => onLongPress(idea)} menu={menu} />
        ))}
        {cards.length === 0 && <li className="py-10 text-center text-sm text-muted-foreground">{tx.emptyColumn}</li>}
      </ul>
    </div>
  )
}

function MobileCard({ idea, onLongPress, menu }: { idea: Idea; onLongPress: () => void; menu: BoardProps['menu'] }) {
  const navigate = useNavigate()
  const timer = useRef<number | undefined>(undefined)
  const fired = useRef(false)
  const start = useRef<{ x: number; y: number } | null>(null)
  const clear = () => window.clearTimeout(timer.current)
  return (
    <li className="flex items-start gap-1 fm-card" data-testid="idea-card">
      <button
        type="button"
        className="min-w-0 flex-1 p-3 text-left select-none"
        style={{ WebkitTouchCallout: 'none' }}
        onContextMenu={(e) => e.preventDefault()}
        onPointerDown={(e) => {
          fired.current = false
          start.current = { x: e.clientX, y: e.clientY }
          clear()
          timer.current = window.setTimeout(() => {
            fired.current = true
            haptic.tap()
            onLongPress()
          }, 500)
        }}
        onPointerMove={(e) => {
          // Scrolling or swiping cancels the long press; a finger's jitter does not.
          if (start.current && Math.hypot(e.clientX - start.current.x, e.clientY - start.current.y) > 10) clear()
        }}
        onPointerUp={clear}
        onPointerCancel={clear}
        onClick={() => {
          if (!fired.current) void navigate(`/ideas/${idea.id}`)
        }}
      >
        <IdeaCardBody idea={idea} />
      </button>
      {menu(idea)}
    </li>
  )
}
