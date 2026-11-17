import { toast } from 'sonner'
import { useLocateRepo } from '@/api/local'
import { errorMessage } from '@/api/hooks'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { haptic } from '@/telegram/haptics'
import { lc } from '@/i18n/local'
import { FolderBrowser } from './FolderBrowser'

/** "Locate…": pick the repository's new folder (POST /local/repos/{id}/locate). */
export function LocateDialog({ repoId, open, onOpenChange }: { repoId: number; open: boolean; onOpenChange: (v: boolean) => void }) {
  const locate = useLocateRepo()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{lc.repo.locateTitle}</DialogTitle>
          <DialogDescription>{lc.repo.missingHint}</DialogDescription>
        </DialogHeader>
        {open && (
          <FolderBrowser
            busy={locate.isPending}
            onPick={(path) =>
              locate.mutate(
                { repoId, path },
                {
                  onSuccess: () => {
                    haptic.success()
                    toast.success(lc.repo.located)
                    onOpenChange(false)
                  },
                  onError: (e) => {
                    haptic.error()
                    toast.error(errorMessage(e))
                  },
                },
              )
            }
          />
        )}
      </DialogContent>
    </Dialog>
  )
}
