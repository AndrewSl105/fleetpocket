import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { errorMessage } from '@/api/hooks'
import { useConfirmTotpReset, useCurrentUser, useStartTotpReset, useUpdateProfile } from '@/api/team'
import type { Enrollment } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { TotpQR } from '@/components/TotpQR'
import { haptic } from '@/telegram/haptics'
import { tm } from '@/i18n/team'

function Field({ id, label, hint, ...props }: { id: string; label: string; hint?: string } & React.ComponentProps<typeof Input>) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      <Input id={id} className="h-10" {...props} />
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  )
}

function ok(msg: string) {
  haptic.success()
  toast.success(msg)
}
function fail(e: unknown) {
  haptic.error()
  toast.error(errorMessage(e))
}

/** Settings → Profile (team mode): name, email, GitHub login, commit email, password, TOTP reset. */
export function ProfileSection() {
  const me = useCurrentUser()
  const update = useUpdateProfile()
  const startTotp = useStartTotpReset()
  const confirmTotp = useConfirmTotpReset()
  const [name, setName] = useState(me?.name ?? '')
  const [email, setEmail] = useState(me?.email ?? '')
  const [gh, setGh] = useState(me?.github_login ?? '')
  const [commit, setCommit] = useState(me?.commit_email ?? '')
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [enrol, setEnrol] = useState<Enrollment | null>(null)
  const [totpPassword, setTotpPassword] = useState('')
  const [code, setCode] = useState('')
  if (!me) return null

  function saveProfile(e: FormEvent) {
    e.preventDefault()
    update.mutate({ name, email, github_login: gh, commit_email: commit }, { onSuccess: () => ok(tm.profileSaved), onError: fail })
  }
  function savePassword(e: FormEvent) {
    e.preventDefault()
    update.mutate(
      { current_password: current, new_password: next },
      {
        onSuccess: () => {
          setCurrent('')
          setNext('')
          ok(tm.passwordSaved)
        },
        onError: fail,
      },
    )
  }
  function saveTotp(e: FormEvent) {
    e.preventDefault()
    if (!enrol) return
    confirmTotp.mutate(
      { enrollment_id: enrol.enrollment_id, password: totpPassword, totp: code },
      {
        onSuccess: () => {
          setEnrol(null)
          setCode('')
          setTotpPassword('')
          ok(tm.totpSaved)
        },
        onError: fail,
      },
    )
  }

  return (
    <section aria-labelledby="profile-title" id="profile" className="space-y-3">
      <h2 id="profile-title" className="text-sm font-bold">
        {tm.profileTitle}
      </h2>
      <form onSubmit={saveProfile} className="fm-group space-y-3 px-3.5 py-3">
        <Field id="p-name" label={tm.name} value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" />
        <Field id="p-email" label={tm.email} type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" />
        <Field id="p-gh" label={tm.githubLogin} value={gh} onChange={(e) => setGh(e.target.value)} placeholder="octocat" />
        <Field id="p-commit" label={tm.commitEmail} hint={tm.commitEmailHint} type="email" value={commit} onChange={(e) => setCommit(e.target.value)} />
        <Button type="submit" className="h-11 w-full" disabled={update.isPending}>
          {tm.saveProfile}
        </Button>
      </form>
      <form onSubmit={savePassword} className="fm-group space-y-3 px-3.5 py-3">
        <p className="text-sm font-medium">{tm.password}</p>
        <Field id="p-cur" label={tm.currentPassword} type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} />
        <Field id="p-new" label={tm.newPassword} type="password" autoComplete="new-password" value={next} onChange={(e) => setNext(e.target.value)} />
        <Button type="submit" variant="outline" className="h-11 w-full" disabled={update.isPending || !current || next.length < 12}>
          {tm.savePassword}
        </Button>
      </form>
      <div className="fm-group space-y-3 px-3.5 py-3">
        {!enrol ? (
          <Button variant="outline" className="h-11 w-full" onClick={() => startTotp.mutate(undefined, { onSuccess: setEnrol, onError: fail })} disabled={startTotp.isPending}>
            {tm.totpReset}
          </Button>
        ) : (
          <form onSubmit={saveTotp} className="space-y-3">
            <p className="text-sm">{tm.totpScan}</p>
            <TotpQR uri={enrol.otpauth_uri} alt={tm.totpReset} />
            <p className="text-center font-mono text-xs break-all">{enrol.totp_secret}</p>
            <Field id="t-pw" label={tm.currentPassword} type="password" value={totpPassword} onChange={(e) => setTotpPassword(e.target.value)} />
            <Field id="t-code" label={tm.signupCode} inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ''))} />
            <Button type="submit" className="h-11 w-full" disabled={confirmTotp.isPending || code.length !== 6 || !totpPassword}>
              {tm.totpConfirm}
            </Button>
          </form>
        )}
      </div>
    </section>
  )
}
