import { describe, expect, it } from 'vitest'
import { costsCsv } from '@/api/costs'
import type { CostReport } from '@/api/types'
import { rangeDates } from './range'

const sum = { cost_usd: 0, estimated_usd: 0, autofix_usd: 0, api_equivalent_usd: 0, runs: 0, unknown_runs: 0, input_tokens: 0, output_tokens: 0 }

describe('rangeDates', () => {
  const today = new Date(2026, 10, 17)
  it('this month by day', () => expect(rangeDates('month', today)).toEqual({ from: '2026-11-01', to: '2026-11-17', period: 'day' }))
  it('last 30 days by day', () => expect(rangeDates('last30', today)).toEqual({ from: '2026-10-19', to: '2026-11-17', period: 'day' }))
  it('last 90 days by week', () => expect(rangeDates('last90', today).period).toBe('week'))
})

describe('costsCsv', () => {
  it('writes one quoted row per day, project and agent', () => {
    const report = {
      rows: [{ ...sum, day: '2026-11-02', project_id: 'a,b', agent: 'codex', cost_usd: 1.5, estimated_usd: 1.5, runs: 2, input_tokens: 10 }],
    } as unknown as CostReport
    const lines = costsCsv(report).trim().split('\n')
    expect(lines[0]).toBe('day,project_id,agent,cost_usd,estimated_usd,autofix_usd,api_equivalent_usd,runs,unknown_runs,input_tokens,output_tokens')
    expect(lines[1]).toBe('2026-11-02,"a,b",codex,1.5,1.5,0,0,2,0,10,0')
  })
})

describe('timeline cost labels', () => {
  it('marks estimates with ≈ and unknown costs with $?', async () => {
    const { dividerText } = await import('@/routes/tasks/TimelineRow')
    expect(dividerText('run_finished:0|0.84|estimated')).toContain('cost ≈$0.84')
    expect(dividerText('run_finished:0|0.84|reported')).toContain('cost $0.84')
    expect(dividerText('run_finished:0|0|unknown')).toContain('cost $?')
    expect(dividerText('run_finished:0')).not.toContain('cost')
    expect(dividerText('budget:{"phase":"warning","scope":"global","spent_usd":80,"limit_usd":100}')).toBe(
      'The global monthly budget is at 80% ($80.0 of $100.0)',
    )
  })
})

describe('cost format', () => {
  it('is one format for known, estimated, unknown and partly unknown costs', async () => {
    const { usd, costLabel } = await import('@/lib/format')
    expect(usd(0.84)).toBe('$0.84')
    expect(usd(0.84, true)).toBe('≈$0.84')
    expect(usd(0, false, true)).toBe('$?')
    expect(usd(0.84, false, true)).toBe('$0.84+?')
    expect(usd(0.84, true, true)).toBe('≈$0.84+?')
    expect(costLabel(0, false, true)).toBe('cost unknown')
    expect(costLabel(0.84, true, true)).toBe('about $0.84 plus an unknown part')
  })

  it("writes 'unknown' in the CSV when none of a row's runs has a price", () => {
    const report = {
      rows: [
        { ...sum, day: '2026-11-02', project_id: 'p', agent: 'cursor', runs: 1, unknown_runs: 1 },
        { ...sum, day: '2026-11-02', project_id: 'p', agent: 'codex', cost_usd: 1, runs: 2, unknown_runs: 1 },
      ],
    } as unknown as CostReport
    const lines = costsCsv(report).trim().split('\n')
    expect(lines[1]).toBe('2026-11-02,p,cursor,unknown,0,0,0,1,1,0,0')
    expect(lines[2]).toBe('2026-11-02,p,codex,1,0,0,0,2,1,0,0')
  })
})
