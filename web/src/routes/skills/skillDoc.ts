// SKILL.md split into the fields the editor shows. The server takes `name`
// and `description` from the frontmatter with a line parser (skills.Frontmatter:
// `key: value`, surrounding quotes trimmed), so the editor writes them back as
// single lines and keeps any other frontmatter lines as they were.

const FM = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?([\s\S]*)$/

export type SkillDoc = {
  name: string
  description: string
  /** Other frontmatter lines (e.g. allowed-tools), kept verbatim. */
  extra: string[]
  /** Everything after the frontmatter. */
  instructions: string
}

const unquote = (v: string) => v.trim().replace(/^["']+|["']+$/g, '')

export function parseSkillMd(md: string): SkillDoc {
  const m = FM.exec(md)
  if (!m) return { name: '', description: '', extra: [], instructions: md }
  let name = ''
  let description = ''
  const extra: string[] = []
  for (const raw of m[1].split('\n')) {
    const line = raw.replace(/\r$/, '')
    const i = line.indexOf(':')
    const key = i < 0 ? '' : line.slice(0, i).trim()
    if (key === 'name') name = unquote(line.slice(i + 1))
    else if (key === 'description') description = unquote(line.slice(i + 1))
    else extra.push(line)
  }
  return { name, description, extra, instructions: m[2] }
}

/** A YAML scalar on one line: quoted when plain YAML would misread it. */
export function yamlValue(v: string): string {
  const s = v.replace(/\s*\n\s*/g, ' ').trim()
  if (s === '' || /^[\s\-?:,[\]{}#&*!|>'"%@`]/.test(s) || /:\s|\s#|:$/.test(s)) return JSON.stringify(s)
  return s
}

export function buildSkillMd(d: SkillDoc): string {
  const head = [`name: ${yamlValue(d.name)}`, `description: ${yamlValue(d.description)}`, ...d.extra.filter((l) => l.trim())]
  return `---\n${head.join('\n')}\n---\n${d.instructions}`
}

/** The skeleton a new skill starts from: a valid frontmatter, so the first save passes. */
export function newSkillDoc(): SkillDoc {
  return {
    name: '',
    description: '',
    extra: [],
    instructions: '# Instructions\n\n1. …\n',
  }
}

export const SLUG_RE = /^[a-z][a-z0-9-]{1,63}$/

/** Paths the server refuses (skills.CheckPath), plus SKILL.md, which it would drop. */
export function badPath(p: string): boolean {
  if (!p || p.startsWith('/') || p.includes('\\')) return true
  if (p === 'SKILL.md') return true
  return p.split('/').some((seg) => seg === '' || seg === '.' || seg === '..' || seg === '.git')
}
