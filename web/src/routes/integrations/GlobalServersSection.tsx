import { useGlobalMCPServers } from '@/api/hooks'
import { Skeleton } from '@/components/ui/skeleton'
import { t } from '@/i18n/en'
import { ServerList } from './ServerList'

const it = t.integrations

/** Settings: shared MCP servers that projects turn on in Integrations. */
export function GlobalServersSection() {
  const list = useGlobalMCPServers()
  return (
    <section aria-labelledby="mcp-global-title">
      <h2 id="mcp-global-title" className="mb-1 text-sm font-bold">
        {it.globalTitle}
      </h2>
      <p className="mb-3 text-xs text-muted-foreground">{it.globalSettingsHint}</p>
      {list.data ? <ServerList servers={list.data.items} projectId={null} empty={it.noGlobal.split('.')[0] + '.'} /> : <Skeleton className="h-24" />}
    </section>
  )
}
