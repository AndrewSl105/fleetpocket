import { useState } from 'react'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { useSaveIdeaSettings } from '@/api/ideas'
import { errorMessage } from '@/api/hooks'
import type { IdeaSettings } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { id as tx } from '@/i18n/ideas'

const FIXED = ['inbox', 'shaping', 'ready', 'in_progress', 'done']
const MAX = 8

type Row = { id?: string; name: string; key: string }

/** Rename, reorder and add columns (the five defaults cannot be removed). */
export function ColumnsDialog({ settings, onClose }: { settings: IdeaSettings; onClose: () => void }) {
  const [rows, setRows] = useState<Row[]>(settings.columns.map((c) => ({ id: c.id, name: c.name, key: c.id })))
  const save = useSaveIdeaSettings()
  const swap = (i: number, j: number) =>
    setRows((r) => {
      if (j < 0 || j >= r.length) return r
      const next = [...r]
      ;[next[i], next[j]] = [next[j], next[i]]
      return next
    })

  async function submit() {
    try {
      await save.mutateAsync({ enabled: settings.enabled, columns: rows.map((r) => ({ ...(r.id ? { id: r.id } : {}), name: r.name.trim() })) })
      toast.success(tx.saved)
      onClose()
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-md">
        <DialogTitle>{tx.columnsTitle}</DialogTitle>
        <DialogDescription className="text-xs">{tx.columnsHint}</DialogDescription>
        <form
          className="space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            void submit()
          }}
        >
          <ol className="space-y-2">
            {rows.map((r, i) => (
              <li key={r.key} className="flex items-center gap-1">
                <Input value={r.name} maxLength={40} required aria-label={tx.columnName(i + 1)} className="h-11 flex-1" onChange={(e) => setRows((all) => all.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))} />
                <Button type="button" variant="ghost" size="icon" className="size-11" disabled={i === 0} aria-label={tx.up(r.name)} onClick={() => swap(i, i - 1)}>
                  <ArrowUp />
                </Button>
                <Button type="button" variant="ghost" size="icon" className="size-11" disabled={i === rows.length - 1} aria-label={tx.down(r.name)} onClick={() => swap(i, i + 1)}>
                  <ArrowDown />
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="icon"
                  className="size-11"
                  disabled={!!r.id && FIXED.includes(r.id)}
                  aria-label={tx.removeColumn(r.name)}
                  onClick={() => setRows((all) => all.filter((_, j) => j !== i))}
                >
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ol>
          <div className="flex flex-wrap justify-between gap-2">
            <Button type="button" variant="outline" className="h-11" disabled={rows.length >= MAX} onClick={() => setRows((r) => [...r, { name: '', key: `new-${Date.now()}` }])}>
              <Plus /> {tx.addColumn}
            </Button>
            <Button type="submit" className="h-11" disabled={save.isPending}>
              {tx.saveColumns}
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  )
}
