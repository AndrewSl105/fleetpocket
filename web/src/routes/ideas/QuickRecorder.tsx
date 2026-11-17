import { Link } from 'react-router'
import { toast } from 'sonner'
import { useVoiceIdea, type Recording } from '@/api/ideas'
import { errorMessage } from '@/api/hooks'
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/ui/dialog'
import { haptic } from '@/telegram/haptics'
import { id as tx } from '@/i18n/ideas'
import { HoldToTalk } from './HoldToTalk'

/** The header's recorder: hold to talk, release to save a new idea in Inbox. */
export default function QuickRecorder({ onClose }: { onClose: () => void }) {
  const voice = useVoiceIdea()
  async function save(rec: Recording) {
    try {
      const idea = await voice.mutateAsync({ rec })
      toast.success(tx.savedVoice(idea.title))
      onClose()
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-sm">
        <DialogTitle>{tx.recordIdea}</DialogTitle>
        <DialogDescription className="sr-only">{tx.holdHint}</DialogDescription>
        <HoldToTalk onRecorded={save} busy={voice.isPending} />
        <Link to="/ideas" onClick={onClose} className="inline-flex min-h-11 items-center text-sm underline">
          {tx.openBoard}
        </Link>
      </DialogContent>
    </Dialog>
  )
}
