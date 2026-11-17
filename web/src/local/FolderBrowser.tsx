import { useState } from 'react'
import { ChevronRight, CornerLeftUp, Folder, FolderGit2, Loader2 } from 'lucide-react'
import { useLocalFolder } from '@/api/local'
import { isApiError } from '@/api/errors'
import { errorMessage } from '@/api/hooks'
import { Button } from '@/components/ui/button'
import { breadcrumb } from '@/lib/local'
import { cn } from '@/lib/utils'
import { lc } from '@/i18n/local'

const f = lc.folder

/**
 * Folder browser limited to the home directory (GET /local/fs): breadcrumb,
 * up one folder, subfolders with git repositories marked, and "Use this
 * folder" when the open folder is a repository.
 */
export function FolderBrowser({
  initialPath = '',
  pickLabel = f.use,
  busy,
  onPick,
}: {
  initialPath?: string
  pickLabel?: string
  busy?: boolean
  onPick: (path: string) => void
}) {
  const [path, setPath] = useState(initialPath)
  const q = useLocalFolder(path)
  const data = q.data
  const crumbs = data ? breadcrumb(data.path, data.home) : []

  if (q.isError && !data) {
    return (
      <p role="alert" className="fm-group px-3 py-2 text-sm text-danger">
        {isApiError(q.error, 'outside_home') ? f.outsideHome : errorMessage(q.error)}
      </p>
    )
  }

  return (
    <div className="fm-field" aria-label={f.browser} role="group">
      <div className="flex min-h-11 items-center gap-1 border-b border-rule px-1">
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className="size-10 shrink-0"
          aria-label={f.up}
          disabled={!data?.parent || q.isFetching}
          onClick={() => data?.parent && setPath(data.parent)}
        >
          <CornerLeftUp />
        </Button>
        <nav aria-label="Breadcrumb" className="flex min-w-0 flex-1 items-center overflow-x-auto font-mono text-xs whitespace-nowrap">
          {crumbs.map((c, i) => (
            <span key={c.path} className="flex items-center">
              {i > 0 && <span className="px-0.5 text-muted-foreground">/</span>}
              {i === crumbs.length - 1 ? (
                <span aria-current="location" className="px-1 font-semibold">
                  {c.name}
                </span>
              ) : (
                <button type="button" className="min-h-10 px-1 underline-offset-2 hover:underline" onClick={() => setPath(c.path)}>
                  {c.name}
                </button>
              )}
            </span>
          ))}
        </nav>
        {q.isFetching && <Loader2 className="mr-2 size-4 shrink-0 animate-spin text-muted-foreground" aria-hidden="true" />}
      </div>
      {q.isError && (
        <p role="alert" className="border-b border-rule px-3 py-2 text-xs text-danger">
          {isApiError(q.error, 'outside_home') ? f.outsideHome : errorMessage(q.error)}
        </p>
      )}
      <ul className={cn('max-h-64 divide-y divide-rule overflow-y-auto', q.isPlaceholderData && 'opacity-60')}>
        {!data ? (
          <li className="px-3 py-3 text-sm text-muted-foreground">…</li>
        ) : data.entries.length === 0 ? (
          <li className="px-3 py-3 text-sm text-muted-foreground">{f.empty}</li>
        ) : (
          data.entries.map((e) => (
            <li key={e.path}>
              <button
                type="button"
                className="flex min-h-11 w-full items-center gap-2.5 px-3 text-left text-sm hover:bg-grid"
                onClick={() => setPath(e.path)}
              >
                {e.is_git ? (
                  <FolderGit2 className="size-4 shrink-0 text-olive-text" aria-hidden="true" />
                ) : (
                  <Folder className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                )}
                <span className="min-w-0 flex-1 truncate">{e.name}</span>
                {e.is_git && (
                  <span className="fm-stamp" data-tone="branch">
                    {f.git}
                  </span>
                )}
                <ChevronRight className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
              </button>
            </li>
          ))
        )}
      </ul>
      <div className="flex flex-wrap items-center gap-2 border-t border-rule px-3 py-2">
        <p className="min-w-0 flex-1 text-xs text-muted-foreground">{data && !data.is_git ? f.notGit : ''}</p>
        <Button type="button" className="h-11" disabled={!data?.is_git || busy || q.isPlaceholderData} onClick={() => data && onPick(data.path)}>
          {pickLabel}
        </Button>
      </div>
    </div>
  )
}
