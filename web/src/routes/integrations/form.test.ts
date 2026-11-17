import { describe, expect, it } from 'vitest'
import type { MCPServer } from '@/api/types'
import { emptyForm, invalidHosts, parseHosts, toForm, toInput, validate } from './form'

const server: MCPServer = {
  id: 3,
  project_id: 'demo',
  name: 'linear',
  kind: 'remote',
  transport: 'http',
  url: 'https://mcp.linear.app/mcp',
  runtime: null,
  package: null,
  command: null,
  args: [],
  secret_hints: { bearer: '…abcd', 'X-Org': '…12' },
  tools_mode: 'deny',
  tools: ['delete_*'],
  read_only: false,
  max_calls: 20,
  enabled: true,
  last_used_at: null,
  last_status: null,
  last_error: null,
  created_at: '2026-11-17T10:00:00Z',
  updated_at: '2026-11-17T10:00:00Z',
}

describe('MCP server form', () => {
  it('never sends stored secrets back; removed ones are deleted', () => {
    const f = toForm(server)
    expect(f.secrets.every((s) => s.value === '')).toBe(true)
    const initial = f.secrets
    f.secrets = f.secrets.filter((s) => s.key !== 'X-Org')
    const body = toInput(f, initial)
    expect(body.secrets).toEqual({ 'X-Org': null })
    f.secrets[0].value = 'lin_new'
    expect(toInput(f, initial).secrets).toEqual({ bearer: 'lin_new', 'X-Org': null })
  })

  it('builds kind-specific bodies', () => {
    const f = { ...emptyForm(), name: 'gh', kind: 'stdio' as const, pkg: '@x/mcp', args: '--a\n\n--b ', toolsMode: 'allow' as const, tools: 'list_*, get' }
    const body = toInput(f, [], null)
    expect(body).toMatchObject({ kind: 'stdio', runtime: 'npm', package: '@x/mcp', args: ['--a', '--b'], tools: ['list_*', 'get'], project_id: null })
    expect(body.url).toBeUndefined()
  })

  it('validates', () => {
    expect(validate({ ...emptyForm(), name: 'Bad' }).name).toBeTruthy()
    expect(validate({ ...emptyForm(), name: 'ok', url: 'https://u:p@x' }).url).toBeTruthy()
    expect(validate({ ...emptyForm(), name: 'ok', url: 'https://x.test/mcp' })).toEqual({})
    expect(validate({ ...emptyForm(), name: 'ok', url: 'https://x', maxCalls: '0' }).maxCalls).toBeTruthy()
  })
})

describe('allowlist hosts', () => {
  it('parses and validates', () => {
    expect(parseHosts('Example.com\n*.npmjs.org, example.com\n\napi.test:8443')).toEqual(['example.com', '*.npmjs.org', 'api.test:8443'])
    expect(invalidHosts(['ok.dev', 'http://x.com', 'a b', '*'])).toEqual(['http://x.com', 'a b', '*'])
  })
})
