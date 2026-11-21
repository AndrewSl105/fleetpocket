import { useState, type ReactNode } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { toast } from 'sonner'
import { CircleCheck, CircleX, FolderGit2, Loader2 } from 'lucide-react'
import { errorMessage, useCreateTask, useHealth, useMe } from '@/api/hooks'
import { useAddLocalRepo, useLocalSuggestions } from '@/api/local'
import type { LocalRepoAdded, LocalRepoSuggestion, StartFrom } from '@/api/types'
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Logo } from '@/components/Logo'
import { Window } from '@/components/Window'
import { relativeTime } from '@/lib/format'
import { cn } from '@/lib/utils'
import { haptic } from '@/telegram/haptics'
import { t } from '@/i18n/en'
import { lc } from '@/i18n/local'
import { FolderBrowser } from './FolderBrowser'
import { GitHubConnect } from './GitHubConnect'
import { StartFromField } from './StartFromField'
import { dismissOnboarding } from './onboarding'

const o = lc.onboarding
const f = lc.folder

export type OnboardingStep = 'docker' | 'agent' | 'folder' | 'github' | 'task'
export const STEPS: OnboardingStep[] = ['docker', 'agent', 'folder', 'github', 'task']

/** `?step=folder` ("Add a folder" on Projects) starts at the folder; else at Docker. */
export function initialOnboardingStep(search: URLSearchParams): OnboardingStep {
  return search.get('step') === 'folder' ? 'folder' : 'docker'
}

function StepIndicator({ step }: { step: OnboardingStep }) {
  const current = STEPS.indexOf(step)
  return (
    <ol className="flex gap-1 text-xs" aria-label={o.progress}>
      {STEPS.map((s, i) => (
        <li
          key={s}
          aria-current={i === current ? 'step' : undefined}
          className={cn(
            'min-w-0 rounded-sm border px-1 py-1 text-center',
            i === current ? 'flex-[2] border-primary bg-primary text-primary-foreground' : 'flex-1',
            i < current ? 'opacity-70' : i > current && 'opacity-50',
          )}
        >
          {i + 1}
          <span className={cn('truncate', i === current ? 'inline' : 'hidden sm:inline')}>. {o.steps[s]}</span>
        </li>
      ))}
    </ol>
  )
}

function Check({ ok, pending, children }: { ok: boolean; pending?: boolean; children: ReactNode }) {
  const Icon = pending ? Loader2 : ok ? CircleCheck : CircleX
  return (
    <p className={cn('flex items-start gap-2 text-sm', !pending && (ok ? 'text-olive-text' : 'text-danger'))} role="status">
      <Icon className={cn('mt-0.5 size-4 shrink-0', pending && 'animate-spin')} aria-hidden="true" />
      <span className="min-w-0 flex-1">{children}</span>
    </p>
  )
}

function Footer({ children }: { children: ReactNode }) {
  return <div className="flex flex-wrap justify-end gap-2 pt-1">{children}</div>
}

export function DockerStep({ onNext }: { onNext: () => void }) {
  const health = useHealth()
  const docker = health.data?.checks.docker
  const ok = docker === 'ok'
  return (
    <div className="space-y-3">
      {health.isPending ? <Check ok={false} pending>{o.dockerChecking}</Check> : <Check ok={ok}>{ok ? o.dockerOk : o.dockerDown(docker ?? 'unknown')}</Check>}
      <Footer>
        {!ok && !health.isPending && (
          <Button type="button" variant="outline" className="h-11" onClick={() => void health.refetch()} disabled={health.isFetching}>
            {o.retry}
          </Button>
        )}
        <Button type="button" className="h-11 min-w-28" disabled={!ok} onClick={onNext}>
          {o.next}
        </Button>
      </Footer>
    </div>
  )
}

export function AgentStep({ onNext }: { onNext: () => void }) {
  const me = useMe()
  const agents = me.data?.agents ?? []
  return (
    <div className="space-y-3">
      {me.isPending ? (
        <Check ok={false} pending>
          …
        </Check>
      ) : agents.length > 0 ? (
        <Check ok>{o.agentOk(agents.map((a) => t.agents[a]).join(', '))}</Check>
      ) : (
        <>
          <Check ok={false}>{o.agentNone}</Check>
          <p className="text-sm text-muted-foreground">{o.agentHow}</p>
        </>
      )}
      <Footer>
        {agents.length === 0 && !me.isPending && (
          <Button type="button" variant="outline" className="h-11" onClick={() => void me.refetch()} disabled={me.isFetching}>
            {o.retry}
          </Button>
        )}
        <Button type="button" className="h-11 min-w-28" disabled={agents.length === 0} onClick={onNext}>
          {o.next}
        </Button>
      </Footer>
    </div>
  )
}

function SuggestionRow({ s, start, busy, onPick }: { s: LocalRepoSuggestion; start: boolean; busy: boolean; onPick: () => void }) {
  return (
    <li>
      <button type="button" disabled={busy} onClick={onPick} className="flex min-h-14 w-full items-center gap-3 px-3 py-2 text-left hover:bg-grid disabled:opacity-60">
        <FolderGit2 className="size-5 shrink-0 text-olive-text" aria-hidden="true" />
        <span className="min-w-0 flex-1">
          <span className="flex items-center gap-2">
            <span className="truncate font-semibold">{s.name}</span>
            {start && <span className="fm-stamp" data-tone="signal">{f.thisFolder}</span>}
            {s.added && <span className="fm-stamp" data-tone="paused">{f.added}</span>}
          </span>
          <span className="block truncate font-mono text-xs text-muted-foreground">{s.path}</span>
          <span className="mt-0.5 flex flex-wrap items-center gap-x-2 font-mono text-[13px] text-muted-foreground">
            <span>⎇ {s.branch || f.detached}</span>
            <span className={s.dirty ? 'text-signal-text' : undefined}>{s.dirty ? f.dirty : f.clean}</span>
            <span>{f.lastCommit(relativeTime(s.last_commit))}</span>
          </span>
        </span>
        <span className="fm-btn inline-flex h-9 shrink-0 items-center px-3 text-sm font-semibold">{f.pick}</span>
      </button>
    </li>
  )
}

/** Step 3: the repositories found on this computer, "This folder", and the folder browser. */
export function FolderStep({ onAdded }: { onAdded: (res: LocalRepoAdded, dirty: boolean) => void }) {
  const suggestions = useLocalSuggestions()
  const add = useAddLocalRepo()
  const [browsing, setBrowsing] = useState(false)
  const items = suggestions.data?.items ?? []
  const start = suggestions.data?.start ?? null
  // "This folder" first, then newest commit first (as returned).
  const sorted = [...items.filter((s) => s.path === start), ...items.filter((s) => s.path !== start)]

  function pick(path: string, dirty: boolean) {
    add.mutate(
      { path },
      {
        onSuccess: (res) => {
          haptic.success()
          toast.success(res.created ? o.added(res.repository.full_name) : o.alreadyAdded(res.repository.full_name))
          onAdded(res, dirty)
        },
        onError: (e) => {
          haptic.error()
          toast.error(errorMessage(e))
        },
      },
    )
  }

  return (
    <div className="space-y-3">
      <p className="text-sm">{o.folderIntro}</p>
      <section aria-label={f.suggestions}>
        <h2 className="fm-label mb-1.5 text-muted-foreground">{f.suggestions}</h2>
        {suggestions.isPending ? (
          <p className="flex items-center gap-2 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" aria-hidden="true" /> {t.common.loading}
          </p>
        ) : suggestions.isError ? (
          <p role="alert" className="text-sm text-danger">
            {errorMessage(suggestions.error)}
          </p>
        ) : sorted.length === 0 ? (
          <p className="text-sm text-muted-foreground">{f.noSuggestions}</p>
        ) : (
          <ul className="max-h-80 divide-y divide-rule overflow-y-auto fm-field">
            {sorted.map((s) => (
              <SuggestionRow key={s.path} s={s} start={s.path === start} busy={add.isPending} onPick={() => pick(s.path, s.dirty)} />
            ))}
          </ul>
        )}
      </section>
      {browsing ? (
        <FolderBrowser busy={add.isPending} onPick={(path) => pick(path, items.find((s) => s.path === path)?.dirty ?? false)} />
      ) : (
        <Button type="button" variant="outline" className="h-11 w-full" onClick={() => setBrowsing(true)}>
          {f.browse}
        </Button>
      )}
    </div>
  )
}

/** Step 5: the first task in the new project, with Start from. */
export function FirstTaskStep({ added, dirty }: { added: LocalRepoAdded; dirty: boolean }) {
  const create = useCreateTask()
  const navigate = useNavigate()
  const [prompt, setPrompt] = useState('')
  const [startFrom, setStartFrom] = useState<StartFrom>('last_commit')

  function submit() {
    if (!prompt.trim()) return
    create.mutate(
      { project_id: added.project.id, repository_id: added.repository.id, prompt: prompt.trim(), start_from: startFrom },
      {
        onSuccess: (task) => {
          haptic.success()
          dismissOnboarding()
          toast.success(t.newTask.created)
          void navigate(`/tasks/${task.id}`, { replace: true })
        },
        onError: (e) => {
          haptic.error()
          toast.error(errorMessage(e))
        },
      },
    )
  }

  return (
    <form
      className="space-y-4"
      onSubmit={(e) => {
        e.preventDefault()
        submit()
      }}
    >
      <div className="space-y-2">
        <Label htmlFor="first-prompt">{o.taskIntro(added.repository.full_name)}</Label>
        <Textarea
          id="first-prompt"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          placeholder={o.taskPlaceholder}
          className="min-h-32 field-sizing-content"
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
              e.preventDefault()
              submit()
            }
          }}
        />
      </div>
      <StartFromField value={startFrom} onChange={setStartFrom} dirty={dirty} />
      <Footer>
        <Button type="submit" className="h-11 min-w-28" disabled={!prompt.trim() || create.isPending}>
          {create.isPending && <Loader2 className="animate-spin" aria-hidden="true" />}
          {o.start}
        </Button>
      </Footer>
    </form>
  )
}

/** Local mode first run (`npx jungle-monkey`): Docker, agent, a folder, GitHub (optional), the first task. */
export default function OnboardingPage() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  const [step, setStep] = useState<OnboardingStep>(() => initialOnboardingStep(params))
  const [added, setAdded] = useState<{ res: LocalRepoAdded; dirty: boolean } | null>(null)
  const next = () => setStep(STEPS[Math.min(STEPS.indexOf(step) + 1, STEPS.length - 1)])
  const back = STEPS.indexOf(step) > 0 && !(step === 'task' && !added) ? () => setStep(STEPS[STEPS.indexOf(step) - 1]) : null

  let body: ReactNode
  switch (step) {
    case 'docker':
      body = <DockerStep onNext={next} />
      break
    case 'agent':
      body = <AgentStep onNext={next} />
      break
    case 'folder':
      body = (
        <FolderStep
          onAdded={(res, dirty) => {
            setAdded({ res, dirty })
            setStep('github')
          }}
        />
      )
      break
    case 'github':
      body = (
        <div className="space-y-3">
          <p className="text-sm">{o.githubIntro}</p>
          <GitHubConnect />
          <Footer>
            <Button type="button" variant="outline" className="h-11" onClick={() => setStep(added ? 'task' : 'folder')}>
              {o.skip}
            </Button>
            <Button type="button" className="h-11 min-w-28" onClick={() => setStep(added ? 'task' : 'folder')}>
              {o.next}
            </Button>
          </Footer>
        </div>
      )
      break
    case 'task':
      body = added ? <FirstTaskStep added={added.res} dirty={added.dirty} /> : <FolderStep onAdded={(res, dirty) => setAdded({ res, dirty })} />
      break
  }

  return (
    <main className="fm-ground min-h-(--app-height) overflow-y-auto px-3 pt-[calc(var(--safe-top)+1.5rem)] pb-10">
      <Window title={o.title} className="mx-auto w-full max-w-lg" bodyClassName="space-y-4 p-3">
        <div className="flex items-center gap-3">
          <Logo className="size-10 shrink-0" />
          <h1 className="text-sm font-semibold">
            {t.app.name} · {o.steps[step]}
          </h1>
        </div>
        <StepIndicator step={step} />
        {body}
        <div className="flex items-center justify-between border-t border-rule pt-2 text-sm">
          {back ? (
            <Button type="button" variant="ghost" className="h-11" onClick={back}>
              {o.back}
            </Button>
          ) : (
            <span />
          )}
          <Link
            to="/"
            className="flex min-h-11 items-center px-2 text-muted-foreground underline underline-offset-2"
            onClick={(e) => {
              e.preventDefault()
              dismissOnboarding()
              void navigate('/', { replace: true })
            }}
          >
            {o.later}
          </Link>
        </div>
      </Window>
    </main>
  )
}
