import { expect, test, type Page } from '@playwright/test'

// Schedules and loops in mock mode (src/mocks/schedules.ts): three schedules,
// "Nightly test fixer" fired a loop task whose check failed once, then passed.

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

test('list, detail with history, run now, pause, duplicate, create', async ({ page }) => {
  await login(page)
  // Phones reach Schedules from Settings.
  await page.getByRole('link', { name: 'Settings' }).first().click()
  await page.getByRole('link', { name: /^Schedules/ }).click()
  await expect(page.getByRole('heading', { name: 'Schedules' })).toBeVisible()
  const list = page.getByRole('list', { name: 'Schedules' })
  await expect(list.getByTestId('schedule-row')).toHaveCount(3)
  const nightly = list.getByTestId('schedule-row').filter({ hasText: 'Nightly test fixer' })
  await expect(nightly.getByText('PR open')).toBeVisible()
  await expect(nightly.getByText('Daily at 03:00 · Europe/Kyiv')).toBeVisible()
  await expect(nightly.getByText('$0.64 of $20.0 this month')).toBeVisible()
  await expect(list.getByTestId('schedule-row').filter({ hasText: 'Weekly docs report' }).getByText('Paused')).toBeVisible()

  // Detail and history.
  await nightly.getByRole('link').click()
  await expect(page.getByRole('heading', { name: 'Nightly test fixer' })).toBeVisible()
  const history = page.getByRole('list', { name: 'History' })
  await expect(history.getByTestId('schedule-firing')).toHaveCount(2)
  await expect(history.getByText('Missed', { exact: true })).toBeVisible()
  await expect(history.getByText('Check passed', { exact: true })).toBeVisible()

  // Run now starts a task; a second one is skipped while it is still queued.
  await page.getByRole('button', { name: 'Run now' }).click()
  await expect(page.getByText(/Started task #\d+/).first()).toBeVisible()
  await expect(history.getByTestId('schedule-firing')).toHaveCount(3)
  await page.getByRole('button', { name: 'Run now' }).click()
  await expect(page.getByText(/Not started: skipped/).first()).toBeVisible()

  // Pause, resume.
  await page.getByRole('button', { name: 'Pause' }).click()
  await expect(page.getByText('Paused', { exact: true }).first()).toBeVisible()
  await page.getByRole('button', { name: 'Resume' }).click()
  await expect(page.getByRole('button', { name: 'Pause' })).toBeVisible()

  // Duplicate opens the paused copy.
  await page.getByRole('button', { name: 'Duplicate' }).click()
  await expect(page.getByRole('heading', { name: 'Nightly test fixer (copy)' })).toBeVisible()

  // Create one with the weekly preset (back to the list; the mock database lives in page memory).
  await page.goBack()
  await page.goBack()
  await page.getByRole('link', { name: 'New schedule' }).click()
  await expect(page.getByRole('heading', { name: 'New schedule' })).toBeVisible()
  await page.getByLabel('Name').fill('Friday cleanup')
  await page.getByLabel('Prompt').fill('Remove dead code flagged by the linter.')
  await page.getByRole('radio', { name: 'Weekly' }).click()
  await page.getByRole('button', { name: 'Create schedule' }).click()
  await expect(page.getByRole('heading', { name: 'Friday cleanup' })).toBeVisible()
  await expect(page.getByText(/Mondays at 09:00/)).toBeVisible()
})

test('a loop task shows its iterations with check stamps; the composer starts a loop', async ({ page }) => {
  await login(page)
  await page.getByRole('tab', { name: 'PR open' }).click()
  await page.getByText('Make the audit tests pass').first().click()
  await expect(page.getByText('Iteration 1/3')).toBeVisible()
  await expect(page.getByText('Iteration 2/3')).toBeVisible()
  const checks = page.getByTestId('loop-check')
  await expect(checks.first().getByText('✕ Check failed 1/3')).toBeVisible()
  await expect(checks.nth(1).getByText('✓ Check passed')).toBeVisible()
  await checks.first().getByRole('button', { name: 'Output' }).click()
  await expect(page.getByText('returns next cursor on the last page')).toBeVisible()
  await expect(page.getByText('Loop finished: the check passed')).toBeVisible()

  // The composer's Loop toggle.
  await page.goBack()
  await page.getByRole('link', { name: 'New' }).first().click()
  await page.getByLabel(/What should .* do\?/).fill('Make the lint pass')
  await page.getByRole('switch', { name: 'Loop until a check passes' }).click()
  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page.getByText('Enter the command that exits 0 when the task is done')).toBeVisible()
  await page.getByLabel('Check command').fill('make lint')
  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page.getByText('Iteration 1/3')).toBeVisible()
  await expect(page.getByText('make lint').first()).toBeVisible()
})
