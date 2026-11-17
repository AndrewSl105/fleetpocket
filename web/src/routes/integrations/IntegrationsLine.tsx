import { Link } from 'react-router'
import { Plug } from 'lucide-react'
import { useIntegrations, useTaskEvents } from '@/api/hooks'
import { ti } from '@/i18n/integrations'

const it = ti

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

/**
 * Info tab rows: what the task's latest run got (its `integrations` event);
 * without one in the loaded events, the project's current settings.
 */
export function IntegrationsInfo({ taskId, projectId }: { taskId: number; projectId: string }) {
  const events = useTaskEvents(taskId).data?.items ?? []
  const project = useIntegrations(projectId).data
  let servers: string[] | undefined
  let egress: string | undefined
  for (let i = events.length - 1; i >= 0; i--) {
    const e = events[i]
    if (e.type === 'integrations') {
      servers = e.payload.servers
      egress = e.payload.egress
      break
    }
  }
  const fromRun = servers !== undefined
  if (!fromRun && project) {
    servers = project.active.map((s) => s.name)
    egress = project.egress.mode
  }
  if (servers === undefined) return null
  return (
    <>
      <div className="flex justify-between gap-4 py-2 text-sm">
        <dt className="text-muted-foreground">{fromRun ? it.servers : it.serversProject}</dt>
        <dd className="min-w-0 text-right font-mono break-words">{servers.join(', ') || it.none}</dd>
      </div>
      <div className="flex justify-between gap-4 py-2 text-sm">
        <dt className="text-muted-foreground">{it.egress.title}</dt>
        <dd className="min-w-0 text-right">{egress === 'allowlist' ? it.egress.allowlist : it.egress.open}</dd>
      </div>
    </>
  )
}
