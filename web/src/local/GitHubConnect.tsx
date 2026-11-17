import { useEffect, useRef, useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { CircleCheck, ExternalLink, KeyRound, Loader2, LogIn, Terminal } from 'lucide-react'
import {
  useDisconnectGitHub,
  useImportGhToken,
  useLocalGitHub,
  usePollDeviceFlow,
  useSetGitHubToken,
  useStartDeviceFlow,
} from '@/api/local'
import { errorMessage } from '@/api/hooks'
import type { DeviceCode } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { CopyButton } from '@/components/CopyButton'
import { useConfirm } from '@/components/confirm'
import { haptic } from '@/telegram/haptics'
import { lc } from '@/i18n/local'

const g = lc.github

type Device = DeviceCode & { problem?: string }

/**
 * Connect GitHub in local mode (optional; for Push & open PR): the OAuth
 * device flow (code + link, polled every `interval` seconds), the GitHub CLI
 * login (asked first) or a pasted fine-grained token. Connected: the login,
 * the scopes and Disconnect.
 */
export function GitHubConnect({ onConnected }: { onConnected?: () => void }) {
  const status = useLocalGitHub()
  const confirm = useConfirm()
  const disconnect = useDisconnectGitHub()
  const importGh = useImportGhToken()
  const setToken = useSetGitHubToken()
  const [device, setDevice] = useState<Device | null>(null)
  const [pat, setPat] = useState('')
  const [error, setError] = useState<string | null>(null)
  const gh = status.data

  function connected() {
    haptic.success()
    toast.success(g.connectedToast)
    setDevice(null)
    setPat('')
    setError(null)
    onConnected?.()
  }
  const failed = (e: unknown) => {
    haptic.error()
    setError(errorMessage(e))
  }

  async function importFromGh() {
    if (!(await confirm(g.confirmGh, g.gh))) return
    setError(null)
    importGh.mutate(undefined, { onSuccess: (s) => (s.connected ? connected() : setError(s.error || null)), onError: failed })
  }

  function savePat(e: FormEvent) {
    e.preventDefault()
    if (!pat.trim()) return
    setError(null)
    setToken.mutate(pat.trim(), { onSuccess: (s) => (s.connected ? connected() : setError(s.error || null)), onError: failed })
  }

  async function forget() {
    if (!(await confirm(g.confirmDisconnect, g.disconnect))) return
    disconnect.mutate(undefined, { onSuccess: () => toast.success(g.disconnected), onError: failed })
  }

  if (status.isPending) {
    return <Loader2 className="size-4 animate-spin text-muted-foreground" aria-label="Loading" />
  }
  if (status.isError) {
    return (
      <p role="alert" className="text-sm text-danger">
        {errorMessage(status.error)}
      </p>
    )
  }

  if (gh?.connected) {
    return (
      <div className="flex flex-wrap items-center gap-3 fm-group px-3 py-2.5">
        <CircleCheck className="size-5 shrink-0 text-olive-text" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <div className="text-sm font-semibold">{g.connected(gh.login)}</div>
          <div className="font-mono text-xs text-muted-foreground">{g.scopes(gh.scopes)}</div>
        </div>
        <Button type="button" variant="outline" className="h-11" disabled={disconnect.isPending} onClick={() => void forget()}>
          {g.disconnect}
        </Button>
      </div>
    )
  }

  return (
    <div className="space-y-3">
      {gh?.error && (
        <p role="alert" className="text-sm text-danger">
          {g.tokenError(gh.error)}
        </p>
      )}
      {device ? (
        <DeviceFlow device={device} onDone={connected} onProblem={(problem) => setDevice({ ...device, problem })} onCancel={() => setDevice(null)} />
      ) : (
        <div className="grid gap-2">
          {gh?.device_flow && <StartDevice onStarted={setDevice} onError={failed} />}
          {gh?.gh_cli && (
            <Button type="button" variant="outline" className="h-11" disabled={importGh.isPending} onClick={() => void importFromGh()}>
              <Terminal /> {g.gh}
            </Button>
          )}
        </div>
      )}
      {!device && (
        <form onSubmit={savePat} className="space-y-2 fm-group p-3">
          <Label htmlFor="gh-pat" className="flex items-center gap-1.5">
            <KeyRound className="size-4" aria-hidden="true" /> {g.patLabel}
          </Label>
          <Input
            id="gh-pat"
            type="password"
            autoComplete="off"
            spellCheck={false}
            value={pat}
            onChange={(e) => setPat(e.target.value)}
            placeholder="github_pat_…"
            className="h-11 font-mono"
          />
          <p className="text-xs text-muted-foreground">{g.patHint}</p>
          <div className="flex justify-end">
            <Button type="submit" variant="outline" className="h-11" disabled={!pat.trim() || setToken.isPending}>
              {g.patSave}
            </Button>
          </div>
        </form>
      )}
      {error && (
        <p role="alert" className="text-sm text-danger">
          {error}
        </p>
      )}
    </div>
  )
}

function StartDevice({ onStarted, onError }: { onStarted: (d: Device) => void; onError: (e: unknown) => void }) {
  const start = useStartDeviceFlow()
  return (
    <Button type="button" className="h-11" disabled={start.isPending} onClick={() => start.mutate(undefined, { onSuccess: onStarted, onError })}>
      {start.isPending ? <Loader2 className="animate-spin" aria-hidden="true" /> : <LogIn />} {g.device}
    </Button>
  )
}

/** The code and link; polls every `interval` seconds until GitHub answers. */
function DeviceFlow({ device, onDone, onProblem, onCancel }: { device: Device; onDone: () => void; onProblem: (p: string) => void; onCancel: () => void }) {
  const poll = usePollDeviceFlow()
  const mutate = useRef(poll.mutate)
  const callbacks = useRef({ onDone, onProblem })
  useEffect(() => {
    mutate.current = poll.mutate
    callbacks.current = { onDone, onProblem }
  })

  useEffect(() => {
    if (device.problem) return
    let live = true
    let timer: ReturnType<typeof setTimeout>
    const deadline = Date.now() + device.expires_in * 1000
    const schedule = () => {
      timer = setTimeout(tick, Math.max(1, device.interval) * 1000)
    }
    const tick = () => {
      if (!live) return
      if (Date.now() > deadline) return callbacks.current.onProblem(g.deviceExpired)
      mutate.current(undefined, {
        onSuccess: (res) => {
          if (!live) return
          if (res.status === 'connected') callbacks.current.onDone()
          else if (res.status === 'pending') schedule()
          else if (res.status === 'expired' || res.status === 'none') callbacks.current.onProblem(g.deviceExpired)
          else callbacks.current.onProblem(g.deviceDenied)
        },
        onError: (e) => live && callbacks.current.onProblem(errorMessage(e)),
      })
    }
    schedule()
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [device])

  return (
    <div className="space-y-3 fm-ask p-3">
      <p className="text-sm">{g.deviceCode}</p>
      <div className="flex items-center gap-2">
        <code className="fm-stamp px-3 py-1.5 text-lg tracking-[0.2em]" aria-label="Device code">
          {device.user_code}
        </code>
        <CopyButton text={device.user_code} />
      </div>
      <Button asChild variant="outline" className="h-11 w-full">
        <a href={device.verification_uri} target="_blank" rel="noreferrer">
          <ExternalLink /> {g.deviceOpen}
        </a>
      </Button>
      {device.problem ? (
        <p role="alert" className="text-sm text-danger">
          {device.problem}
        </p>
      ) : (
        <p className="flex items-center gap-2 text-xs text-muted-foreground" role="status">
          <Loader2 className="size-3.5 animate-spin" aria-hidden="true" /> {g.deviceWaiting}
        </p>
      )}
      <div className="flex justify-end">
        <Button type="button" variant="ghost" className="h-11" onClick={onCancel}>
          {g.deviceCancel}
        </Button>
      </div>
    </div>
  )
}
