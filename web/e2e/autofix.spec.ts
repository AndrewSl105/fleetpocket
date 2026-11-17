import { expect, test, type Page } from '@playwright/test'

// CI auto-fix in mock mode (src/mocks/db.ts seedAutoFix): task 10 went green
// after one fix, task 11's fix skips a test and waits for the owner.

test.beforeEach(async ({ page }) => {
  await page.goto('/')
  await page.evaluate(() => localStorage.clear())
  await page.goto('/')
})

async function login(page: Page) {
  await page.getByLabel('Password').fill('fleetpocket')
  await page.getByLabel('Authenticator code').fill('123456')
  await expect(page.getByRole('heading', { name: 'Tasks' })).toBeVisible()
}

test('a PR that went green after one fix', async ({ page }) => {
  await login(page)
  await page.goto('/tasks/10')
  await expect(page.getByRole('heading', { name: 'Add totals row to the CSV export' })).toBeVisible()
  await expect(page.getByTestId('ci-badge')).toHaveText('CI ✓')
  await expect(page.getByText('✓ CI green after 1 fix')).toBeVisible()
  const attempt = page.getByRole('region', { name: 'Auto-fix attempt 1/2' })
  await attempt.scrollIntoViewIfNeeded()
  await expect(attempt.getByRole('link', { name: 'ci / test' })).toBeVisible()
  await expect(attempt.getByText('7f3a9c21d04be815')).toBeVisible()
  await expect(attempt.getByText('ci-logs-attempt-1.txt')).toBeVisible()
})

test('a fix that changes tests waits for the owner', async ({ page }) => {
  await login(page)
  await page.goto('/tasks/11')
  await expect(page.getByTestId('ci-badge')).toHaveText('CI ✕ · needs you')
  await expect(page.getByText('The fix changes CI or tests. Push anyway?')).toBeVisible()
  await page.getByRole('button', { name: 'Discard' }).click()
  await expect(page.getByText('Auto-fix discarded')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Push anyway' })).toBeDisabled()
})

test('auto-fix settings: global default and project override', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'Settings' }).click()
  const section = page.getByRole('region', { name: 'CI auto-fix' })
  await section.scrollIntoViewIfNeeded()
  await section.getByRole('switch', { name: 'Fix failing CI' }).click()
  await section.getByLabel('Attempts (1–5)').fill('3')
  await section.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('CI auto-fix defaults saved')).toBeVisible()

  // In-app navigation keeps the mock backend's state (a reload reseeds it).
  await page.getByRole('link', { name: 'Projects' }).click()
  await page.getByText('Bioma', { exact: true }).click()
  // Bioma inherits the new default (on, 3 attempts).
  await expect(page.getByLabel('Fix failing CI')).toHaveText('Default (on)')
  await expect(page.getByLabel('Attempts (1–5)')).toHaveAttribute('placeholder', 'Default (3)')
  await page.goBack()
  await page.getByText('Vatary', { exact: true }).click()
  await expect(page.getByLabel('Fix failing CI')).toHaveText('On')
})
