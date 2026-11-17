import { z } from 'zod'
import type { Agent, CIAutoFixSettings, Project, ProjectFields } from '@/api/types'

const ENV_KEY = /^[A-Z_][A-Z0-9_]*$/
const numeric = z.string().trim().regex(/^(\d+(\.\d+)?)?$/, 'Number')

export const projectSchema = z.object({
  id: z
    .string()
    .trim()
    .regex(/^[a-z0-9][a-z0-9-]{0,39}$/, 'Lowercase letters, digits and dashes, max 40'),
  name: z.string().trim().min(1, 'Required'),
  instructions: z.string(),
  postgres: z.boolean(),
  redis: z.boolean(),
  env: z.array(z.object({ key: z.string().trim().regex(ENV_KEY, 'A–Z, 0–9, _'), value: z.string() })),
  agent: z.enum(['', 'claude', 'codex', 'cursor']),
  // '' runs on the server, else a node id.
  node: z.string(),
  model: z.string().trim(),
  max_turns: numeric,
  max_budget_usd: numeric,
  max_duration_sec: numeric,
  runner_memory: z.string().trim(),
  runner_cpus: numeric,
  // CI auto-fix overrides: '' inherits the global default.
  autofix: z.enum(['', 'on', 'off']),
  autofix_max_attempts: z
    .string()
    .trim()
    .regex(/^([1-5])?$/, '1 to 5'),
  autofix_checks: z.string(),
  autofix_budget_usd: numeric,
  autofix_allow_ci: z.enum(['', 'yes', 'no']),
})

export type ProjectFormValues = z.infer<typeof projectSchema>

export function toFormValues(p?: Project): ProjectFormValues {
  const s = (v: unknown) => (v === null || v === undefined ? '' : String(v))
  return {
    id: p?.id ?? '',
    name: p?.name ?? '',
    instructions: p?.instructions ?? '',
    postgres: p?.services.includes('postgres') ?? false,
    redis: p?.services.includes('redis') ?? false,
    env: Object.entries(p?.env ?? {}).map(([key, value]) => ({ key, value })),
    agent: p?.agent ?? '',
    node: p?.node ?? '',
    model: s(p?.model),
    max_turns: s(p?.max_turns),
    max_budget_usd: s(p?.max_budget_usd),
    max_duration_sec: s(p?.max_duration_sec),
    runner_memory: s(p?.runner_memory),
    runner_cpus: s(p?.runner_cpus),
    ...autoFixFormValues(p?.ci_autofix),
  }
}

type AutoFixForm = Pick<ProjectFormValues, 'autofix' | 'autofix_max_attempts' | 'autofix_checks' | 'autofix_budget_usd' | 'autofix_allow_ci'>

export function autoFixFormValues(a?: CIAutoFixSettings | null): AutoFixForm {
  const tri = <T extends string>(v: boolean | null | undefined, yes: T, no: T): '' | T => (v == null ? '' : v ? yes : no)
  return {
    autofix: tri(a?.enabled, 'on', 'off'),
    autofix_max_attempts: a?.max_attempts == null ? '' : String(a.max_attempts),
    autofix_checks: (a?.checks ?? []).join(', '),
    autofix_budget_usd: a?.budget_usd ? String(a.budget_usd) : '',
    autofix_allow_ci: tri(a?.allow_ci_changes, 'yes', 'no'),
  }
}

/** The CI auto-fix object; empty fields become null (inherit). */
export function autoFixSettings(v: AutoFixForm): CIAutoFixSettings {
  const checks = v.autofix_checks
    .split(',')
    .map((c) => c.trim())
    .filter(Boolean)
  return {
    enabled: v.autofix === '' ? null : v.autofix === 'on',
    max_attempts: v.autofix_max_attempts.trim() ? Number(v.autofix_max_attempts) : null,
    checks: checks.length ? checks : null,
    budget_usd: v.autofix_budget_usd.trim() ? Number(v.autofix_budget_usd) : null,
    allow_ci_changes: v.autofix_allow_ci === '' ? null : v.autofix_allow_ci === 'yes',
  }
}

type Full = Required<Pick<ProjectFields, 'name' | 'instructions' | 'services' | 'env'>> & {
  agent: '' | Agent
  node: string
  model: string
  max_turns: number
  max_budget_usd: number
  max_duration_sec: number
  runner_memory: string
  runner_cpus: number
  ci_autofix: CIAutoFixSettings
}

/** "" or 0 means "server default" for overrides (docs/API.md). */
function normalize(v: ProjectFormValues): Full {
  return {
    name: v.name.trim(),
    instructions: v.instructions,
    services: [...(v.postgres ? ['postgres' as const] : []), ...(v.redis ? ['redis' as const] : [])],
    env: Object.fromEntries(v.env.filter((e) => e.key.trim()).map((e) => [e.key.trim(), e.value])),
    agent: v.agent,
    node: v.node,
    model: v.model.trim(),
    max_turns: Number(v.max_turns || 0),
    max_budget_usd: Number(v.max_budget_usd || 0),
    max_duration_sec: Number(v.max_duration_sec || 0),
    runner_memory: v.runner_memory.trim(),
    runner_cpus: Number(v.runner_cpus || 0),
    ci_autofix: autoFixSettings(v),
  }
}

const OVERRIDES = ['agent', 'node', 'model', 'max_turns', 'max_budget_usd', 'max_duration_sec', 'runner_memory', 'runner_cpus'] as const

/** POST body: only fields with a value (no explicit defaults). */
export function createBody(v: ProjectFormValues): ProjectFields & { id: string } {
  const n = normalize(v)
  const body: ProjectFields & { id: string } = { id: v.id.trim(), name: n.name, services: n.services, env: n.env }
  if (n.instructions) body.instructions = n.instructions
  for (const k of OVERRIDES) if (n[k]) Object.assign(body, { [k]: n[k] })
  if (Object.values(n.ci_autofix).some((x) => x !== null)) body.ci_autofix = n.ci_autofix
  return body
}

/** PATCH body: only changed fields; a cleared override is sent as "" or 0 to reset it. */
export function patchBody(v: ProjectFormValues, original: Project): ProjectFields {
  const next = normalize(v)
  const prev = normalize(toFormValues(original))
  const body: ProjectFields = {}
  for (const k of Object.keys(next) as Array<keyof Full>) {
    if (JSON.stringify(next[k]) !== JSON.stringify(prev[k])) Object.assign(body, { [k]: next[k] })
  }
  return body
}
