import { useEffect, useRef } from 'react'
import { UserBadge } from '@/components/UserBadge'
import { ImageOff, Mic } from 'lucide-react'
import { useIdeaFile } from '@/api/ideas'
import type { Idea, IdeaAttachment, IdeaColumn } from '@/api/types'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { formatDuration } from '@/lib/ideas'
import { cn } from '@/lib/utils'
import { id as tx } from '@/i18n/ideas'

/**
 * Shows a Blob in an <img> or <audio> through an object URL set in an effect
 * on the element (revoked on cleanup), like the task attachments.
 */
export function useBlobSrc<T extends HTMLImageElement | HTMLAudioElement>(blob: Blob | undefined) {
  const ref = useRef<T>(null)
  useEffect(() => {
    const el = ref.current
    if (!el || !blob) return
    const u = URL.createObjectURL(blob)
    el.src = u
    return () => {
      el.removeAttribute('src')
      URL.revokeObjectURL(u)
    }
  }, [blob])
  return ref
}

/** A thumbnail of an image or link preview, fetched through the API client. */
export function IdeaThumb({ ideaId, a, className }: { ideaId: number; a: IdeaAttachment; className?: string }) {
  const q = useIdeaFile(ideaId, a, true)
  const img = useBlobSrc<HTMLImageElement>(q.data)
  return (
    <span className={cn('flex size-14 shrink-0 items-center justify-center overflow-hidden fm-well bg-grid', className)}>
      {q.data ? (
        <img ref={img} alt={a.title ?? a.name} className="size-full object-cover" />
      ) : q.isError ? (
        <ImageOff className="size-4 text-muted-foreground" aria-hidden="true" />
      ) : null}
    </span>
  )
}

/** "MIC VOICE 0:07" chip. */
export function VoiceChip({ ms }: { ms: number | null }) {
  return (
    <span className="fm-stamp" data-tone="ask">
      <Mic className="size-3" aria-hidden="true" />
      {tx.voice(formatDuration(ms))}
    </span>
  )
}

/** A card on the board: title, first lines, thumbnails, voice chip, tags, project, task and failure stamps. */
export function IdeaCardBody({ idea }: { idea: Idea }) {
  const thumbs = idea.attachments.filter((a) => a.has_thumb).slice(0, 3)
  const files = idea.attachments.filter((a) => a.kind === 'file' && !a.has_thumb).length
  const links = idea.attachments.filter((a) => a.kind === 'link' && !a.has_thumb).length
  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex items-start gap-2">
        <span className="min-w-0 flex-1 font-medium break-words">{idea.title}</span>
        <UserBadge user={idea.created_by} className="shrink-0" />
        {idea.failed && (
          <span className="fm-stamp" data-tone="danger" title={tx.failedStamp}>
            ✕ <span className="sr-only">{tx.failedStamp}</span>
          </span>
        )}
      </div>
      {idea.body && idea.body.trim() !== idea.title && <p className="line-clamp-3 text-xs break-words whitespace-pre-line text-muted-foreground">{idea.body}</p>}
      {thumbs.length > 0 && (
        <div className="flex gap-1.5">
          {thumbs.map((a) => (
            <IdeaThumb key={a.id} ideaId={idea.id} a={a} />
          ))}
        </div>
      )}
      <div className="flex flex-wrap items-center gap-1.5">
        {idea.voice_notes.map((v) => (
          <VoiceChip key={v.id} ms={v.duration_ms} />
        ))}
        {files > 0 && <span className="fm-label text-[10px] text-muted-foreground">{tx.files(files)}</span>}
        {links > 0 && <span className="fm-label text-[10px] text-muted-foreground">{tx.links(links)}</span>}
        {idea.tags.map((t) => (
          <span key={t} className="font-mono text-[11px] text-muted-foreground">
            #{t}
          </span>
        ))}
        {idea.project_id && <span className="fm-stamp">{idea.project_id}</span>}
        {idea.linked_task_ids.length > 0 && <span className="fm-stamp">{tx.tasks(idea.linked_task_ids.length)}</span>}
      </div>
      {idea.auto_note && (
        <p className="font-mono text-[11px] text-muted-foreground">
          <span className="fm-label text-[10px]">{tx.autoNote}</span> {idea.auto_note}
        </p>
      )}
    </div>
  )
}

/** "Move to…": one big button per column (the touch alternative to dragging). */
export function MoveSheet({
  idea,
  columns,
  onMove,
  onClose,
}: {
  idea: Idea | null
  columns: IdeaColumn[]
  onMove: (column: string) => void
  onClose: () => void
}) {
  return (
    <Dialog open={idea !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogTitle>{idea ? tx.moveTitle(idea.title) : tx.moveTo}</DialogTitle>
        <DialogDescription className="sr-only">{tx.moveTo}</DialogDescription>
        <ul className="divide-y fm-field">
          {columns.map((c) => (
            <li key={c.id}>
              <button
                type="button"
                disabled={idea?.column === c.id}
                className="flex h-12 w-full items-center px-3 text-left font-medium hover:bg-grid disabled:text-muted-foreground"
                onClick={() => onMove(c.id)}
              >
                {c.name}
              </button>
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  )
}
