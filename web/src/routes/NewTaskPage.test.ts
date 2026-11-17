import { describe, expect, it } from 'vitest'
import { toCreateBody } from './NewTaskPage'

describe('toCreateBody', () => {
  const base = { project_id: 'p', repository_id: '', title: '', prompt: ' Do it ', agent: '' as const, node: '', skills_add: [] as string[], skills_remove: [] as string[] }

  it('omits blank optional fields, including the agent and host (the project decides)', () => {
    expect(toCreateBody(base)).toEqual({ project_id: 'p', prompt: 'Do it' })
  })

  it('sends a picked agent', () => {
    expect(toCreateBody({ ...base, agent: 'codex' })).toEqual({ project_id: 'p', prompt: 'Do it', agent: 'codex' })
  })

  it('sends a picked host', () => {
    expect(toCreateBody({ ...base, node: 'macbook' })).toEqual({ project_id: 'p', prompt: 'Do it', node: 'macbook' })
    expect(toCreateBody({ ...base, node: 'server' })).toEqual({ project_id: 'p', prompt: 'Do it', node: 'server' })
  })

  it('sends per-task skill changes only when there are some', () => {
    expect(toCreateBody({ ...base, skills_add: ['infra-map'], skills_remove: ['db-schema'] })).toEqual({
      project_id: 'p',
      prompt: 'Do it',
      skills_add: ['infra-map'],
      skills_remove: ['db-schema'],
    })
  })
})
