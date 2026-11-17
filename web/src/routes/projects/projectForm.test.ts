import { describe, expect, it } from 'vitest'
import type { Project } from '@/api/types'
import { createBody, patchBody, toFormValues } from './projectForm'

const project: Project = {
  id: 'vatary',
  name: 'Vatary',
  instructions: 'x',
  services: ['postgres'],
  env: { NODE_ENV: 'test' },
  runner_image: null,
  agent: null,
  model: 'claude-opus-5-5',
  max_turns: 80,
  max_budget_usd: null,
  max_duration_sec: null,
  runner_memory: null,
  runner_cpus: null,
  telegram_thread_id: null,
  archived: false,
  ci_autofix: { enabled: null, max_attempts: null, checks: null, budget_usd: null, allow_ci_changes: null },
  budget: null,
  month_cost_usd: 0,
  month_cost_unknown: false,
  created_at: '',
  updated_at: '',
  repositories: [],
}

describe('patchBody', () => {
  it('sends nothing when nothing changed', () => {
    expect(patchBody(toFormValues(project), project)).toEqual({})
  })

  it('sends only changed fields and resets cleared overrides with "" / 0', () => {
    const v = { ...toFormValues(project), name: 'Vatary 2', model: '', max_turns: '', redis: true, max_budget_usd: '2.5' }
    expect(patchBody(v, project)).toEqual({ name: 'Vatary 2', model: '', max_turns: 0, services: ['postgres', 'redis'], max_budget_usd: 2.5 })
  })

  it('sends the whole env map when a variable changes', () => {
    const v = { ...toFormValues(project), env: [{ key: 'NODE_ENV', value: 'test' }, { key: 'LOG', value: '1' }] }
    expect(patchBody(v, project)).toEqual({ env: { NODE_ENV: 'test', LOG: '1' } })
  })
})

describe('agent', () => {
  it('sends a picked agent and resets it to the server default with ""', () => {
    expect(patchBody({ ...toFormValues(project), agent: 'codex' }, project)).toEqual({ agent: 'codex' })
    const codex = { ...project, agent: 'codex' as const }
    expect(patchBody({ ...toFormValues(codex), agent: '' }, codex)).toEqual({ agent: '' })
    expect(createBody({ ...toFormValues(), id: 'n', name: 'N', agent: 'cursor' })).toMatchObject({ agent: 'cursor' })
  })
})

describe('node', () => {
  it('sends a picked computer and moves back to the server with ""', () => {
    expect(patchBody({ ...toFormValues(project), node: 'macbook' }, project)).toEqual({ node: 'macbook' })
    const mac = { ...project, node: 'macbook' }
    expect(patchBody({ ...toFormValues(mac), node: '' }, mac)).toEqual({ node: '' })
    expect(createBody({ ...toFormValues(), id: 'n', name: 'N', node: 'macbook' })).toMatchObject({ node: 'macbook' })
  })
})

describe('createBody', () => {
  it('omits empty overrides', () => {
    const v = { ...toFormValues(), id: 'new', name: 'New' }
    expect(createBody(v)).toEqual({ id: 'new', name: 'New', services: [], env: {} })
  })
})

describe('ci_autofix', () => {
  it('sends the whole object when a setting changes; empty fields inherit (null)', () => {
    const v = { ...toFormValues(project), autofix: 'on' as const, autofix_checks: 'test, lint ,', autofix_max_attempts: '3' }
    expect(patchBody(v, project)).toEqual({
      ci_autofix: { enabled: true, max_attempts: 3, checks: ['test', 'lint'], budget_usd: null, allow_ci_changes: null },
    })
  })

  it('round-trips a project override and leaves it out of a create without one', () => {
    const p = { ...project, ci_autofix: { enabled: false, max_attempts: 2, checks: ['e2e'], budget_usd: 1.5, allow_ci_changes: true } }
    expect(patchBody(toFormValues(p), p)).toEqual({})
    expect(toFormValues(p)).toMatchObject({ autofix: 'off', autofix_allow_ci: 'yes', autofix_checks: 'e2e', autofix_budget_usd: '1.5' })
    expect(createBody({ ...toFormValues(), id: 'n', name: 'N' })).not.toHaveProperty('ci_autofix')
  })
})

describe('monthly budget', () => {
  it('sets, keeps and removes the cap', () => {
    expect(patchBody({ ...toFormValues(project), monthly_budget_usd: '50' }, project)).toEqual({ budget: { monthly_usd: 50 } })
    const capped = { ...project, budget: { monthly_usd: 50 } }
    expect(patchBody(toFormValues(capped), capped)).toEqual({})
    expect(patchBody({ ...toFormValues(capped), monthly_budget_usd: '' }, capped)).toEqual({ budget: { monthly_usd: null } })
    expect(createBody({ ...toFormValues(), id: 'n', name: 'N', monthly_budget_usd: '20' })).toMatchObject({ budget: { monthly_usd: 20 } })
  })
})
