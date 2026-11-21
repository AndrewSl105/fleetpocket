import { useMemo, useState } from 'react'
import { Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { useCheckpointPreview, useRestoreCheckpoint } from '@/api/checkpoints'
import { errorMessage } from '@/api/hooks'
import type { Checkpoint } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { haptic } from '@/telegram/haptics'
import { cp } from '@/i18n/checkpoints'

const d = cp.dialog

/** Files a `git diff` touches, with added/removed line counts. */
export function diffFiles(diff: string): Array<{ path: string; add: number; del: number }> {
  const out: Array<{ path: string; add: number; del: number }> = []
  let cur: { path: string; add: number; del: number } | null = null
  for (const line of diff.split('\n')) {
    const m = /^diff --git a\/(.+) b\/(.+)$/.exec(line)
    if (m) {
      cur = { path: m[2], add: 0, del: 0 }
      out.push(cur)
    } else if (cur && line.startsWith('+') && !line.startsWith('+++')) cur.add++
    else if (cur && line.startsWith('-') && !line.startsWith('---')) cur.del++
  }
  return out
}

type Props = { taskId: number; checkpoint: Checkpoint; open: boolean; onOpenChange: (v: boolean) => void }

/** Confirms a restore: what it discards (later steps, file diff), plus an optional follow-up. */
export default function RestoreDialog({ taskId, checkpoint, open, onOpenChange }: Props) {
  const preview = useCheckpointPreview(taskId, checkpoint.id, open)
  const restore = useRestoreCheckpoint(taskId)
  const [instruction, setInstruction] = useState('')
  const p = preview.data
  const files = useMemo(() => diffFiles(p?.diff ?? ''), [p?.diff])
  const available = p ? p.available : checkpoint.available
  const reason = p?.unavailable_reason ?? checkpoint.unavailable_reason

  async function submit() {
    try {
      await restore.mutateAsync({ id: checkpoint.id, instruction: instruction.trim() })
      haptic.success()
      toast.success(d.done)
      onOpenChange(false)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{d.title(checkpoint.n)}</DialogTitle>
          <DialogDescription>{d.description}</DialogDescription>
        </DialogHeader>
        {!p ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground" role="status">
            <Loader2 className="size-4 animate-spin" aria-hidden="true" />
            {d.loading}
          </p>
        ) : (
          <div className="space-y-3 text-sm">
            <div className="rounded-[10px] border border-border px-3 py-2.5">
              <div className="text-[13px] font-semibold text-muted-foreground">{d.discards}</div>
              <div className="mt-1">{d.discarded(p.discarded)}</div>
              <div className="mt-1 text-muted-foreground">{d.files(files.length)}</div>
              {files.length > 0 && (
                <ul className="mt-2 max-h-40 divide-y divide-border overflow-y-auto border-t border-border font-mono text-xs">
                  {files.map((f) => (
                    <li key={f.path} className="flex items-center gap-2 py-1.5">
                      <span className="min-w-0 flex-1 truncate">{f.path}</span>
                      <span className="text-olive-text">+{f.add}</span>
                      <span className="text-danger">−{f.del}</span>
                    </li>
                  ))}
                </ul>
              )}
              {p.truncated && <div className="mt-1 text-xs text-muted-foreground">{d.truncated}</div>}
            </div>
            <p className="text-xs text-muted-foreground">{p.mode === 'fork' ? d.fork : d.summary}</p>
            {p.discarded.commits > 0 && <p className="text-xs text-muted-foreground">{d.forcePush}</p>}
          </div>
        )}
        {!available && reason && (
          <p role="alert" className="text-sm text-danger">
            {reason}
          </p>
        )}
        <div className="space-y-2">
          <Label htmlFor="restore-instruction">{d.instruction}</Label>
          <Textarea
            id="restore-instruction"
            value={instruction}
            onChange={(e) => setInstruction(e.target.value)}
            placeholder={d.instructionPlaceholder}
            rows={3}
          />
        </div>
        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
          <Button variant="outline" className="h-11" onClick={() => onOpenChange(false)}>
            {d.cancel}
          </Button>
          <Button className="h-11" onClick={submit} disabled={!p || !available || restore.isPending}>
            {restore.isPending ? d.restoring : d.confirm}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
