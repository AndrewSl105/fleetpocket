import { useState } from 'react'
import { Loader2, Pencil, PlugZap, Trash2 } from 'lucide-react'
import { toast } from 'sonner'
import { errorMessage, useDeleteMCPServer, useTestMCPServer } from '@/api/hooks'
import type { MCPServer, MCPTestResult } from '@/api/types'
import { Button } from '@/components/ui/button'
import { useConfirm } from '@/components/confirm'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { t } from '@/i18n/en'
import { ServerDialog } from './ServerDialog'

const it = t.integrations

export function toolsLabel(s: MCPServer): string {
  if (s.tools_mode === 'allow') return it.toolsAllow(s.tools.length)
  if (s.tools_mode === 'deny') return it.toolsDeny(s.tools.length)
  return it.toolsAll
}

/** Status stamp: off, error (last call or test failed), ok, or nothing yet. */
function StatusStamp({ s }: { s: MCPServer }) {
  if (!s.enabled) return <span className="fm-stamp" data-tone="paused">{it.disabled}</span>
  if (s.last_status === 'error') return <span className="fm-stamp" data-tone="danger">error</span>
  if (s.last_status === 'ok') return <span className="fm-stamp" data-tone="olive">ok</span>
  return null
}

/** MCP servers as a ruled list: status, tools, last use, and Test / Edit / Delete. */
export function ServerList({ servers, projectId, empty }: { servers: MCPServer[]; projectId: string | null; empty: string }) {
  const [editing, setEditing] = useState<MCPServer | undefined>()
  const [open, setOpen] = useState(false)
  const [results, setResults] = useState<Record<number, MCPTestResult>>({})
  const test = useTestMCPServer()
  const del = useDeleteMCPServer()
  const confirm = useConfirm()

  async function runTest(s: MCPServer) {
    try {
      const r = await test.mutateAsync(s.id)
      setResults((x) => ({ ...x, [s.id]: r }))
      if (r.ok) toast.success(it.testOk(r.tools.length))
      else toast.error(`${it.testFailed}: ${r.error ?? ''}`)
    } catch (e) {
      toast.error(errorMessage(e))
    }
  }

  async function remove(s: MCPServer) {
    if (await confirm(`${it.deleteConfirm(s.name)} ${it.deleteHint}`, t.common.remove)) {
      del.mutate(s.id, { onSuccess: () => toast.success(it.deleted) })
    }
  }

  return (
    <div className="space-y-3">
      {servers.length === 0 ? (
        <p className="fm-group px-3 py-3 text-sm text-muted-foreground">{empty}</p>
      ) : (
        <ul className="divide-y fm-group" aria-label={it.servers}>
          {servers.map((s) => {
            const r = results[s.id]
            return (
              <li key={s.id} className="px-3 py-2.5">
                <div className="flex min-h-11 items-center gap-2">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="font-mono text-sm font-semibold">{s.name}</span>
                      <span className="fm-label text-muted-foreground">{s.kind}</span>
                      <StatusStamp s={s} />
                      {s.read_only && <span className="fm-stamp">{it.readOnly}</span>}
                    </div>
                    <div className="mt-0.5 line-clamp-2 text-xs break-all text-muted-foreground">
                      {[s.url ?? s.package ?? 'DATABASE_URL', toolsLabel(s), s.max_calls ? it.maxCalls(s.max_calls) : null, s.last_used_at ? it.lastUsed(relativeTime(s.last_used_at)) : it.neverUsed]
                        .filter(Boolean)
                        .join(' · ')}
                    </div>
                    {s.last_status === 'error' && s.last_error && <div className="mt-0.5 truncate font-mono text-xs text-danger">{s.last_error}</div>}
                  </div>
                  <Button variant="outline" className="h-11 px-3" onClick={() => void runTest(s)} disabled={test.isPending && test.variables === s.id} aria-label={`${it.test} ${s.name}`}>
                    {test.isPending && test.variables === s.id ? <Loader2 className="animate-spin" /> : <PlugZap />}
                    <span className="hidden sm:inline">{it.test}</span>
                  </Button>
                  <Button variant="ghost" size="icon" className="size-11" aria-label={`${it.edit}: ${s.name}`} onClick={() => { setEditing(s); setOpen(true) }}>
                    <Pencil />
                  </Button>
                  <Button variant="ghost" size="icon" className="size-11" aria-label={`${t.common.remove} ${s.name}`} onClick={() => void remove(s)}>
                    <Trash2 />
                  </Button>
                </div>
                {r && (
                  <div className={cn('mt-1 text-xs', r.ok ? 'text-olive-text' : 'text-danger')}>
                    {r.ok ? it.testOk(r.tools.length) : `${it.testFailed}: ${r.error ?? ''}`}
                    {r.ok && r.tools.length > 0 && (
                      <ul className="mt-1 flex flex-wrap gap-1">
                        {r.tools.map((tool) => (
                          <li key={tool.name} className={cn('fm-stamp', !tool.allowed && 'line-through opacity-60')} title={tool.allowed ? tool.description : it.hidden}>
                            {tool.name}
                          </li>
                        ))}
                      </ul>
                    )}
                  </div>
                )}
              </li>
            )
          })}
        </ul>
      )}
      <AddServerButton projectId={projectId} />
      <ServerDialog open={open} onOpenChange={setOpen} server={editing} projectId={projectId} />
    </div>
  )
}

export function AddServerButton({ projectId }: { projectId: string | null }) {
  const [open, setOpen] = useState(false)
  return (
    <>
      <Button variant="outline" className="h-11" onClick={() => setOpen(true)}>
        {it.add}
      </Button>
      <ServerDialog open={open} onOpenChange={setOpen} projectId={projectId} />
    </>
  )
}
