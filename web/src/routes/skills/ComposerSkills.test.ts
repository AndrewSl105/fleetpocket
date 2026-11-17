import { describe, expect, it } from 'vitest'
import { baseSkills, toggleSkill } from './ComposerSkills'

describe('composer skills', () => {
  const base = baseSkills({ skills: ['db-schema'] }, [
    { slug: 'db-schema', enabled_by_default: false },
    { slug: 'pr-checklist', enabled_by_default: true },
    { slug: 'infra-map', enabled_by_default: false },
  ])

  it('starts from the project skills plus the default ones', () => {
    expect([...base].sort()).toEqual(['db-schema', 'pr-checklist'])
  })

  it('drops a base skill with skills_remove and adds another with skills_add; toggling back undoes it', () => {
    expect(toggleSkill('pr-checklist', base, [], [])).toEqual({ add: [], remove: ['pr-checklist'] })
    expect(toggleSkill('pr-checklist', base, [], ['pr-checklist'])).toEqual({ add: [], remove: [] })
    expect(toggleSkill('infra-map', base, [], [])).toEqual({ add: ['infra-map'], remove: [] })
    expect(toggleSkill('infra-map', base, ['infra-map'], [])).toEqual({ add: [], remove: [] })
  })
})
