import { expect, test, type Page } from '@playwright/test'

// Checkpoints in mock mode (src/mocks/checkpoints.ts seedCheckpoints): "Add an
// orders table" ran with checkpoints, then was restored to checkpoint 2, so its
// follow-up run is an abandoned branch.

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

test('restore a task to a checkpoint with a follow-up', async ({ page }) => {
  await login(page)
  await page.getByRole('tab', { name: 'PR open' }).click()
  await page.getByText('Add an orders table').first().click()
  await expect(page.getByText(/Restored to checkpoint 2/)).toBeVisible()
  await expect(page.getByText(/Rewrote agent\/.* with --force-with-lease/)).toBeVisible()
  // The discarded follow-up is kept, collapsed.
  const abandoned = page.getByRole('button', { name: /Abandoned branch · \d+ entries/ })
  await expect(abandoned).toBeVisible()
  await abandoned.click()
  await expect(page.getByRole('button', { name: 'Write · scripts/backfill_orders.ts' })).toBeVisible()

  await page.getByRole('button', { name: /Restore here/ }).last().click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('heading', { name: 'Restore checkpoint 1?' })).toBeVisible()
  await expect(dialog.getByText('This discards')).toBeVisible()
  await expect(dialog.getByText('db/schema.sql')).toBeVisible()
  await expect(dialog.getByText(/Claude resumes its session/)).toBeVisible()
  await dialog.getByLabel('Then (optional)').fill('then do it with a view instead')
  await dialog.getByRole('button', { name: 'Restore and continue' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByText('then do it with a view instead')).toBeVisible()
})

test('the Checkpoints setting is off by default and can be turned on', async ({ page }) => {
  await login(page)
  await page.goto('/settings')
  const toggle = page.getByRole('switch', { name: 'Take checkpoints' })
  await expect(toggle).toHaveAttribute('aria-checked', 'false')
  await toggle.click()
  await expect(toggle).toHaveAttribute('aria-checked', 'true')
})
