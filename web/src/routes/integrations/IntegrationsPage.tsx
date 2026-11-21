import { useState, type ReactNode } from 'react'
import { Link, useParams } from 'react-router'
import { ArrowLeft, TriangleAlert } from 'lucide-react'
import { toast } from 'sonner'
import { errorMessage, useIntegrations, useProject, useSaveIntegrations } from '@/api/hooks'
import { isApiError } from '@/api/errors'
import type { Egress, Integrations } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { PageSpinner } from '@/components/PageSpinner'
import { NotFound } from '@/routes/NotFound'
import { cn } from '@/lib/utils'
import { haptic } from '@/telegram/haptics'
import { t } from '@/i18n/en'
import { ti } from '@/i18n/integrations'
import { invalidHosts, parseHosts } from './form'
import { ServerList } from './ServerList'

const it = ti
const eg = it.egress

function Section({ title, hint, children }: { title: string; hint?: string; children: ReactNode }) {
  return (
    <section className="space-y-3">
      <h2 className="fm-strip-muted px-0.5 pb-1">{title}</h2>
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      {children}
    </section>
  )
}

function Warning({ children }: { children: ReactNode }) {
  return (
    <p role="alert" className="flex gap-2 fm-ask px-3 py-2.5 text-sm">
      <TriangleAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span>{children}</span>
    </p>
  )
}

/** Project → Integrations: MCP servers (own and shared) and network access. */
export default function IntegrationsPage() {
  const { id = '' } = useParams()
  const project = useProject(id)
  const data = useIntegrations(id)
  if (project.isPending || data.isPending) return <PageSpinner />
  if (project.isError || data.isError) {
    const e = project.error ?? data.error
    return <NotFound title={isApiError(e, 'not_found') ? t.system.notFound : t.common.somethingWrong} />
  }
  return (
    <div className="mx-auto max-w-2xl space-y-7 px-4 pt-3 pb-24">
      <header className="flex items-center gap-2">
        <Button asChild variant="ghost" size="icon" className="size-11 shrink-0">
          <Link to={`/projects/${id}`} aria-label={t.common.back}>
            <ArrowLeft />
          </Link>
        </Button>
        <h1 className="fm-strip flex min-h-7 min-w-0 flex-1 items-center px-2 text-base">
          <span className="truncate">
            {project.data.name} · {it.title}
          </span>
        </h1>
      </header>
      <p className="text-sm text-muted-foreground">{it.hint}</p>
      {!data.data.secrets_enabled && <Warning>{it.secretsOff}</Warning>}
      <ActiveSummary data={data.data} />
      <Section title={it.servers}>
        <ServerList servers={data.data.servers} projectId={id} empty={it.noServers} />
      </Section>
      <SharedServers projectId={id} data={data.data} />
      <EgressSection key={JSON.stringify(data.data.egress)} projectId={id} data={data.data} />
    </div>
  )
}

function ActiveSummary({ data }: { data: Integrations }) {
  const names = data.active.map((s) => s.name).join(', ')
  return (
    <div className="space-y-2 fm-well px-3.5 py-3 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-medium">{it.willGet(names || it.none)}</span>
        {data.egress.mode === 'allowlist' && (
          <span className="fm-stamp" data-tone="signal">
            {it.allowlistStamp}
          </span>
        )}
      </div>
      <div>
        <span className="fm-label text-muted-foreground">{it.agentSupport}</span>
        <ul className="mt-1 space-y-0.5 text-xs text-muted-foreground">
          {data.agents.map((a) => (
            <li key={a.agent}>
              <span className="font-medium text-foreground">{(t.agents as Record<string, string>)[a.agent] ?? a.agent}</span>: {a.note}
            </li>
          ))}
        </ul>
      </div>
    </div>
  )
}

function SharedServers({ projectId, data }: { projectId: string; data: Integrations }) {
  const save = useSaveIntegrations(projectId)
  const selected = new Set(data.global_server_ids)
  async function toggle(id: number, on: boolean) {
    const ids = data.global_servers.map((s) => s.id).filter((x) => (x === id ? on : selected.has(x)))
    try {
      await save.mutateAsync({ global_server_ids: ids })
      haptic.select()
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }
  return (
    <Section title={it.global} hint={it.globalHint}>
      {data.global_servers.length === 0 ? (
        <p className="fm-group px-3 py-3 text-sm text-muted-foreground">{it.noGlobal}</p>
      ) : (
        <ul className="divide-y fm-group">
          {data.global_servers.map((s) => (
            <li key={s.id}>
              <label className="flex min-h-11 items-center justify-between gap-3 px-3 py-2">
                <span className="min-w-0">
                  <span className="font-mono text-sm font-semibold">{s.name}</span>
                  <span className="ml-2 fm-label text-muted-foreground">{s.kind}</span>
                  {!s.enabled && <span className="ml-2 fm-stamp" data-tone="paused">{it.disabled}</span>}
                </span>
                <Switch checked={selected.has(s.id)} disabled={save.isPending} onCheckedChange={(v) => void toggle(s.id, v)} aria-label={s.name} />
              </label>
            </li>
          ))}
        </ul>
      )}
    </Section>
  )
}

function EgressSection({ projectId, data }: { projectId: string; data: Integrations }) {
  const save = useSaveIntegrations(projectId)
  const [mode, setMode] = useState<Egress['mode']>(data.egress.mode)
  const [presets, setPresets] = useState<string[]>(data.egress.presets)
  const [hostsText, setHostsText] = useState(data.egress.hosts.join('\n'))
  const hosts = parseHosts(hostsText)
  const bad = invalidHosts(hosts)

  async function submit() {
    if (bad.length) return
    try {
      await save.mutateAsync({ egress: { mode, presets, hosts } })
      haptic.success()
      toast.success(`${it.saved}. ${eg.nextRun}`)
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  return (
    <Section title={eg.title}>
      {!data.egress_supported && mode === 'allowlist' && <Warning>{eg.nodeWarning}</Warning>}
      <div role="radiogroup" aria-label={eg.title} className="grid grid-cols-2 gap-2">
        {(['open', 'allowlist'] as const).map((m) => (
          <button
            key={m}
            type="button"
            role="radio"
            aria-checked={mode === m}
            onClick={() => setMode(m)}
            className={cn('fm-btn flex min-h-11 flex-col items-start justify-center px-3 py-2 text-left', mode === m && 'fm-pressed bg-muted font-bold')}
          >
            <span className="text-sm">{m === 'open' ? eg.open : eg.allowlist}</span>
          </button>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">{mode === 'open' ? eg.openHint : eg.allowlistHint}</p>
      {mode === 'allowlist' && (
        <>
          <fieldset className="space-y-1">
            <legend className="mb-1 text-sm font-medium">{eg.presets}</legend>
            <ul className="divide-y fm-group">
              {data.presets.map((p) => (
                <li key={p.id}>
                  <label className="flex min-h-11 items-center gap-3 px-3 py-2">
                    <input
                      type="checkbox"
                      className="size-5 shrink-0 accent-[var(--ink)]"
                      checked={presets.includes(p.id)}
                      onChange={(e) => setPresets(e.target.checked ? [...presets, p.id] : presets.filter((x) => x !== p.id))}
                    />
                    <span className="min-w-0">
                      <span className="block text-sm font-medium">{p.label}</span>
                      <span className="block truncate font-mono text-[13px] text-muted-foreground">{p.hosts.join(' ')}</span>
                    </span>
                  </label>
                </li>
              ))}
            </ul>
          </fieldset>
          <div className="space-y-2">
            <Label htmlFor="egress-hosts">{eg.hosts}</Label>
            <Textarea id="egress-hosts" rows={4} autoCapitalize="none" className="font-mono" value={hostsText} onChange={(e) => setHostsText(e.target.value)} />
            {bad.length > 0 ? (
              <p className="text-xs text-destructive">{bad.join(', ')}</p>
            ) : (
              <p className="text-xs text-muted-foreground">{eg.hostsHint}</p>
            )}
          </div>
        </>
      )}
      <Button className="h-11 w-full" disabled={bad.length > 0 || save.isPending} onClick={() => void submit()}>
        {eg.save}
      </Button>
      <p className="text-xs text-muted-foreground">{eg.nextRun}</p>
    </Section>
  )
}
