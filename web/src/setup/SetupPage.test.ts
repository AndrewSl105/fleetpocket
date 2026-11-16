import { describe, expect, it } from 'vitest'
import { ApiError } from '@/api/errors'
import { t } from '@/i18n/en'
import { initialStep, setupErrorMessage } from './SetupPage'

describe('setup wizard', () => {
  it('starts at the password unless GitHub sends the user back', () => {
    expect(initialStep(new URLSearchParams('code=ABC'))).toBe('password')
    expect(initialStep(new URLSearchParams('step=github'))).toBe('github')
    expect(initialStep(new URLSearchParams('step=repo'))).toBe('repo')
    expect(initialStep(new URLSearchParams('step=totp'))).toBe('password')
  })

  it('maps API errors to messages', () => {
    expect(setupErrorMessage(new ApiError(403, 'bad_setup_code', 'x'))).toBe(t.setup.badCode)
    expect(setupErrorMessage(new ApiError(409, 'setup_done', 'x'))).toBe(t.setup.done)
    expect(setupErrorMessage(new ApiError(401, 'bad_credentials', 'x'))).toBe(t.setup.badTotp)
    expect(setupErrorMessage(new Error('boom'))).toBe(t.common.somethingWrong)
  })
})

describe('GitHub step', () => {
  it('explains callback errors', async () => {
    const { githubErrorMessage } = await import('./GitHubStep')
    expect(githubErrorMessage(null)).toBeNull()
    expect(githubErrorMessage('state')).toBe(t.setup.githubErrors.state)
    expect(githubErrorMessage('other')).toBe(t.common.somethingWrong)
  })
})
