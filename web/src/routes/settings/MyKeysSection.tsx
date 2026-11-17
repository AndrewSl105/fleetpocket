import { useState, type FormEvent } from 'react'
import { toast } from 'sonner'
import { KeyRound } from 'lucide-react'
import { errorMessage } from '@/api/hooks'
import { useCredentials, useDeleteCredential, usePutCredential, useTestCredential } from '@/api/team'
import type { Agent, Credential } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Skeleton } from '@/components/ui/skeleton'
import { useConfirm } from '@/components/confirm'
import { relativeTime } from '@/lib/format'
import { haptic } from '@/telegram/haptics'
import { tm } from '@/i18n/team'

const PROVIDERS: Agent[] = ['claude', 'codex', 'cursor']
type Kind = 'oauth_token' | 'api_key'

function KeyForm({ provider, onDone }: { provider: Agent; onDone: () => void }) {
  const put = usePutCredential()
  const [kind, setKind] = useState<Kind>('api_key')
  const [value, setValue] = useState('')
  const [label, setLabel] = useState('')

  async function submit(e: FormEvent) {
    e.preventDefault()
    try {
      await put.mutateAsync({ provider, kind, value: value.trim(), label: label.trim() || undefined })
      setValue('')
      haptic.success()
      toast.success(tm.keySaved)
      onDone()
    } catch (err) {
      haptic.error()
      toast.error(errorMessage(err))
    }
  }

  return (
    <form onSubmit={submit} className="space-y-2 pt-2">
      {provider === 'claude' && (
        <div role="radiogroup" aria-label={tm.providers.claude} className="grid grid-cols-1 gap-1 text-xs">
          {(['api_key', 'oauth_token'] as Kind[]).map((k) => (
            <label key={k} className="flex min-h-9 items-center gap-2">
              <input type="radio" name={`kind-${provider}`} checked={kind === k} onChange={() => setKind(k)} />
              {tm.kinds[k]}
            </label>
          ))}
          {kind === 'oauth_token' && <p className="text-muted-foreground">{tm.subscriptionWarning}</p>}
        </div>
      )}
      <div className="space-y-1">
        <Label htmlFor={`key-${provider}`}>{tm.keyValue}</Label>
        <Input
          id={`key-${provider}`}
          type="password"
          autoComplete="off"
          spellCheck={false}
          value={value}
          onChange={(e) => setValue(e.target.value)}
          className="h-10 font-mono"
        />
      </div>
      <div className="space-y-1">
        <Label htmlFor={`label-${provider}`}>{tm.keyLabel}</Label>
        <Input id={`label-${provider}`} value={label} onChange={(e) => setLabel(e.target.value)} className="h-10" />
      </div>
      <Button type="submit" className="h-11 w-full" disabled={put.isPending || !value.trim()}>
        {tm.keySave}
      </Button>
    </form>
  )
}

function ProviderRow({ provider, cred }: { provider: Agent; cred?: Credential }) {
  const [editing, setEditing] = useState(false)
  const del = useDeleteCredential()
  const test = useTestCredential()
  const confirm = useConfirm()

  async function remove() {
    if (!(await confirm(`${tm.keyRemove} ${tm.providers[provider]}?`, tm.keyRemove))) return
    del.mutate(provider, { onSuccess: () => toast.success(tm.keyRemoved), onError: (e) => toast.error(errorMessage(e)) })
  }
  async function runTest() {
    try {
      const r = await test.mutateAsync(provider)
      if (r.status === 'ok') toast.success(r.message)
      else if (r.status === 'not_testable') toast.message(r.message)
      else toast.error(r.message)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  return (
    <li className="px-3.5 py-3" data-testid={`key-${provider}`}>
      <div className="flex min-w-0 items-center gap-2">
        <KeyRound className="size-4 shrink-0" aria-hidden="true" />
        <div className="min-w-0 flex-1">
          <p className="text-sm font-medium">{tm.providers[provider]}</p>
          <p className="truncate font-mono text-xs text-muted-foreground">
            {cred
              ? `${tm.kinds[cred.kind]} ${cred.last4}${cred.label ? ` · ${cred.label}` : ''} · ${cred.last_used_at ? tm.lastUsed(relativeTime(cred.last_used_at)) : tm.neverUsed}`
              : tm.keyNone}
          </p>
        </div>
      </div>
      <div className="mt-2 flex flex-wrap gap-2">
        <Button variant="outline" className="h-9" onClick={() => setEditing((v) => !v)}>
          {cred ? tm.keyReplace : tm.keyAdd}
        </Button>
        {cred && (
          <>
            <Button variant="outline" className="h-9" onClick={runTest} disabled={test.isPending}>
              {tm.keyTest}
            </Button>
            <Button variant="outline" className="h-9 text-danger" onClick={remove} disabled={del.isPending}>
              {tm.keyRemove}
            </Button>
          </>
        )}
      </div>
      {editing && <KeyForm provider={provider} onDone={() => setEditing(false)} />}
    </li>
  )
}

/** Settings → My keys (team mode): each person's agent keys, write-only. */
export function MyKeysSection() {
  const creds = useCredentials()
  const byProvider = new Map(creds.data?.items.map((c) => [c.provider, c]))
  return (
    <section aria-labelledby="keys-title" id="keys">
      <h2 id="keys-title" className="mb-1 text-sm font-bold">
        {tm.keysTitle}
      </h2>
      <p className="mb-3 text-xs text-muted-foreground">
        {tm.keysHint} {creds.data && (creds.data.shared_key_fallback ? tm.fallbackOn : tm.fallbackOff)}
      </p>
      {creds.data ? (
        <ul className="divide-y fm-group">
          {PROVIDERS.map((p) => (
            <ProviderRow key={p} provider={p} cred={byProvider.get(p)} />
          ))}
        </ul>
      ) : (
        <Skeleton className="h-32" />
      )}
    </section>
  )
}
