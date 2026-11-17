import { useState } from 'react'
import { FolderSearch, TriangleAlert } from 'lucide-react'
import { useLocalRepoStatus } from '@/api/local'
import type { Repository } from '@/api/types'
import { Button } from '@/components/ui/button'
import { lc } from '@/i18n/local'
import { LocateDialog } from './LocateDialog'

const r = lc.repo

/** A local repository on the project page: path, current branch, dirty state; "Locate…" when the folder is gone. */
export function LocalRepoInfo({ repo }: { repo: Repository }) {
  const status = useLocalRepoStatus(repo.id)
  const [locating, setLocating] = useState(false)
  const s = status.data
  return (
    <div className="mt-0.5 space-y-0.5 text-xs text-muted-foreground">
      <div className="truncate font-mono" title={repo.path}>
        {repo.path}
      </div>
      {s && !s.missing && (
        <div className="flex flex-wrap items-center gap-x-2 font-mono">
          <span>
            ⎇ {s.detached ? lc.folder.detached : s.branch}
          </span>
          <span className={s.dirty ? 'text-signal-text' : 'text-olive-text'}>{s.dirty ? lc.folder.dirty : lc.folder.clean}</span>
        </div>
      )}
      {s?.missing && (
        <div role="alert" className="mt-1 flex flex-wrap items-center gap-2 text-danger">
          <TriangleAlert className="size-3.5 shrink-0" aria-hidden="true" />
          <span className="min-w-0 flex-1 font-semibold">{r.missing}</span>
          <Button type="button" variant="outline" className="h-11" onClick={() => setLocating(true)}>
            <FolderSearch /> {r.locate}
          </Button>
          <LocateDialog repoId={repo.id} open={locating} onOpenChange={setLocating} />
        </div>
      )}
    </div>
  )
}
