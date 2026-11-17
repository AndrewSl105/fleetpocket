import { useEffect, useState, type FormEvent } from 'react'
import { isApiError } from '@/api/errors'
import { signup, signupStart } from '@/api/team'
import type { Enrollment } from '@/api/types'
import { errorMessage } from '@/api/hooks'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Logo } from '@/components/Logo'
import { Window } from '@/components/Window'
import { TotpQR } from '@/components/TotpQR'
import { tm } from '@/i18n/team'

/**
 * /signup?token=… (team mode, no session): name, email and password, then
 * TOTP enrolment (QR + a code) before the account exists. On success the
 * server signs the person in and the app opens.
 */
export function SignupPage({ onDone }: { onDone: () => void }) {
  const token = new URLSearchParams(window.location.search).get('token') ?? ''
  const [enrol, setEnrol] = useState<Enrollment | null>(null)
  const [gone, setGone] = useState(false)
  const [step, setStep] = useState<'details' | 'totp'>('details')
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let live = true
    signupStart(token)
      .then((e) => {
        if (!live) return
        setEnrol(e)
        if (e.email) setEmail(e.email)
      })
      .catch(() => live && setGone(true))
    return () => {
      live = false
    }
  }, [token])

  function next(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (password.length < 12) return setError(tm.signupPassword)
    setStep('totp')
  }

  async function finish(e: FormEvent) {
    e.preventDefault()
    if (!enrol || busy) return
    setBusy(true)
    setError(null)
    try {
      await signup({ token, enrollment_id: enrol.enrollment_id, name, email, password, totp: code })
      // Signed in now: open the app from its start (the router was created at /signup).
      onDone()
      window.location.assign('/')
    } catch (err) {
      if (isApiError(err, 'invite_gone')) setGone(true)
      setError(errorMessage(err))
      setCode('')
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="fm-ground flex min-h-(--app-height) items-center justify-center px-3 py-10">
      <Window title={tm.signupTitle} className="w-full max-w-sm" bodyClassName="p-3">
        <div className="mb-4 flex items-center gap-3">
          <Logo className="size-10" />
          <h1 className="text-[18px] font-bold">{tm.signupTitle}</h1>
        </div>
        {gone ? (
          <p role="alert" className="text-sm text-destructive">
            {tm.signupBadLink}
          </p>
        ) : !enrol ? (
          <p className="text-sm text-muted-foreground">{tm.signupOpening}</p>
        ) : step === 'details' ? (
          <form onSubmit={next} className="space-y-4" noValidate>
            <p className="text-sm">{tm.signupIntro(tm.roles[enrol.role])}</p>
            <div className="space-y-1">
              <Label htmlFor="su-name">{tm.name}</Label>
              <Input id="su-name" autoComplete="name" value={name} onChange={(e) => setName(e.target.value)} className="h-10" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="su-email">{tm.email}</Label>
              <Input id="su-email" type="email" autoComplete="email" readOnly={!!enrol.email} value={email} onChange={(e) => setEmail(e.target.value)} className="h-10" />
            </div>
            <div className="space-y-1">
              <Label htmlFor="su-password">{tm.signupPassword}</Label>
              <Input id="su-password" type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} className="h-10" />
            </div>
            {error && (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            )}
            <Button type="submit" className="h-11 w-full" disabled={!name.trim() || !email.trim() || password.length < 12}>
              {tm.signupNext}
            </Button>
          </form>
        ) : (
          <form onSubmit={finish} className="space-y-4" noValidate>
            <p className="text-sm">{tm.signupScan}</p>
            <TotpQR uri={enrol.otpauth_uri} alt={tm.signupScan} />
            <p className="text-center text-xs">
              {tm.signupSecret}: <code className="font-mono break-all" data-testid="totp-secret">{enrol.totp_secret}</code>
            </p>
            <div className="space-y-1">
              <Label htmlFor="su-code">{tm.signupCode}</Label>
              <Input
                id="su-code"
                inputMode="numeric"
                autoComplete="one-time-code"
                maxLength={6}
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, '').slice(0, 6))}
                className="h-10 font-mono tracking-[0.4em]"
              />
            </div>
            {error && (
              <p role="alert" className="text-sm text-destructive">
                {error}
              </p>
            )}
            <Button type="submit" className="h-11 w-full" disabled={busy || code.length !== 6}>
              {tm.signupFinish}
            </Button>
          </form>
        )}
      </Window>
    </main>
  )
}

export default SignupPage
