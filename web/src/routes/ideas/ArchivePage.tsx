import { Link } from 'react-router'
import { RotateCcw, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { useArchiveIdea, useDeleteIdea, useIdeas } from '@/api/ideas'
import { errorMessage } from '@/api/hooks'
import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useConfirm } from '@/components/confirm'
import { id as tx } from '@/i18n/ideas'
import { IdeaCardBody } from './ideaParts'

/** Archived ideas: bring one back, or delete it for good. */
export default function ArchivePage() {
  const list = useIdeas({ archived: true })
  const archive = useArchiveIdea()
  const del = useDeleteIdea()
  const confirm = useConfirm()
  const fail = (e: unknown) => toast.error(errorMessage(e))
  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-2xl px-4 pt-4 pb-6">
        <div className="mb-3 flex items-center gap-2">
          <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">{tx.archived}</h1>
          <Button asChild variant="outline" className="h-11">
            <Link to="/ideas">{tx.backToBoard}</Link>
          </Button>
        </div>
        {list.isPending ? (
          <Skeleton className="h-20" />
        ) : list.isError ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{errorMessage(list.error) || tx.loadError}</p>
        ) : !list.data.length ? (
          <p className="py-10 text-center text-sm text-muted-foreground">{tx.archiveEmpty}</p>
        ) : (
          <ul className="divide-y fm-field" aria-label={tx.archived}>
            {list.data.map((idea) => (
              <li key={idea.id} className="flex items-start gap-1" data-testid="archived-idea">
                <Link to={`/ideas/${idea.id}`} className="block min-w-0 flex-1 p-3 hover:bg-grid">
                  <IdeaCardBody idea={idea} />
                </Link>
                <Button variant="ghost" size="icon" className="mt-1 size-11" aria-label={`${tx.unarchive}: ${idea.title}`} onClick={() => archive.mutate({ id: idea.id, archived: false }, { onError: fail })}>
                  <RotateCcw />
                </Button>
                <Button
                  variant="ghost"
                  size="icon"
                  className="mt-1 mr-1 size-11 text-destructive"
                  aria-label={`${tx.deleteForGood}: ${idea.title}`}
                  onClick={() => void confirm(tx.deleteConfirm(idea.title)).then((ok) => ok && del.mutate(idea.id, { onSuccess: () => toast.success(tx.deleted), onError: fail }))}
                >
                  <Trash2 />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  )
}
