import { useState, type FormEvent } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, call, CSRF } from '@/api/client'
import { isApiError } from '@/api/errors'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { t } from '@/i18n/en'

/** Message for the callback's ?github_error=<reason> redirect. */
export function githubErrorMessage(reason: string | null): string | null {
  if (!reason) return null
  return t.setup.githubErrors[reason as keyof typeof t.setup.githubErrors] ?? t.common.somethingWrong
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
      setError(isApiError(err) && err.status === 400 ? t.setup.badOrg : t.common.somethingWrong)
      setBusy(false)
    }
  }

  if (status.data?.github_configured) {
    return (
      <div className="space-y-3">
        <p className="text-sm">{t.setup.githubReady}</p>
        <div className="flex justify-end">
          <Button className="h-10 min-w-28" onClick={onNext}>
            {t.setup.next}
          </Button>
        </div>
      </div>
    )
  }

  return (
    <form onSubmit={submit} className="space-y-3" noValidate>
      <p className="text-sm">{t.setup.githubIntro}</p>
      <fieldset className="space-y-2 text-sm">
        <legend className="sr-only">{t.setup.githubOwner}</legend>
        <label className="flex min-h-10 items-center gap-2">
          <input type="radio" name="owner" checked={owner === 'user'} onChange={() => setOwner('user')} />
          {t.setup.githubPersonal}
        </label>
        <label className="flex min-h-10 items-center gap-2">
          <input type="radio" name="owner" checked={owner === 'org'} onChange={() => setOwner('org')} />
          {t.setup.githubOrg}
        </label>
      </fieldset>
      {owner === 'org' && (
        <div className="space-y-1">
          <Label htmlFor="setup-org">{t.setup.orgName}</Label>
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
          {t.setup.skip}
        </Button>
        <Button type="submit" className="h-10 min-w-28" disabled={busy || (owner === 'org' && !org.trim())}>
          {t.setup.createApp}
        </Button>
      </div>
    </form>
  )
}
