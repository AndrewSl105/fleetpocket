import { describe, expect, it } from 'vitest'
import { badPath, buildSkillMd, parseSkillMd, yamlValue } from './skillDoc'

const SEED = `---
name: db-schema
description: Draw the database schema as a Mermaid ER diagram.
allowed-tools: Bash, Read
---
# Database schema diagram

Steps.
`

describe('parseSkillMd', () => {
  it('splits name, description, other frontmatter lines and the instructions', () => {
    expect(parseSkillMd(SEED)).toEqual({
      name: 'db-schema',
      description: 'Draw the database schema as a Mermaid ER diagram.',
      extra: ['allowed-tools: Bash, Read'],
      instructions: '# Database schema diagram\n\nSteps.\n',
    })
  })

  it('trims quotes like the server does', () => {
    expect(parseSkillMd('---\nname: "x"\ndescription: \'y: z\'\n---\nbody').description).toBe('y: z')
  })

  it('keeps a file without frontmatter as the instructions', () => {
    expect(parseSkillMd('# Just text')).toEqual({ name: '', description: '', extra: [], instructions: '# Just text' })
  })
})

describe('buildSkillMd', () => {
  it('round-trips a seeded skill', () => {
    expect(buildSkillMd(parseSkillMd(SEED))).toBe(SEED)
  })

  it('quotes values plain YAML would misread, on one line', () => {
    expect(yamlValue('Draw: the schema')).toBe('"Draw: the schema"')
    expect(yamlValue('- list')).toBe('"- list"')
    expect(yamlValue('two\nlines')).toBe('two lines')
    expect(yamlValue('plain words')).toBe('plain words')
  })
})

describe('badPath', () => {
  it('refuses what the server refuses, and SKILL.md', () => {
    for (const p of ['', '/etc/x', 'a/../b', 'a\\b', '.git/config', 'a//b', 'SKILL.md']) expect(badPath(p)).toBe(true)
    for (const p of ['ref/notes.md', 'templates/er.mmd', 'README.md']) expect(badPath(p)).toBe(false)
  })
})
