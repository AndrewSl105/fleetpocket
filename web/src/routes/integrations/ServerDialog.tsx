import { useState } from 'react'
import { Plus, X } from 'lucide-react'
import { toast } from 'sonner'
import { errorMessage, useSaveMCPServer, useTestMCPServer } from '@/api/hooks'
import type { MCPServer, MCPTestResult } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { haptic } from '@/telegram/haptics'
import { t } from '@/i18n/en'
import { ti } from '@/i18n/integrations'
import { emptyForm, secretsForKind, toForm, toInput, validate, type Kind, type ServerForm } from './form'

const it = ti
const f = it.fields

function Choice<V extends string>({ id, value, onChange, options }: { id: string; value: V; onChange: (v: V) => void; options: Record<V, string> }) {
  return (
    <Select value={value} onValueChange={(v) => onChange(v as V)}>
      <SelectTrigger id={id} className="h-11 w-full">
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        {(Object.keys(options) as V[]).map((k) => (
          <SelectItem key={k} value={k} className="h-10">
            {options[k]}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  )
}

type Props = {
  open: boolean
  onOpenChange: (v: boolean) => void
  /** The server to edit; undefined adds one. */
  server?: MCPServer
  /** The owning project; null adds a shared (global) server. */
  projectId: string | null
}

/** Add or edit an MCP server. Secrets are write-only: stored ones show their last characters. */
export function ServerDialog({ open, onOpenChange, server, projectId }: Props) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90dvh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{server ? it.edit : it.add}</DialogTitle>
          <DialogDescription>{it.hint}</DialogDescription>
        </DialogHeader>
        {open && <ServerFormBody key={server?.id ?? 'new'} server={server} projectId={projectId} onDone={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  )
}

function ServerFormBody({ server, projectId, onDone }: { server?: MCPServer; projectId: string | null; onDone: () => void }) {
  const save = useSaveMCPServer()
  const test = useTestMCPServer()
  const [result, setResult] = useState<MCPTestResult | null>(null)
  const [form, setForm] = useState<ServerForm>(() => (server ? toForm(server) : { ...emptyForm() }))
  const [initial] = useState(() => (server ? toForm(server).secrets : []))
  const errors = validate(form)
  const set = (patch: Partial<ServerForm>) => setForm((x) => ({ ...x, ...patch }))

  function setKind(kind: Kind) {
    set({ kind, readOnly: kind === 'postgres', ...(server ? {} : { secrets: secretsForKind(kind) }) })
  }

  async function submit() {
    if (Object.keys(errors).length) return
    try {
      const saved = await save.mutateAsync({ id: server?.id, body: toInput(form, initial, server ? undefined : projectId) })
      haptic.success()
      toast.success(it.saved)
      onDone()
      // A new server is tested right away, as the gateway would connect.
      if (!server) {
        test.mutate(saved.id, {
          onSuccess: (r) => (r.ok ? toast.success(`${saved.name}: ${it.testOk(r.tools.length)}`) : toast.error(`${saved.name}: ${it.testFailed}: ${r.error ?? ''}`)),
        })
      }
    } catch (e) {
      haptic.error()
      toast.error(errorMessage(e))
    }
  }

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        void submit()
      }}
    >
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-2">
          <Label htmlFor="mcp-name">{f.name}</Label>
          <Input id="mcp-name" autoCapitalize="none" className="h-11 font-mono" value={form.name} onChange={(e) => set({ name: e.target.value })} />
        </div>
        <div className="space-y-2">
          <Label htmlFor="mcp-kind">{f.kind}</Label>
          <Choice id="mcp-kind" value={form.kind} onChange={setKind} options={f.kinds} />
        </div>
      </div>
      <p className="-mt-2 text-xs text-muted-foreground">{errors.name && form.name ? <span className="text-destructive">{errors.name}</span> : f.nameHint}</p>

      {form.kind === 'remote' && (
        <>
          <div className="space-y-2">
            <Label htmlFor="mcp-url">{f.url}</Label>
            <Input id="mcp-url" type="url" inputMode="url" autoCapitalize="none" className="h-11 font-mono" placeholder="https://mcp.example.com/mcp" value={form.url} onChange={(e) => set({ url: e.target.value })} />
            {errors.url && form.url && <p className="text-xs text-destructive">{errors.url}</p>}
          </div>
          <div className="space-y-2">
            <Label htmlFor="mcp-transport">{f.transport}</Label>
            <Choice id="mcp-transport" value={form.transport} onChange={(v) => set({ transport: v })} options={f.transports} />
          </div>
        </>
      )}

      {form.kind === 'stdio' && (
        <>
          <div className="grid grid-cols-2 gap-3">
            <div className="space-y-2">
              <Label htmlFor="mcp-runtime">{f.runtime}</Label>
              <Choice id="mcp-runtime" value={form.runtime} onChange={(v) => set({ runtime: v })} options={f.runtimes} />
            </div>
            <div className="space-y-2">
              <Label htmlFor="mcp-pkg">{form.runtime === 'image' ? f.packageImage : f.package}</Label>
              <Input id="mcp-pkg" autoCapitalize="none" className="h-11 font-mono" value={form.pkg} onChange={(e) => set({ pkg: e.target.value })} />
            </div>
          </div>
          {form.runtime === 'pip' && (
            <div className="space-y-2">
              <Label htmlFor="mcp-cmd">{f.command}</Label>
              <Input id="mcp-cmd" autoCapitalize="none" className="h-11 font-mono" value={form.command} onChange={(e) => set({ command: e.target.value })} />
              <p className="text-xs text-muted-foreground">{f.commandHint}</p>
            </div>
          )}
          <div className="space-y-2">
            <Label htmlFor="mcp-args">{f.args}</Label>
            <Textarea id="mcp-args" rows={2} className="font-mono" value={form.args} onChange={(e) => set({ args: e.target.value })} />
            <p className="text-xs text-muted-foreground">{f.argsHint}</p>
          </div>
        </>
      )}

      <fieldset className="space-y-2">
        <legend className="text-sm font-medium">{f.secrets}</legend>
        <p className="text-xs text-muted-foreground">{f.secretsHint[form.kind]}</p>
        {form.secrets.map((s, i) => (
          <div key={i} className="flex items-start gap-2">
            <Input
              aria-label={f.secretKey}
              placeholder={f.secretKey}
              autoCapitalize="none"
              className="h-11 w-2/5 min-w-0 font-mono"
              value={s.key}
              readOnly={form.kind === 'postgres'}
              onChange={(e) => set({ secrets: form.secrets.map((x, j) => (j === i ? { ...x, key: e.target.value } : x)) })}
            />
            <Input
              aria-label={f.secretValue}
              type="password"
              autoComplete="off"
              placeholder={s.hint ? f.secretStored(s.hint) : f.secretValue}
              className="h-11 min-w-0 flex-1 font-mono"
              value={s.value}
              onChange={(e) => set({ secrets: form.secrets.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)) })}
            />
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="size-11"
              aria-label={t.common.remove}
              onClick={() => set({ secrets: form.secrets.filter((_, j) => j !== i) })}
            >
              <X />
            </Button>
          </div>
        ))}
        {errors.secrets && <p className="text-xs text-destructive">{errors.secrets}</p>}
        {form.kind !== 'postgres' && (
          <Button type="button" variant="outline" className="h-10" onClick={() => set({ secrets: [...form.secrets, { key: '', value: '' }] })}>
            <Plus /> {f.addSecret}
          </Button>
        )}
      </fieldset>

      <div className="space-y-2">
        <Label htmlFor="mcp-tools-mode">{f.tools}</Label>
        <Choice id="mcp-tools-mode" value={form.toolsMode} onChange={(v) => set({ toolsMode: v })} options={f.toolsModes} />
        {form.toolsMode !== 'all' && (
          <>
            <Input aria-label={f.toolsList} autoCapitalize="none" className="h-11 font-mono" placeholder="delete_*, create_issue" value={form.tools} onChange={(e) => set({ tools: e.target.value })} />
            <p className="text-xs text-muted-foreground">{f.toolsListHint}</p>
          </>
        )}
      </div>

      <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3 py-2">
        <span>
          <span className="block text-sm font-medium">{f.readOnly}</span>
          <span className="block text-xs text-muted-foreground">{form.kind === 'postgres' ? f.readOnlyHint.postgres : f.readOnlyHint.other}</span>
        </span>
        <Switch checked={form.readOnly} onCheckedChange={(v) => set({ readOnly: v })} aria-label={f.readOnly} />
      </label>

      <div className="grid grid-cols-2 items-end gap-3">
        <div className="space-y-2">
          <Label htmlFor="mcp-max">{f.maxCalls}</Label>
          <Input id="mcp-max" inputMode="numeric" placeholder={f.noLimit} className="h-11" value={form.maxCalls} onChange={(e) => set({ maxCalls: e.target.value })} />
        </div>
        <label className="flex min-h-11 items-center justify-between gap-3 fm-group px-3">
          <span className="text-sm font-medium">{f.enabled}</span>
          <Switch checked={form.enabled} onCheckedChange={(v) => set({ enabled: v })} aria-label={f.enabled} />
        </label>
      </div>
      {errors.maxCalls && <p className="text-xs text-destructive">{errors.maxCalls}</p>}

      {server && (
        <div className="space-y-1">
          <Button
            type="button"
            variant="outline"
            className="h-11 w-full"
            disabled={test.isPending}
            onClick={() => test.mutate(server.id, { onSuccess: setResult, onError: (e) => toast.error(errorMessage(e)) })}
          >
            {test.isPending ? it.testing : it.test}
          </Button>
          <p className="text-xs text-muted-foreground">{it.testSaved}</p>
          {result && (
            <p role="status" className={result.ok ? 'text-xs text-olive-text' : 'text-xs text-danger'}>
              {result.ok ? `${it.testOk(result.tools.length)}: ${result.tools.filter((x) => x.allowed).map((x) => x.name).join(', ')}` : `${it.testFailed}: ${result.error ?? ''}`}
            </p>
          )}
        </div>
      )}
      <Button type="submit" className="h-11 w-full" disabled={Object.keys(errors).length > 0 || save.isPending}>
        {t.common.save}
      </Button>
    </form>
  )
}
