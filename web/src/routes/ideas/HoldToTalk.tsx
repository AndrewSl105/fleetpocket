import { useEffect, useRef, useState } from 'react'
import { Loader2, Mic, X } from 'lucide-react'
import { toast } from 'sonner'
import type { Recording } from '@/api/ideas'
import { formatDuration, recorderMime } from '@/lib/ideas'
import { cn } from '@/lib/utils'
import { haptic } from '@/telegram/haptics'
import { id as tx } from '@/i18n/ideas'

/** Whether this browser (or Telegram WebView) can record audio. */
export function canRecord(): boolean {
  return typeof window !== 'undefined' && typeof window.MediaRecorder !== 'undefined' && !!navigator.mediaDevices?.getUserMedia
}

const CANCEL_DISTANCE = 80 // px the pointer moves away to arm cancel
const MIN_MS = 600
const MAX_MS = 5 * 60_000

type Phase = 'idle' | 'starting' | 'recording'

type Session = {
  stream: MediaStream
  recorder: MediaRecorder
  chunks: Blob[]
  started: number
  audio?: AudioContext
  raf?: number
  mime: string
}

/**
 * Hold-to-talk: press and hold (pointer, or Space/Enter) to record, release
 * to save, slide away to cancel. Shows a live timer and an input level meter.
 */
export function HoldToTalk({
  onRecorded,
  busy,
  label = tx.hold,
  compact,
}: {
  onRecorded: (rec: Recording) => void | Promise<void>
  busy?: boolean
  label?: string
  compact?: boolean
}) {
  const [phase, setPhase] = useState<Phase>('idle')
  const [elapsed, setElapsed] = useState(0)
  const [level, setLevel] = useState(0)
  const [cancelArmed, setCancelArmed] = useState(false)
  const session = useRef<Session | null>(null)
  const origin = useRef<{ x: number; y: number } | null>(null)
  // Released (or cancelled) while the microphone was still starting.
  const pending = useRef<'save' | 'cancel' | null>(null)
  const cancelRef = useRef(false)
  const deliver = useRef(onRecorded)
  deliver.current = onRecorded

  useEffect(() => () => teardown(session.current), [])

  useEffect(() => {
    if (phase !== 'recording') return
    const timer = window.setInterval(() => {
      const s = session.current
      if (!s) return
      const ms = Date.now() - s.started
      setElapsed(ms)
      if (ms >= MAX_MS) finish('save')
    }, 200)
    return () => window.clearInterval(timer)
    // finish only reads refs
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [phase])

  async function start() {
    if (phase !== 'idle' || busy) return
    if (!canRecord()) {
      toast.error(tx.noMic)
      return
    }
    pending.current = null
    cancelRef.current = false
    setCancelArmed(false)
    setElapsed(0)
    setPhase('starting')
    let stream: MediaStream
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    } catch {
      setPhase('idle')
      toast.error(tx.micDenied)
      return
    }
    const mime = recorderMime((t) => MediaRecorder.isTypeSupported(t))
    let recorder: MediaRecorder
    try {
      recorder = mime ? new MediaRecorder(stream, { mimeType: mime }) : new MediaRecorder(stream)
    } catch {
      stream.getTracks().forEach((t) => t.stop())
      setPhase('idle')
      toast.error(tx.noMic)
      return
    }
    const s: Session = { stream, recorder, chunks: [], started: Date.now(), mime: recorder.mimeType || mime || 'audio/webm' }
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) s.chunks.push(e.data)
    }
    recorder.start(250)
    session.current = s
    meter(s)
    haptic.tap()
    setPhase('recording')
    if (pending.current) finish(pending.current)
  }

  function meter(s: Session) {
    try {
      const Ctx = window.AudioContext ?? (window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext
      if (!Ctx) return
      const audio = new Ctx()
      const analyser = audio.createAnalyser()
      analyser.fftSize = 256
      audio.createMediaStreamSource(s.stream).connect(analyser)
      const data = new Uint8Array(analyser.frequencyBinCount)
      s.audio = audio
      let last = 0
      const tick = (now: number) => {
        analyser.getByteTimeDomainData(data)
        let peak = 0
        for (const v of data) peak = Math.max(peak, Math.abs(v - 128))
        if (now - last > 80) {
          setLevel(Math.min(1, peak / 64))
          last = now
        }
        s.raf = requestAnimationFrame(tick)
      }
      s.raf = requestAnimationFrame(tick)
    } catch {
      // No meter; recording still works.
    }
  }

  function finish(how: 'save' | 'cancel') {
    const s = session.current
    if (!s) {
      if (phase === 'starting') pending.current = how
      return
    }
    session.current = null
    const ms = Date.now() - s.started
    s.recorder.onstop = () => {
      teardown(s)
      if (how === 'cancel') {
        toast(tx.cancelled)
        return
      }
      if (ms < MIN_MS) {
        toast(tx.tooShort)
        return
      }
      haptic.success()
      void deliver.current({ blob: new Blob(s.chunks, { type: s.mime }), mime: s.mime, durationMs: ms })
    }
    try {
      s.recorder.stop()
    } catch {
      teardown(s)
    }
    setPhase('idle')
    setLevel(0)
    setCancelArmed(false)
  }

  const recording = phase === 'recording'
  return (
    <div className={cn('select-none', compact ? 'space-y-1' : 'space-y-2')}>
      <button
        type="button"
        disabled={busy}
        aria-pressed={recording}
        aria-label={busy ? tx.saving : recording ? tx.recording : label}
        className={cn(
          'flex w-full touch-none items-center justify-center gap-3 font-semibold disabled:opacity-60',
          compact ? 'h-12 text-sm' : 'h-20 text-lg',
          recording ? (cancelArmed ? 'fm-ask' : 'fm-pressed') : 'fm-btn-signal',
        )}
        style={{ WebkitTouchCallout: 'none' }}
        onContextMenu={(e) => e.preventDefault()}
        onPointerDown={(e) => {
          if (e.button !== 0) return
          e.preventDefault()
          try {
            // Keep getting the pointer's moves when it slides off the button (to cancel).
            e.currentTarget.setPointerCapture(e.pointerId)
          } catch {
            // Not an active pointer (synthetic events): recording still works.
          }
          origin.current = { x: e.clientX, y: e.clientY }
          void start()
        }}
        onPointerMove={(e) => {
          if (!origin.current || phase === 'idle') return
          const away = Math.hypot(e.clientX - origin.current.x, e.clientY - origin.current.y) > CANCEL_DISTANCE
          if (away !== cancelRef.current) {
            cancelRef.current = away
            setCancelArmed(away)
            if (away) haptic.select()
          }
        }}
        onPointerUp={() => {
          origin.current = null
          finish(cancelRef.current ? 'cancel' : 'save')
        }}
        onPointerCancel={() => {
          origin.current = null
          finish('cancel')
        }}
        onKeyDown={(e) => {
          if ((e.key === ' ' || e.key === 'Enter') && !e.repeat) {
            e.preventDefault()
            void start()
          }
          if (e.key === 'Escape') finish('cancel')
        }}
        onKeyUp={(e) => {
          if (e.key === ' ' || e.key === 'Enter') {
            e.preventDefault()
            finish('save')
          }
        }}
      >
        {busy ? (
          <Loader2 className="size-5 animate-spin" aria-hidden="true" />
        ) : cancelArmed ? (
          <X className="size-5" aria-hidden="true" />
        ) : (
          <Mic className={cn('size-5', recording && 'fm-blink')} aria-hidden="true" />
        )}
        {busy ? tx.saving : recording ? <span className="font-mono">{formatDuration(elapsed)}</span> : label}
      </button>
      {recording ? (
        <div className="flex items-center gap-2" aria-live="polite">
          <div className="h-2 flex-1 fm-well" role="meter" aria-label={tx.level} aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(level * 100)}>
            <div className="h-full bg-signal transition-[width] duration-75" style={{ width: `${Math.round(level * 100)}%` }} />
          </div>
          <span className="fm-label text-[13px] text-muted-foreground">{cancelArmed ? tx.cancelZone : tx.holdHint}</span>
        </div>
      ) : (
        !compact && <p className="text-center text-xs text-muted-foreground">{tx.holdHint}</p>
      )}
    </div>
  )
}

function teardown(s: Session | null) {
  if (!s) return
  if (s.raf) cancelAnimationFrame(s.raf)
  s.stream.getTracks().forEach((t) => t.stop())
  void s.audio?.close().catch(() => undefined)
}
