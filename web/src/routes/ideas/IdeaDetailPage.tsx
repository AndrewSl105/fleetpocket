import { useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { CalendarClock, Download, ExternalLink, File as FileIcon, Paperclip, Play, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import {
  fetchIdeaFile,
  useAddIdeaFiles,
  useAddIdeaLink,
  useAddIdeaVoice,
  useArchiveIdea,
  useDeleteIdea,
  useIdea,
  useIdeaFile,
  useIdeaSettings,
  useMoveIdea,
  useRemoveIdeaAttachment,
  useUpdateIdea,
  type Recording,
} from '@/api/ideas'
import { errorMessage, useProjects } from '@/api/hooks'
import type { Idea, IdeaAttachment } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Skeleton } from '@/components/ui/skeleton'
import { StatusBadge } from '@/components/StatusBadge'
import { useConfirm } from '@/components/confirm'
import { formatBytes } from '@/lib/attachments'
import { formatDuration, ideaPrompt, parseTags } from '@/lib/ideas'
import { haptic } from '@/telegram/haptics'
import { id as tx } from '@/i18n/ideas'
import { canRecord, HoldToTalk } from './HoldToTalk'
import { IdeaThumb, useBlobSrc, VoiceChip } from './ideaParts'

/** What NewTaskPage and the schedule editor take from an idea (router state). */
export type IdeaPrefill = { ideaId: number; title: string; prompt: string; projectId: string | null; files: Pick<IdeaAttachment, 'id' | 'name' | 'mime'>[] }

export function prefillOf(idea: Idea): IdeaPrefill {
  return {
    ideaId: idea.id,
    title: idea.title,
    prompt: ideaPrompt(idea),
    projectId: idea.project_id,
    files: idea.attachments.filter((a) => a.kind === 'file').map((a) => ({ id: a.id, name: a.name, mime: a.mime })),
  }
}

/** One idea: edit it, its attachments, and turn it into a task or a schedule. */
export default function IdeaDetailPage() {
  const ideaId = Number(useParams().id)
  const idea = useIdea(ideaId)
  if (idea.isPending) {
    return (
      <div className="mx-auto max-w-2xl space-y-3 p-4">
        <Skeleton className="h-10" />
        <Skeleton className="h-40" />
      </div>
    )
  }
  if (idea.isError) return <p className="py-10 text-center text-sm text-muted-foreground">{errorMessage(idea.error) || tx.loadError}</p>
  return <Detail key={idea.data.updated_at} idea={idea.data} />
}

function Detail({ idea }: { idea: Idea }) {
  const navigate = useNavigate()
  const confirm = useConfirm()
  const settings = useIdeaSettings()
  const projects = useProjects()
  const update = useUpdateIdea(idea.id)
  const move = useMoveIdea(['ideas', 'detail', idea.id])
  const archive = useArchiveIdea()
  const del = useDeleteIdea()
  const addFiles = useAddIdeaFiles(idea.id)
  const addLink = useAddIdeaLink(idea.id)
  const addVoice = useAddIdeaVoice(idea.id)
  const remove = useRemoveIdeaAttachment(idea.id)
  const [title, setTitle] = useState(idea.title)
  const [body, setBody] = useState(idea.body)
  const [tags, setTags] = useState(idea.tags.join(', '))
  const [project, setProject] = useState(idea.project_id ?? '')
  const [link, setLink] = useState('')
  const fileInput = useRef<HTMLInputElement>(null)
  const columns = settings.data?.columns ?? []
  const dirty = title !== idea.title || body !== idea.body || tags !== idea.tags.join(', ') || project !== (idea.project_id ?? '')

  const run = async (p: Promise<unknown>, ok?: string) => {
    try {
      await p
      haptic.success()
      if (ok) toast.success(ok)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  const files = idea.attachments.filter((a) => a.kind === 'file')
  const links = idea.attachments.filter((a) => a.kind === 'link')
  const prefill = prefillOf(idea)

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl space-y-5 px-4 pt-4 pb-8">
        <div className="flex items-center gap-2">
          <Link to={idea.archived_at ? '/ideas/archive' : '/ideas'} className="fm-label text-[13px] text-muted-foreground hover:underline">
            ← {idea.archived_at ? tx.archived : tx.backToBoard}
          </Link>
          {idea.failed && (
            <span className="fm-stamp" data-tone="danger">
              ✕ {tx.failedStamp}
            </span>
          )}
        </div>

        <form
          className="space-y-4"
          onSubmit={(e) => {
            e.preventDefault()
            void run(update.mutateAsync({ title, body, tags: parseTags(tags), project_id: project }), tx.saved)
          }}
        >
          <div className="space-y-2">
            <Label htmlFor="idea-title">{tx.titleLabel}</Label>
            <Input id="idea-title" value={title} maxLength={200} required onChange={(e) => setTitle(e.target.value)} className="h-11 text-base" />
          </div>
          <div className="space-y-2">
            <Label htmlFor="idea-body">{tx.body}</Label>
            <Textarea id="idea-body" value={body} onChange={(e) => setBody(e.target.value)} rows={6} className="text-base" />
            <p className="text-xs text-muted-foreground">{tx.bodyHint}</p>
          </div>
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-2">
              <Label htmlFor="idea-tags">{tx.tags}</Label>
              <Input id="idea-tags" value={tags} onChange={(e) => setTags(e.target.value)} placeholder={tx.tagsHint} className="h-11 text-base" />
            </div>
            <div className="space-y-2">
              <Label htmlFor="idea-project">{tx.project}</Label>
              <select id="idea-project" value={project} onChange={(e) => setProject(e.target.value)} className="h-11 w-full fm-field bg-transparent px-2 text-base">
                <option value="">{tx.noProject}</option>
                {projects.data?.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.name}
                  </option>
                ))}
              </select>
            </div>
          </div>
          <Button type="submit" className="h-11" disabled={!dirty || update.isPending}>
            {tx.save}
          </Button>
        </form>

        {!idea.archived_at && (
          <div className="space-y-2">
            <Label htmlFor="idea-column">{tx.column}</Label>
            <select
              id="idea-column"
              value={idea.column}
              onChange={(e) => void run(move.mutateAsync({ id: idea.id, column: e.target.value, afterId: null }))}
              className="h-11 w-full fm-field bg-transparent px-2 text-base"
            >
              {columns.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
            {idea.auto_note && <p className="font-mono text-xs text-muted-foreground">{idea.auto_note}</p>}
          </div>
        )}

        <div className="flex flex-wrap gap-2">
          <Button className="h-11" onClick={() => void navigate('/new', { state: { idea: prefill } })}>
            <Play /> {tx.startTask}
          </Button>
          <Button variant="outline" className="h-11" onClick={() => void navigate('/schedules/new', { state: { idea: prefill } })}>
            <CalendarClock /> {tx.schedule}
          </Button>
        </div>

        {idea.linked_tasks.length > 0 && (
          <section aria-labelledby="idea-tasks">
            <h2 id="idea-tasks" className="mb-2 fm-strip px-2 py-1">
              {tx.linkedTasks}
            </h2>
            <ul className="divide-y fm-field">
              {idea.linked_tasks.map((t) => (
                <li key={t.id}>
                  <Link to={`/tasks/${t.id}`} className="flex min-h-11 items-center gap-2 px-3 py-2 hover:bg-grid">
                    <span className="font-mono text-xs text-muted-foreground">#{t.id}</span>
                    <span className="min-w-0 flex-1 truncate text-sm">{t.title}</span>
                    <StatusBadge status={t.status} />
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        )}

        <section aria-labelledby="idea-voice" className="space-y-2">
          <h2 id="idea-voice" className="fm-strip px-2 py-1">
            {tx.voiceNotes}
          </h2>
          {idea.voice_notes.map((v) => (
            <VoiceNote key={v.id} ideaId={idea.id} v={v} onRemove={() => void confirm(tx.removeConfirm(v.name)).then((ok) => (ok ? run(remove.mutateAsync(v.id)) : undefined))} />
          ))}
          {canRecord() && <HoldToTalk compact label={tx.addVoice} busy={addVoice.isPending} onRecorded={(rec: Recording) => void run(addVoice.mutateAsync(rec))} />}
        </section>

        <section aria-labelledby="idea-files" className="space-y-2">
          <h2 id="idea-files" className="fm-strip px-2 py-1">
            {tx.attachments}
          </h2>
          {(files.length > 0 || links.length > 0) && (
            <ul className="divide-y fm-field">
              {files.map((a) => (
                <li key={a.id} className="flex items-center gap-2 px-2 py-1.5">
                  {a.has_thumb ? <IdeaThumb ideaId={idea.id} a={a} /> : <FileIcon className="mx-2 size-5 text-muted-foreground" aria-hidden="true" />}
                  <span className="min-w-0 flex-1 truncate text-sm">{a.name}</span>
                  <span className="font-mono text-xs text-muted-foreground">{formatBytes(a.size)}</span>
                  <Button variant="ghost" size="icon" className="size-11" aria-label={`Download ${a.name}`} onClick={() => void download(idea.id, a)}>
                    <Download />
                  </Button>
                  <Button variant="ghost" size="icon" className="size-11" aria-label={tx.remove(a.name)} onClick={() => void confirm(tx.removeConfirm(a.name)).then((ok) => (ok ? run(remove.mutateAsync(a.id)) : undefined))}>
                    <Trash2 />
                  </Button>
                </li>
              ))}
              {links.map((a) => (
                <li key={a.id} className="flex items-center gap-2 px-2 py-1.5">
                  {a.has_thumb ? <IdeaThumb ideaId={idea.id} a={a} /> : <ExternalLink className="mx-2 size-5 text-muted-foreground" aria-hidden="true" />}
                  <a href={a.url ?? '#'} target="_blank" rel="noopener noreferrer" className="min-w-0 flex-1 text-sm hover:underline">
                    <span className="block truncate font-medium">{a.title ?? a.url}</span>
                    <span className="block truncate font-mono text-xs text-muted-foreground">{a.url}</span>
                  </a>
                  <Button variant="ghost" size="icon" className="size-11" aria-label={tx.remove(a.name)} onClick={() => void run(remove.mutateAsync(a.id))}>
                    <Trash2 />
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <form
            className="flex gap-2"
            onSubmit={(e) => {
              e.preventDefault()
              if (link.trim()) void run(addLink.mutateAsync(link.trim()).then(() => setLink('')))
            }}
          >
            <Input value={link} onChange={(e) => setLink(e.target.value)} placeholder={tx.linkPlaceholder} aria-label={tx.addLink} type="url" className="h-11 flex-1" />
            <Button type="submit" variant="outline" className="h-11" disabled={addLink.isPending}>
              {tx.addLink}
            </Button>
          </form>
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              const picked = e.target.files ? [...e.target.files] : []
              e.target.value = ''
              if (picked.length) void run(addFiles.mutateAsync(picked))
            }}
          />
          <Button variant="outline" className="h-11" disabled={addFiles.isPending} onClick={() => fileInput.current?.click()}>
            <Paperclip /> {tx.addFiles}
          </Button>
        </section>

        <div className="flex flex-wrap gap-2 border-t border-rule pt-4">
          {idea.archived_at ? (
            <>
              <Button variant="outline" className="h-11" onClick={() => void run(archive.mutateAsync({ id: idea.id, archived: false }))}>
                {tx.unarchive}
              </Button>
              <Button
                variant="outline"
                className="h-11 text-destructive"
                onClick={() =>
                  void confirm(tx.deleteConfirm(idea.title)).then(async (ok) => {
                    if (!ok) return
                    await run(del.mutateAsync(idea.id), tx.deleted)
                    void navigate('/ideas/archive', { replace: true })
                  })
                }
              >
                <Trash2 /> {tx.deleteForGood}
              </Button>
            </>
          ) : (
            <Button variant="outline" className="h-11" onClick={() => void run(archive.mutateAsync({ id: idea.id, archived: true })).then(() => navigate('/ideas'))}>
              {tx.archiveIdea}
            </Button>
          )}
        </div>
      </div>
    </div>
  )
}

function VoiceNote({ ideaId, v, onRemove }: { ideaId: number; v: IdeaAttachment; onRemove: () => void }) {
  const file = useIdeaFile(ideaId, v, false)
  // The server sends the recording's own type; the Blob gets it too so <audio> can play it.
  const blob = useMemo(() => (file.data ? new Blob([file.data], { type: v.mime }) : undefined), [file.data, v.mime])
  const audio = useBlobSrc<HTMLAudioElement>(blob)
  return (
    <div className="space-y-1.5 fm-group p-2.5">
      <div className="flex items-center gap-2">
        <VoiceChip ms={v.duration_ms} />
        <span className="font-mono text-xs text-muted-foreground">{formatDuration(v.duration_ms)}</span>
        <Button variant="ghost" size="icon" className="ml-auto size-11" aria-label={tx.remove(v.name)} onClick={onRemove}>
          <Trash2 />
        </Button>
      </div>
      {blob && <audio ref={audio} controls className="w-full" preload="metadata" />}
      <p className="text-sm whitespace-pre-line">{v.transcript ?? <span className="text-muted-foreground">{tx.noTranscript}</span>}</p>
    </div>
  )
}

async function download(ideaId: number, a: IdeaAttachment) {
  try {
    const blob = await fetchIdeaFile(ideaId, a)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = a.name
    link.click()
    setTimeout(() => URL.revokeObjectURL(url), 60_000)
  } catch (e) {
    toast.error(errorMessage(e))
  }
}
