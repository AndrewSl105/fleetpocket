import { describe, expect, it } from 'vitest'
import { emptyValues, parseMoney, parsePreset, presetCron, toInput, toValues } from './scheduleForm'
import { describeCron } from '@/i18n/schedules'

describe('schedule presets', () => {
  it('builds cron expressions from presets', () => {
    expect(presetCron('daily', '09:30', 1, '')).toBe('30 9 * * *')
    expect(presetCron('weekdays', '08:05', 1, '')).toBe('5 8 * * 1-5')
    expect(presetCron('weekly', '17:00', 5, '')).toBe('0 17 * * 5')
    expect(presetCron('custom', '09:00', 1, ' */15 * * * * ')).toBe('*/15 * * * *')
  })

  it('reads presets back, anything else is custom', () => {
    expect(parsePreset('30 9 * * *')).toEqual({ preset: 'daily', time: '09:30', weekday: 1 })
    expect(parsePreset('0 9 * * 1-5')).toMatchObject({ preset: 'weekdays', time: '09:00' })
    expect(parsePreset('0 17 * * 5')).toEqual({ preset: 'weekly', time: '17:00', weekday: 5 })
    expect(parsePreset('*/15 * * * *').preset).toBe('custom')
    expect(describeCron('0 17 * * 5')).toBe('Fridays at 17:00')
    expect(describeCron('@daily')).toBe('@daily')
  })
})

describe('schedule input', () => {
  it('sends nulls for empty optional fields and the loop only when on', () => {
    const v = { ...emptyValues('vatary'), name: ' Nightly ', prompt: ' Fix tests ', timezone: 'Europe/Kyiv', preset: 'daily' as const, time: '03:00' }
    expect(toInput(v)).toEqual({
      name: 'Nightly',
      project_id: 'vatary',
      repository_id: null,
      prompt: 'Fix tests',
      agent: null,
      skills: [],
      mcp_servers: null,
      cron: '0 3 * * *',
      timezone: 'Europe/Kyiv',
      run_budget_usd: null,
      monthly_budget_usd: null,
      concurrency: 'skip',
      pr_only_if_changes: true,
      ends_at: null,
      loop: null,
      enabled: true,
    })
    const looped = toInput({ ...v, monthly_budget: '20', ends_on: '2026-12-31', loop: { enabled: true, check: 'npm test', max: 4, budget: '' } })
    expect(looped).toMatchObject({ monthly_budget_usd: 20, ends_at: '2026-12-31T23:59:59Z', loop: { check_command: 'npm test', max_iterations: 4 } })
  })

  it('round-trips a schedule', () => {
    const s = {
      id: 1, name: 'N', project_id: 'p', repository_id: 3, prompt: 'x', agent: 'codex' as const, skills: ['a'], mcp_servers: ['linear'], cron: '0 9 * * 1-5',
      timezone: 'UTC', run_budget_usd: 1, monthly_budget_usd: null, concurrency: 'queue' as const, pr_only_if_changes: false, ends_at: null,
      loop: { check_command: 'make lint', max_iterations: 2, budget_usd: 1.5 }, enabled: false, next_run_at: null, created_at: '', updated_at: '',
      month_spend_usd: 0, busy: false, last_firing: null,
    }
    expect(toInput(toValues(s))).toEqual({
      name: 'N', project_id: 'p', repository_id: 3, prompt: 'x', agent: 'codex', skills: ['a'], mcp_servers: ['linear'], cron: '0 9 * * 1-5', timezone: 'UTC',
      run_budget_usd: 1, monthly_budget_usd: null, concurrency: 'queue', pr_only_if_changes: false, ends_at: null,
      loop: { check_command: 'make lint', max_iterations: 2, budget_usd: 1.5 }, enabled: false,
    })
  })

  it('parses money', () => {
    expect(parseMoney('')).toBeNull()
    expect(parseMoney('2.5')).toBe(2.5)
    expect(parseMoney('-1')).toBeUndefined()
    expect(parseMoney('abc')).toBeUndefined()
  })
})
