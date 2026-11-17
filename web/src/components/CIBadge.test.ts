import { describe, expect, it } from 'vitest'
import { ciBadge } from './CIBadge'

const base = { ci_status: null, autofix_state: null, autofix_attempts: 0, autofix_max_attempts: null } as const

describe('ciBadge', () => {
  it('shows CI alone without a loop', () => {
    expect(ciBadge(base)).toBeNull()
    expect(ciBadge({ ...base, ci_status: 'success' })).toEqual({ text: 'CI ✓', tone: 'olive' })
    expect(ciBadge({ ...base, ci_status: 'failure' })).toEqual({ text: 'CI ✕', tone: 'danger' })
  })

  it('shows the auto-fix loop', () => {
    expect(ciBadge({ ...base, ci_status: 'failure', autofix_state: 'fixing', autofix_attempts: 1, autofix_max_attempts: 2 })?.text).toBe('CI ✕ · fixing 1/2')
    expect(ciBadge({ ...base, ci_status: 'failure', autofix_state: 'needs_you', autofix_attempts: 2, autofix_max_attempts: 2 })).toEqual({
      text: 'CI ✕ · needs you',
      tone: 'ask',
    })
    expect(ciBadge({ ...base, ci_status: 'success', autofix_state: 'green', autofix_attempts: 1, autofix_max_attempts: 2 })?.text).toBe('CI ✓')
  })
})
