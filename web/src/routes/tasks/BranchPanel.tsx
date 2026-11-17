import { useState } from 'react'
import { toast } from 'sonner'
import { FolderSearch, GitBranch, GitMerge, GitPullRequest, Loader2, Trash2 } from 'lucide-react'
import type { TaskView } from '@/api/types'
import { useBranchAction, useLocalGitHub, type BranchActionKind } from '@/api/local'
import { Button } from '@/components/ui/button'
import { CopyButton } from '@/components/CopyButton'
import { useConfirm } from '@/components/confirm'
import { branchError, canPushPr, isGitHubRemote, isLocalRepo, waitsForFolder, type BranchError } from '@/lib/local'
import { haptic } from '@/telegram/haptics'
import { t } from '@/i18n/en'
import { lc } from '@/i18n/local'
import { LocateDialog } from '@/local/LocateDialog'

const b = lc.branch

/**
 * A local repository's delivered branch (`branch_ready`): Merge into the
 * current branch, Check out, Discard, and Push & open PR when the repository
 * has a GitHub remote and GitHub is connected. 409s stay on screen, with the
 * conflict files. A delivery waiting for a moved folder offers "Locate…".
 */
export function BranchPanel({ task }: { task: TaskView }) {
  const ready = task.status === 'branch_ready' && !!task.local_branch
  const waiting = waitsForFolder(task)
  if (!ready && !waiting) return null
  return ready ? <BranchReady task={task} /> : <FolderMissing task={task} message={task.status_reason ?? b.errors.repo_missing} />
}

function FolderMissing({ task, message }: { task: TaskView; message: string }) {
  const [open, setOpen] = useState(false)
  const repoId = task.repository?.id ?? task.repository_id
  return (
    <div role="alert" className="mt-2 flex flex-wrap items-center gap-2 border border-dashed border-danger px-2.5 py-1.5 text-xs text-danger">
      <p className="min-w-0 flex-1 break-words">{message}</p>
      <Button type="button" variant="outline" className="h-11" onClick={() => setOpen(true)}>
        <FolderSearch /> {lc.repo.locate}
      </Button>
      <LocateDialog repoId={repoId} open={open} onOpenChange={setOpen} />
    </div>
  )
}

function BranchReady({ task }: { task: TaskView }) {
  const branch = task.local_branch!
  const confirm = useConfirm()
  const action = useBranchAction(task.id)
  const repo = task.repository
  const gh = useLocalGitHub(isLocalRepo(repo) && isGitHubRemote(repo?.remote_url))
  const [error, setError] = useState<(BranchError & { code?: string }) | null>(null)
  const pushable = canPushPr(repo, gh.data)

  function run(kind: BranchActionKind) {
    setError(null)
    action.mutate(kind, {
      onSuccess: () => {
        haptic.success()
        toast.success(b.done[kind])
      },
      onError: (e) => {
        haptic.error()
        setError({ ...branchError(e, kind), code: (e as { code?: string }).code })
      },
    })
  }

  async function discard() {
    if (await confirm(b.confirmDiscard(branch), b.discard)) run('discard')
  }

  const busy = action.isPending
  const pending = busy ? action.variables : undefined
  const spin = (kind: BranchActionKind) => pending === kind && <Loader2 className="animate-spin" aria-hidden="true" />

  return (
    <section aria-label={b.stamp} className="mt-2 border-[1.5px] border-olive-text px-2.5 py-2">
      <div className="flex items-center gap-2 text-sm">
        <GitBranch className="size-4 shrink-0 text-olive-text" aria-hidden="true" />
        <p className="min-w-0 flex-1 break-words">
          {b.ready(branch)}
        </p>
        <CopyButton text={branch} label={t.task.actions.copyBranch} className="-my-1" />
      </div>
      <div className="mt-2 grid grid-cols-2 gap-2">
        <Button type="button" className="col-span-2 h-11" disabled={busy} onClick={() => run('merge')}>
          {spin('merge') || <GitMerge />} {b.merge}
        </Button>
        <Button type="button" variant="outline" className="h-11" disabled={busy} onClick={() => run('checkout')}>
          {spin('checkout') || <GitBranch />} {b.checkout}
        </Button>
        <Button type="button" variant="destructive" className="h-11" disabled={busy} onClick={() => void discard()}>
          {spin('discard') || <Trash2 />} {b.discard}
        </Button>
        {pushable && (
          <Button type="button" variant="outline" className="col-span-2 h-11" disabled={busy} onClick={() => run('push')}>
            {spin('push') || <GitPullRequest />} {b.push}
          </Button>
        )}
      </div>
      {error && (
        <div role="alert" className="mt-2 bg-grid px-2.5 py-2 text-xs text-danger">
          <p className="break-words">{error.message}</p>
          {error.files.length > 0 && (
            <ul className="mt-1 list-inside list-disc font-mono">
              {error.files.map((f) => (
                <li key={f} className="break-all">
                  {f}
                </li>
              ))}
            </ul>
          )}
          {error.code === 'repo_missing' && <LocateInline task={task} />}
        </div>
      )}
    </section>
  )
}

function LocateInline({ task }: { task: TaskView }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button type="button" variant="outline" className="mt-2 h-11" onClick={() => setOpen(true)}>
        <FolderSearch /> {lc.repo.locate}
      </Button>
      <LocateDialog repoId={task.repository?.id ?? task.repository_id} open={open} onOpenChange={setOpen} />
    </>
  )
}
