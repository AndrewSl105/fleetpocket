import { describe, expect, it } from 'vitest'
import type { TimelineItem } from '@/lib/timeline'
import {
  classify,
  classifyTool,
  currentActivity,
  diffTotals,
  extractPath,
  isTestCommand,
  LANE_BLOCKS,
  laneCounts,
  stepCount,
  touchedFiles,
} from './activity'

let n = 0
const tool = (name: string, summary: string, ok: boolean | null = true): TimelineItem => ({
  kind: 'tool',
  key: String(++n),
  tool: name,
  summary,
  ok,
  at: '2026-11-17T10:00:00Z',
})

describe('subagents', () => {
  it('carries the subagent as via', () => {
    const item: TimelineItem = { kind: 'tool', key: 'x', tool: 'Grep', summary: 'Grep router', ok: null, subagent: 'explorer', at: '2026-11-17T10:00:00Z' }
    expect(classify(item)).toMatchObject({ kind: 'read', via: 'explorer' })
    expect(classify(tool('Grep', 'Grep router'))).not.toHaveProperty('via')
  })
})

describe('classifyTool', () => {
  it.each(['Read', 'Grep', 'Glob', 'LS', 'NotebookRead'])('%s is read', (name) => {
    expect(classifyTool(name, `${name} src/a.ts`).kind).toBe('read')
  })

  it.each(['Write', 'Edit', 'MultiEdit', 'NotebookEdit', 'Delete'])('%s is edit (W)', (name) => {
    const a = classifyTool(name, `${name} src/a.ts`)
    expect(a).toMatchObject({ kind: 'edit', verb: 'WRITING', path: 'src/a.ts', mode: 'W' })
  })

  it('WebFetch targets the host, WebSearch the query', () => {
    expect(classifyTool('WebFetch', 'WebFetch https://pkg.go.dev/net/http')).toMatchObject({ kind: 'web', verb: 'FETCHING', target: 'pkg.go.dev' })
    expect(classifyTool('WebSearch', 'WebSearch vitest fake timers')).toMatchObject({ kind: 'web', target: 'vitest fake timers' })
  })

  it('Bash is shell, or test for test runners', () => {
    expect(classifyTool('Bash', 'Bash: ls -la')).toMatchObject({ kind: 'shell', verb: 'RUNNING', target: 'ls -la' })
    expect(classifyTool('Bash', 'Bash: cd server && go test ./...')).toMatchObject({ kind: 'test', verb: 'TESTING' })
  })

  it.each(['TodoWrite', 'Task'])('%s is think', (name) => {
    expect(classifyTool(name, `${name} (3 items)`).kind).toBe('think')
  })

  it('AskUserQuestion is ask', () => {
    expect(classifyTool('AskUserQuestion', 'AskUserQuestion').kind).toBe('ask')
  })

  it('MCP tools are web with the server as target', () => {
    expect(classifyTool('github.search_issues', 'github.search_issues')).toMatchObject({ kind: 'web', target: 'github' })
    expect(classifyTool('mcp__linear__get_issue', 'mcp__linear__get_issue')).toMatchObject({ kind: 'web', target: 'linear' })
    expect(classifyTool('mcp_tool_call', 'mcp_tool_call').kind).toBe('web')
  })

  it('unknown tools are shell with the raw name as verb', () => {
    expect(classifyTool('Frobnicate', 'Frobnicate now')).toMatchObject({ kind: 'shell', verb: 'Frobnicate', target: 'now' })
    expect(() => classifyTool('', '')).not.toThrow()
  })
})

describe('isTestCommand', () => {
  it.each(['go test ./...', 'npm test', 'npm run test -- x', 'pnpm test', 'yarn test', 'npx vitest run', 'jest --ci', 'pytest -q', 'cargo test', 'make test', 'npx playwright test'])(
    'detects %s',
    (cmd) => expect(isTestCommand(cmd)).toBe(true),
  )
  it.each(['npm install', 'go build ./...', 'cat testdata.txt', 'make lint'])('ignores %s', (cmd) => expect(isTestCommand(cmd)).toBe(false))
})

describe('extractPath', () => {
  it('finds the first path-looking token', () => {
    expect(extractPath('Read README.md (120 bytes)', 'Read')).toBe('README.md')
    expect(extractPath('Grep TODO in server/internal', 'Grep')).toBe('server/internal')
    expect(extractPath('Edit web/src/a.ts, web/src/b.ts', 'Edit')).toBe('web/src/a.ts')
    expect(extractPath('Glob **/*.ts', 'Glob')).toBeUndefined()
    expect(extractPath('LS .', 'LS')).toBeUndefined()
  })
})

describe('classify', () => {
  it('maps assistant text, questions, commits and errors', () => {
    expect(classify({ kind: 'claude', key: 'c', text: 'Let me look\nmore', at: '' })).toMatchObject({ kind: 'think', target: 'Let me look' })
    expect(classify({ kind: 'question', key: 'q', questionId: '1-q1', text: 'Which?', options: [], at: '' })?.kind).toBe('ask')
    expect(classify({ kind: 'commit', key: 'k', sha: 'abcdef123', files: 1, insertions: 1, deletions: 0, at: '' })?.kind).toBe('git')
    expect(classify({ kind: 'divider', key: 'd', text: 'interrupt', at: '' })).toBeNull()
  })
})

describe('currentActivity', () => {
  const base = { title: 'T', status_reason: null, pr_number: null, branch: 'fp/1' }
  it('uses the latest item while running', () => {
    const items = [tool('Read', 'Read a.ts'), { kind: 'claude', key: 'x', text: 'hmm', at: '' } as TimelineItem]
    expect(currentActivity(items, { ...base, status: 'running' }).kind).toBe('think')
    expect(currentActivity([tool('Bash', 'Bash: npm test', null)], { ...base, status: 'running' }).kind).toBe('test')
    expect(currentActivity([], { ...base, status: 'running' }).kind).toBe('think')
  })
  it('lets settled statuses win', () => {
    expect(currentActivity([], { ...base, status: 'waiting_input' }, 'Which one?')).toMatchObject({ kind: 'ask', target: 'Which one?' })
    expect(currentActivity([], { ...base, status: 'pushing' })).toMatchObject({ kind: 'git', verb: 'PUSHING' })
    expect(currentActivity([], { ...base, status: 'pr_open', pr_number: 7 })).toMatchObject({ kind: 'done', verb: 'PR OPEN', target: '#7' })
    const err: TimelineItem = { kind: 'error', key: 'e', title: 'agent', message: 'boom', at: '' }
    expect(currentActivity([err], { ...base, status: 'failed' })).toMatchObject({ kind: 'failed', target: 'boom' })
    expect(currentActivity([], { ...base, status: 'paused', status_reason: 'budget' })).toMatchObject({ verb: 'PAUSED', target: 'budget' })
    expect(currentActivity([], { ...base, status: 'queued' }).verb).toBe('QUEUED')
  })
})

describe('lanes and touched', () => {
  it('counts per lane and keeps the newest blocks', () => {
    const items = [
      ...Array.from({ length: 15 }, (_, i) => tool('Read', `Read f${i}.ts`)),
      tool('Bash', 'Bash: ls'),
      tool('Bash', 'Bash: go test ./...'),
      tool('WebFetch', 'WebFetch https://a.dev'),
      tool('Edit', 'Edit a.ts'),
      { kind: 'commit', key: 'c1', sha: 'abc', files: 2, insertions: 10, deletions: 3, at: '' } as TimelineItem,
    ]
    const rows = Object.fromEntries(laneCounts(items).map((r) => [r.lane, r]))
    expect(rows.read.count).toBe(15)
    expect(rows.read.blocks).toHaveLength(LANE_BLOCKS)
    expect(rows.read.blocks.at(-1)).toBe(items[14].key)
    expect(rows.shell.count).toBe(2)
    expect(rows.web.count).toBe(1)
    expect(rows.edit.count).toBe(1)
    expect(rows.git.count).toBe(1)
    expect(stepCount(items)).toBe(19)
    expect(diffTotals(items)).toEqual({ files: 2, insertions: 10, deletions: 3 })
  })

  it('de-duplicates touched files, newest first', () => {
    const items = [tool('Read', 'Read a.ts'), tool('Read', 'Read b.ts'), tool('Edit', 'Edit a.ts'), tool('Read', 'Read b.ts'), tool('Grep', 'Grep x in c/d.go'), tool('Bash', 'Bash: ls')]
    expect(touchedFiles(items)).toEqual([
      { path: 'c/d.go', mode: 'R' },
      { path: 'b.ts', mode: 'R' },
      { path: 'a.ts', mode: 'W' },
    ])
  })
})
