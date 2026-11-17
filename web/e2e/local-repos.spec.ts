import { expect, test, type Page } from '@playwright/test'

// Local mode in the mock (src/mocks/localdb.ts): `fleetpocket.mock.mode=local`
// pretends to be `npx jungle-monkey` with no folders added yet, a fake home
// directory (~/code/shop-api is "this folder") and the CLI's login already done.

async function localMode(page: Page, mode: 'local' | 'local-demo' = 'local', path = '/') {
  await page.goto('/')
  await page.evaluate((m) => {
    localStorage.clear()
    localStorage.setItem('fleetpocket.mock.mode', m)
  }, mode)
  await page.goto(path)
}

test('local onboarding → pick a folder → first task → answer → branch ready → merge', async ({ page }) => {
  await localMode(page)

  // No local repository yet: the first screen is onboarding.
  await expect(page).toHaveURL(/\/welcome$/)
  await expect(page.getByText('Docker is running.')).toBeVisible()
  await page.getByRole('button', { name: 'Next' }).click()
  await expect(page.getByText('Agent login found: Claude, Codex.')).toBeVisible()
  await page.getByRole('button', { name: 'Next' }).click()

  // Pick "this folder" from the suggestions.
  const suggestions = page.getByRole('region', { name: 'Repositories on this computer' })
  const shop = suggestions.getByRole('button', { name: /shop-api/ })
  await expect(shop).toContainText('This folder')
  await expect(shop).toContainText('⎇ main')
  await shop.click()

  // GitHub is optional.
  await expect(page.getByText('Connect GitHub to push and open PRs (optional)', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: 'Skip' }).click()

  // First task.
  await page.getByLabel('What should the agent do in shop-api?').fill('Rate-limit failed logins')
  await expect(page.getByRole('radio', { name: 'Last commit' })).toBeChecked()
  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page).toHaveURL(/\/tasks\/\d+$/)
  await expect(page.getByRole('heading', { name: 'Rate-limit failed logins' })).toBeVisible()

  // The agent asks; answer it.
  await expect(page.getByText('Should failed logins be limited per IP address or per account?')).toBeVisible({ timeout: 20_000 })
  await page.getByRole('button', { name: 'Per IP address' }).click()

  // Delivered into the repository as jm/<id>-<slug>.
  const merge = page.getByRole('button', { name: 'Merge into current branch' })
  await expect(merge).toBeVisible({ timeout: 20_000 })
  await expect(page.getByText(/Branch jm\/\d+-rate-limit-failed-logins is in your repository\./)).toBeVisible()
  await expect(page.getByText(/Branch jm\/\d+-rate-limit-failed-logins ready · 1 commit/)).toBeVisible()
  // No GitHub connected: no Push & open PR.
  await expect(page.getByRole('button', { name: 'Push & open PR' })).toHaveCount(0)

  await merge.click()
  await expect(page.getByText(/Merged jm\/\d+-rate-limit-failed-logins into main \(fast-forward\)/)).toBeVisible()
  await expect(page.locator('header').getByText('Completed')).toBeVisible()
  await expect(merge).toHaveCount(0)
})

test('the folder browser adds a repository found by browsing', async ({ page }) => {
  await localMode(page, 'local', '/welcome?step=folder')
  await page.getByRole('button', { name: 'Browse folders' }).click()
  const browser = page.getByRole('group', { name: 'Folder browser' })
  await browser.getByRole('button', { name: /Documents/ }).click()
  await browser.getByRole('button', { name: /GitHub/ }).click()
  await expect(browser.getByRole('button', { name: 'Use this folder' })).toBeDisabled()
  await expect(browser.getByText('This folder is not a git repository.')).toBeVisible()
  await browser.getByRole('button', { name: /dotfiles/ }).click()
  await browser.getByRole('button', { name: 'Use this folder' }).click()
  await expect(page.getByText('Added dotfiles')).toBeVisible()
})

test('check out a delivered branch; Merge then explains the 409; Push & open PR with GitHub connected', async ({ page }) => {
  await localMode(page, 'local-demo', '/?filter=pr_open')
  await page.getByText('Add rate limiting to the login endpoint').click()
  await expect(page.getByRole('button', { name: 'Push & open PR' })).toBeVisible()
  await page.getByRole('button', { name: 'Check out' }).click()
  await expect(page.getByText(/Checked out jm\/1-/)).toBeVisible()
  // The task branch is now checked out: Merge says so.
  await page.getByRole('button', { name: 'Merge into current branch' }).click()
  await expect(page.getByRole('alert')).toContainText('The task branch is checked out')
})

test('Settings → GitHub connects with the device flow', async ({ page }) => {
  await localMode(page, 'local-demo', '/settings')
  const section = page.getByRole('region', { name: 'GitHub' })
  await expect(section.getByText('Connected as demo-dev')).toBeVisible()
  await section.getByRole('button', { name: 'Disconnect' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Disconnect' }).click()
  await section.getByRole('button', { name: 'Sign in with GitHub' }).click()
  await expect(section.getByLabel('Device code')).toHaveText('WDJB-MJHT')
  await expect(section.getByRole('link', { name: /github.com\/login\/device/ })).toHaveAttribute('href', 'https://github.com/login/device')
  await expect(section.getByText('Connected as demo-dev')).toBeVisible({ timeout: 10_000 })
})
