import { Link } from 'react-router'
import { Plug } from 'lucide-react'
import { useIntegrations } from '@/api/hooks'
import { t } from '@/i18n/en'

const it = t.integrations

/** What a task of the project gets: its MCP servers and the allowlist stamp. Nothing when it gets neither. */
export function IntegrationsLine({ projectId }: { projectId: string }) {
  const data = useIntegrations(projectId).data
  if (!data || (data.active.length === 0 && data.egress.mode === 'open')) return null
  const names = data.active.map((s) => s.name).join(', ')
  return (
    <Link
      to={`/projects/${projectId}/integrations`}
      className="flex min-h-11 items-center gap-2 text-xs text-muted-foreground hover:text-foreground"
      data-testid="integrations-line"
    >
      <Plug className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="truncate">{it.composer(names || it.none)}</span>
      {data.egress.mode === 'allowlist' && (
        <span className="fm-stamp" data-tone="signal">
          {it.allowlistStamp}
        </span>
      )}
    </Link>
  )
}

/** Info tab rows: the project's MCP servers and network mode. */
export function IntegrationsInfo({ projectId }: { projectId: string }) {
  const data = useIntegrations(projectId).data
  if (!data) return null
  return (
    <>
      <div className="flex justify-between gap-4 py-2 text-sm">
        <dt className="text-muted-foreground">{it.servers}</dt>
        <dd className="min-w-0 text-right font-mono break-words">{data.active.map((s) => s.name).join(', ') || it.none}</dd>
      </div>
      <div className="flex justify-between gap-4 py-2 text-sm">
        <dt className="text-muted-foreground">{it.egress.title}</dt>
        <dd className="min-w-0 text-right">{data.egress.mode === 'allowlist' ? it.egress.allowlist : it.egress.open}</dd>
      </div>
    </>
  )
}
