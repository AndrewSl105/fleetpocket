import { expect, test, type Page } from '@playwright/test'

// MCP servers and egress in mock mode (src/mocks/integrations.ts): Vatary
// has linear, db and playwright (off), uses the shared sentry server and is
// in allowlist mode; task 12 used the gateway and hit the allowlist.

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

test('project integrations: servers, test connection, add, egress', async ({ page }) => {
  await login(page)
  await page.goto('/projects/vatary')
  await page.getByRole('link', { name: /Integrations/ }).click()
  await expect(page.getByRole('heading', { name: 'Vatary · Integrations' })).toBeVisible()
  await expect(page.getByText('Tasks get: db, linear, sentry')).toBeVisible()

  const servers = page.getByRole('list', { name: 'MCP servers' })
  await expect(servers.getByText('linear', { exact: true })).toBeVisible()
  await servers.getByRole('button', { name: 'Test connection linear' }).click()
  await expect(servers.getByText('Connected · 4 tools')).toBeVisible()
  await expect(servers.getByText('delete_issue')).toHaveClass(/line-through/)

  // Add a server; the stored secret comes back only as a hint.
  await page.getByRole('button', { name: 'Add MCP server' }).first().click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('Name', { exact: true }).fill('notion')
  await dialog.getByLabel('URL').fill('https://mcp.notion.com/mcp')
  await dialog.getByRole('textbox', { name: 'Secret value' }).fill('ntn_secret_value_9z9z')
  await dialog.getByRole('button', { name: 'Save' }).click()
  await expect(servers.getByText('notion', { exact: true })).toBeVisible()
  await servers.getByRole('button', { name: 'Edit MCP server: notion' }).click()
  await expect(page.getByRole('dialog').getByRole('textbox', { name: 'Secret value' })).toHaveAttribute('placeholder', /…9z9z/)
  await expect(page.getByRole('dialog').getByRole('textbox', { name: 'Secret value' })).toHaveValue('')
  await page.keyboard.press('Escape')

  // Shared servers and network access.
  await expect(page.getByRole('switch', { name: 'sentry' })).toBeChecked()
  await expect(page.getByRole('radio', { name: 'Allowlist' })).toHaveAttribute('aria-checked', 'true')
  await page.getByLabel('Extra hosts').fill('api.stripe.com\n*.sentry.io')
  await page.getByRole('button', { name: 'Save network access' }).click()
  await expect(page.getByText(/Saved\. Changes apply from the next run/)).toBeVisible()
})

test('a blocked host can be allowed from the timeline', async ({ page }) => {
  await login(page)
  await page.goto('/tasks/12')
  await expect(page.getByRole('heading', { name: 'Close stale Linear issues fixed by merged PRs' })).toBeVisible()
  await expect(page.getByText('MCP: db, linear, sentry · network: allowlist')).toBeVisible()
  await expect(page.getByText('linear · delete_issue')).toBeVisible()
  await expect(page.getByText('Blocked: status.linear.app')).toBeVisible()
  await page.getByRole('button', { name: 'Allow for this project' }).click()
  await expect(page.getByText('status.linear.app allowed from the next run').first()).toBeVisible()
})

test('the composer shows what the task will get', async ({ page }) => {
  await login(page)
  await page.getByRole('link', { name: 'New' }).first().click()
  await page.getByRole('combobox', { name: 'Project' }).click()
  await page.getByRole('option', { name: 'Vatary' }).click()
  await expect(page.getByTestId('integrations-line')).toContainText('MCP: db, linear, sentry')
  await expect(page.getByTestId('integrations-line')).toContainText('allowlist')
})
