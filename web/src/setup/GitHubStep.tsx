import { useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, call, CSRF } from '@/api/client'
import { isApiError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n/en'
import { st } from '@/i18n/setup'

/** Message for the callback's ?github_error=<reason> redirect. */
export function githubErrorMessage(reason: string | null): string | null {
  if (!reason) return null
  return st.githubErrors[reason as keyof typeof st.githubErrors] ?? t.common.somethingWrong
}

/** GitHub's manifest flow is a top-level form POST; fetch cannot follow it. */
function postManifest(actionUrl: string, manifest: unknown) {
  const form = document.createElement('form')
  form.method = 'post'
  form.action = actionUrl
  const input = document.createElement('input')
  input.type = 'hidden'
  input.name = 'manifest'
  input.value = JSON.stringify(manifest)
  form.appendChild(input)
  document.body.appendChild(form)
  form.submit()
}

export function GitHubStep({ onNext }: { onNext: () => void }) {
  const status = useQuery({ queryKey: ['setup-status'], queryFn: () => call(api.GET('/api/v1/setup/status')) })
  const [owner, setOwner] = useState<'user' | 'org'>('user')
  const [org, setOrg] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(() =>
    githubErrorMessage(new URLSearchParams(window.location.search).get('github_error')),
  )

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const r = await call(
        api.POST('/api/v1/setup/github/manifest', {
          params: { header: CSRF },
          body: { organization: owner === 'org' ? org.trim() : '' },
        }),
      )
      postManifest(r.action_url, r.manifest)
    } catch (err) {
      setError(isApiError(err) && err.status === 400 ? st.badOrg : t.common.somethingWrong)
      setBusy(false)
    }
  }

  if (status.data?.github_configured) {
    return (
      <div className="space-y-3">
        <p className="text-sm">{st.githubReady}</p>
        <div className="flex justify-end">
          <Button className="h-10 min-w-28" onClick={onNext}>
            {st.next}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <form onSubmit={submit} className="space-y-3" noValidate>
      <p className="text-sm">{st.githubIntro}</p>
      <fieldset className="space-y-2 text-sm">
        <legend className="sr-only">{st.githubOwner}</legend>
        <label className="flex min-h-10 items-center gap-2">
          <input type="radio" name="owner" checked={owner === 'user'} onChange={() => setOwner('user')} />
          {st.githubPersonal}
        </label>
        <label className="flex min-h-10 items-center gap-2">
          <input type="radio" name="owner" checked={owner === 'org'} onChange={() => setOwner('org')} />
          {st.githubOrg}
        </label>
      </fieldset>
      {owner === 'org' && (
        <div className="space-y-1">
          <Label htmlFor="setup-org">{st.orgName}</Label>
          <Input id="setup-org" value={org} onChange={(e) => setOrg(e.target.value)} autoComplete="off" className="h-10" />
        </div>
      )}
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex flex-wrap justify-end gap-2">
        <Button type="button" variant="outline" className="h-10" onClick={onNext}>
          {st.skip}
        </Button>
        <Button type="submit" className="h-10 min-w-28" disabled={busy || (owner === 'org' && !org.trim())}>
          {st.createApp}
        </Button>
      </div>
    </form>
  )
}
