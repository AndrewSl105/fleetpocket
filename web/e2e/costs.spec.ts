import { readFileSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'

// Costs and budgets in mock mode (src/mocks/costs.ts seedCosts): Vatary has a
// $3.50 monthly budget it has used up, so "Archive old webhook logs" waits; the
// Bioma Codex task's cost is an estimate.

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

test('the Costs screen: totals, projects, agents, days and CSV', async ({ page }) => {
  await login(page)
  await page.goto('/costs')
  await expect(page.getByRole('heading', { name: 'Costs' })).toBeVisible()
  await expect(page.getByText('Total', { exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'By project' })).toBeVisible()
  await expect(page.getByRole('link', { name: /Vatary/ })).toBeVisible()
  await expect(page.getByText(/Budget: \$.* of \$3\.50/)).toBeVisible()
  await expect(page.getByRole('heading', { name: 'By agent' })).toBeVisible()
  await expect(page.getByText('≈$1.29').first()).toBeVisible()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export CSV' }).click()
  const file = await download
  expect(file.suggestedFilename()).toMatch(/^costs-\d{4}-\d{2}-\d{2}-to-\d{4}-\d{2}-\d{2}\.csv$/)
  const csv = readFileSync((await file.path())!, 'utf8')
  expect(csv.split('\n')[0]).toBe('day,project_id,agent,cost_usd,estimated_usd,autofix_usd,api_equivalent_usd,runs,unknown_runs,input_tokens,output_tokens')
  expect(csv).toContain(',bioma,codex,1.29,1.29,')
  await page.getByRole('radio', { name: 'Last 90 days' }).click()
  await expect(page.getByRole('heading', { name: 'By week' })).toBeVisible()
})

test('a task waiting for the monthly budget starts anyway', async ({ page }) => {
  await login(page)
  await page.getByText('Archive old webhook logs').click()
  await expect(page.getByText("Waiting: budget. This project's monthly budget is used up.")).toBeVisible()
  await page.getByRole('button', { name: 'Start anyway' }).click()
  await expect(page.getByText('Starting despite the budget').first()).toBeVisible()
  await expect(page.getByRole('button', { name: 'Start anyway' })).toBeHidden()
})

test('project cards show the month against the budget; tasks show estimates', async ({ page }) => {
  await login(page)
  await page.goto('/projects')
  const vatary = page.getByRole('link', { name: /Vatary/ })
  await expect(vatary.getByRole('meter', { name: 'Budget' })).toBeVisible()
  await expect(vatary.getByText(/Budget: \$.* of \$3\.50/)).toBeVisible()
  await page.goto('/')
  await page.getByRole('tab', { name: /Done/ }).click().catch(() => {})
  await page.goto('/tasks/' + (await taskId(page, 'Add a dark mode toggle')))
  await expect(page.getByText('≈$1.29').first()).toBeVisible()
  await expect(page.getByText('cost ≈$1.29')).toBeVisible()
})

async function taskId(page: Page, title: string): Promise<number> {
  return page.evaluate(async (t) => {
    const res = await fetch('/api/v1/tasks?limit=200', { credentials: 'include' })
    const body = (await res.json()) as { items: { id: number; title: string }[] }
    return body.items.find((i) => i.title === t)!.id
  }, title)
}
