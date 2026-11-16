import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import QRCode from 'qrcode'
import { api, call, CSRF } from '@/api/client'
import { isApiError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Logo } from '@/components/Logo'
import { Window } from '@/components/Window'
import { CopyButton } from '@/components/CopyButton'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'
import { GitHubStep } from './GitHubStep'

export type SetupStep = 'password' | 'totp' | 'github' | 'repo'
const STEPS: SetupStep[] = ['password', 'totp', 'github', 'repo']

export function setupErrorMessage(e: unknown): string {
  if (isApiError(e, 'bad_setup_code')) return t.setup.badCode
  if (isApiError(e, 'setup_done')) return t.setup.done
  if (isApiError(e, 'bad_credentials')) return t.setup.badTotp
  if (isApiError(e, 'rate_limited')) return t.auth.tooMany(15)
  if (isApiError(e) && e.status === 400) return e.message
  return t.common.somethingWrong
}

/** Where the wizard starts: GitHub's redirect back (?step=github|repo) or step 1. */
export function initialStep(search: URLSearchParams): SetupStep {
  const s = search.get('step')
  return s === 'github' || s === 'repo' ? s : 'password'
}

function StepIndicator({ step }: { step: SetupStep }) {
  const current = STEPS.indexOf(step)
  return (
    <ol className="flex gap-1 text-xs" aria-label={t.setup.progress}>
      {STEPS.map((s, i) => (
        <li
          key={s}
          aria-current={i === current ? 'step' : undefined}
          className={cn(
            'flex-1 rounded-sm border px-1 py-1 text-center',
            i === current ? 'border-primary bg-primary text-primary-foreground' : i < current ? 'opacity-70' : 'opacity-50',
          )}
        >
          {i + 1}. {t.setup.steps[s]}
        </li>
      ))}
    </ol>
  )
}

function ErrorText({ error }: { error: string | null }) {
  return error ? (
    <p role="alert" className="text-sm text-destructive">
      {error}
    </p>
  ) : null
}

function QR({ uri }: { uri: string }) {
  const [src, setSrc] = useState<string | null>(null)
  useEffect(() => {
    let live = true
    void QRCode.toDataURL(uri, { margin: 1, width: 200 }).then((url) => live && setSrc(url))
    return () => {
      live = false
    }
  }, [uri])
  return src ? <img src={src} alt={t.setup.qrAlt} width={200} height={200} className="mx-auto bg-white" /> : null
}

export function SetupPage() {
  const params = new URLSearchParams(window.location.search)
  const [step, setStep] = useState<SetupStep>(() => initialStep(params))
  const [code, setCode] = useState(params.get('code') ?? '')
  const [password, setPassword] = useState('')
  const [repeat, setRepeat] = useState('')
  const [totp, setTotp] = useState('')
  const [otp, setOtp] = useState<{ otpauth_uri: string; secret: string } | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function run(fn: () => Promise<void>) {
    if (busy) return
    setBusy(true)
    setError(null)
    try {
      await fn()
    } catch (e) {
      setError(setupErrorMessage(e))
    } finally {
      setBusy(false)
    }
  }

  function submitPassword(e: FormEvent) {
    e.preventDefault()
    if (password.length < 12) return setError(t.setup.passwordShort)
    if (password !== repeat) return setError(t.setup.passwordMismatch)
    void run(async () => {
      setOtp(await call(api.POST('/api/v1/setup/owner', { params: { header: CSRF }, body: { code: code.trim(), password } })))
      setStep('totp')
    })
  }

  function submitTotp(e: FormEvent) {
    e.preventDefault()
    void run(async () => {
      await call(api.POST('/api/v1/setup/owner/confirm', { params: { header: CSRF }, body: { code: code.trim(), totp } }))
      setStep('github')
    }).finally(() => setTotp(''))
  }

  let body: ReactNode
  switch (step) {
    case 'password':
      body = (
        <form onSubmit={submitPassword} className="space-y-3" noValidate>
          <p className="text-sm">{t.setup.passwordIntro}</p>
          <div className="space-y-1">
            <Label htmlFor="setup-code">{t.setup.code}</Label>
            <Input id="setup-code" value={code} onChange={(e) => setCode(e.target.value)} autoComplete="off" className="h-10 font-mono" />
          </div>
          <div className="space-y-1">
            <Label htmlFor="setup-password">{t.auth.password}</Label>
            <Input id="setup-password" type="password" autoComplete="new-password" autoFocus value={password} onChange={(e) => setPassword(e.target.value)} className="h-10" />
          </div>
          <div className="space-y-1">
            <Label htmlFor="setup-repeat">{t.setup.repeat}</Label>
            <Input id="setup-repeat" type="password" autoComplete="new-password" value={repeat} onChange={(e) => setRepeat(e.target.value)} className="h-10" />
          </div>
          <ErrorText error={error} />
          <div className="flex justify-end">
            <Button type="submit" className="h-10 min-w-28" disabled={busy || !code || !password}>
              {t.setup.next}
            </Button>
          </div>
        </form>
      )
      break
    case 'totp':
      body = (
        <form onSubmit={submitTotp} className="space-y-3" noValidate>
          <p className="text-sm">{t.setup.totpIntro}</p>
          {otp && <QR uri={otp.otpauth_uri} />}
          {otp && (
            <div className="flex items-center gap-2 text-xs">
              <code className="min-w-0 flex-1 break-all">{otp.secret}</code>
              <CopyButton text={otp.secret} />
            </div>
          )}
          <div className="space-y-1">
            <Label htmlFor="setup-totp">{t.auth.code}</Label>
            <Input
              id="setup-totp"
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9]*"
              maxLength={6}
              value={totp}
              onChange={(e) => setTotp(e.target.value.replace(/\D/g, '').slice(0, 6))}
              className="h-10 font-mono tracking-[0.4em]"
            />
          </div>
          <ErrorText error={error} />
          <div className="flex justify-end">
            <Button type="submit" className="h-10 min-w-28" disabled={busy || totp.length !== 6}>
              {t.setup.confirm}
            </Button>
          </div>
        </form>
      )
      break
    case 'github':
      body = <GitHubStep onNext={() => setStep('repo')} />
      break
    case 'repo':
      body = (
        <div className="space-y-3">
          <p className="text-sm">{t.setup.repoIntro}</p>
          <div className="flex justify-end">
            <Button asChild className="h-10 min-w-28">
              <a href="/projects/new">{t.setup.addRepo}</a>
            </Button>
          </div>
        </div>
      )
      break
  }

  return (
    <main className="win-desktop flex min-h-(--app-height) items-center justify-center px-3 py-10">
      <Window title={t.setup.title} className="w-full max-w-md" bodyClassName="space-y-4 p-3">
        <div className="flex items-center gap-3">
          <Logo className="size-10 shrink-0" />
          <h1 className="text-sm">{t.setup.title}</h1>
        </div>
        <StepIndicator step={step} />
        {body}
      </Window>
    </main>
  )
}
