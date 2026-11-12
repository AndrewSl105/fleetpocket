import { useRef } from 'react'
import { File as FileIcon, ImagePlus, Paperclip, X } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ACCEPT, formatBytes } from '@/lib/attachments'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'
import type { ImagePicker } from './useImagePicker'

/**
 * The attach buttons with their hidden file inputs: images (the explicit
 * `accept` makes phones offer camera and gallery and convert HEIC) and any file.
 */
export function AttachButton({ picker, disabled, className }: { picker: ImagePicker; disabled?: boolean; className?: string }) {
  const images = useRef<HTMLInputElement>(null)
  const files = useRef<HTMLInputElement>(null)
  const button = 'size-11 shrink-0'
  return (
    <>
      <input {...picker.inputProps} ref={images} accept={ACCEPT} data-testid="attach-input" />
      <input {...picker.inputProps} ref={files} data-testid="attach-file-input" />
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={cn(button, className)}
        onClick={() => images.current?.click()}
        disabled={disabled}
        aria-label={t.attachments.attach}
        title={t.attachments.attach}
      >
        <ImagePlus />
      </Button>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className={cn(button, '-ml-1.5')}
        onClick={() => files.current?.click()}
        disabled={disabled}
        aria-label={t.attachments.attachFile}
        title={t.attachments.attachFile}
      >
        <Paperclip />
      </Button>
    </>
  )
}

/**
 * Previews of the picked files (images as thumbnails, other files by name)
 * with remove buttons, the upload progress while sending, and the reasons
 * files were refused.
 */
export function PickedImages({ picker, progress }: { picker: ImagePicker; progress: number | null }) {
  const { picked, errors } = picker
  if (picked.length === 0 && errors.length === 0 && progress === null) return null
  const uploading = progress !== null
  return (
    <div className="space-y-1.5">
      {picked.length > 0 && (
        <ul className="flex gap-2 overflow-x-auto pt-1 pb-1" aria-label={t.attachments.attach}>
          {picked.map((p) => (
            <li key={p.id} className="relative size-16 shrink-0">
              {p.url ? (
                <img
                  src={p.url}
                  alt={p.file.name}
                  title={`${p.file.name} · ${formatBytes(p.file.size)}`}
                  className={cn('size-16 rounded-lg border object-cover', uploading && 'opacity-60')}
                />
              ) : (
                <div
                  title={`${p.file.name} · ${formatBytes(p.file.size)}`}
                  className={cn(
                    'flex size-16 flex-col items-center justify-center gap-0.5 rounded-lg border bg-muted px-1 text-center',
                    uploading && 'opacity-60',
                  )}
                >
                  <FileIcon className="size-5 text-muted-foreground" aria-hidden="true" />
                  <span className="w-full truncate text-[10px] leading-tight">{p.file.name || t.attachments.pasted}</span>
                  <span className="text-[10px] leading-tight text-muted-foreground">{formatBytes(p.file.size)}</span>
                </div>
              )}
              {!uploading && (
                <button
                  type="button"
                  onClick={() => picker.remove(p.id)}
                  aria-label={t.attachments.remove(p.file.name || t.attachments.pasted)}
                  // 44 px hit area around a small visible badge.
                  className="absolute -top-3 -right-3 flex size-11 items-center justify-center"
                >
                  <span className="flex size-6 items-center justify-center win-raised text-foreground">
                    <X className="size-3.5" />
                  </span>
                </button>
              )}
            </li>
          ))}
        </ul>
      )}
      {uploading && (
        <div className="space-y-1" role="status" aria-live="polite">
          <div className="win-field h-3 overflow-hidden p-[2px]">
            <div className="h-full bg-[repeating-linear-gradient(90deg,var(--win-select)_0_6px,transparent_6px_8px)] transition-[width]" style={{ width: `${Math.round(progress * 100)}%` }} />
          </div>
          <p className="text-xs text-muted-foreground">{t.attachments.uploading(Math.round(progress * 100))}</p>
        </div>
      )}
      {errors.length > 0 && (
        <ul className="space-y-0.5 text-xs text-destructive" role="alert">
          {errors.map((e) => (
            <li key={e}>{e}</li>
          ))}
        </ul>
      )}
    </div>
  )
}

/** Overlay shown while files are dragged over a drop target. */
export function DropOverlay({ show }: { show: boolean }) {
  if (!show) return null
  return (
    <div className="pointer-events-none absolute inset-0 z-10 flex items-center justify-center border-2 border-dotted border-foreground bg-background/80 text-sm font-bold text-foreground">
      {t.attachments.dropHere}
    </div>
  )
}
