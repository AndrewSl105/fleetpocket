import { devices, expect, test, type Page } from '@playwright/test'

// Team mode in mock mode (docs/API.md "Teams"): enable a team, invite a
// member, the member signs up with TOTP, adds a key and creates a task; the
// admin sees it on the canvas with the member's initials; the member answers
// the agent's question. Desktop: the canvas is a desktop view.
test.use({ ...devices['Desktop Chrome'], viewport: { width: 1440, height: 900 } })

test.beforeEach(async ({ page }) => {
  await page.goto('/')
  await page.evaluate(() => localStorage.clear())
  await page.goto('/')
})

async function signIn(page: Page, password: string, email?: string) {
  if (email) await page.getByLabel('Email').fill(email)
  await page.getByLabel('Password').fill(password)
  await page.getByLabel('Authenticator code').fill('123456')
  // Signing in again keeps the page the session ended on: go to Tasks.
  await page.getByRole('link', { name: 'Tasks' }).first().click()
  await expect(page.getByRole('heading', { name: 'Tasks' })).toBeVisible()
}

async function signOut(page: Page) {
  await page.getByRole('link', { name: 'Settings' }).first().click()
  await page.getByRole('button', { name: 'Log out' }).click()
  await expect(page.getByLabel('Authenticator code')).toBeVisible()
}

test('single-owner mode shows only the Create a team card', async ({ page }) => {
  await signIn(page, 'fleetpocket')
  await page.getByRole('link', { name: 'Settings' }).first().click()
  await expect(page.getByRole('heading', { name: 'Create a team' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'My keys' })).toHaveCount(0)
  await expect(page.getByRole('radiogroup', { name: 'Mine / Everyone' })).toHaveCount(0)
})

test('enable team → invite → signup with TOTP → key → task → admin canvas → member answers', async ({ page }) => {
  test.setTimeout(120_000)
  // The owner creates the team.
  await signIn(page, 'fleetpocket')
  await page.getByRole('link', { name: 'Settings' }).first().click()
  await page.getByRole('button', { name: 'Create a team' }).click()
  await page.getByLabel('Your name').fill('Ann Admin')
  await page.getByLabel('Your email').fill('ann@example.com')
  await page.getByRole('button', { name: 'Create team' }).click()
  await expect(page.getByRole('heading', { name: 'Team', exact: true })).toBeVisible()
  await expect(page.getByTestId('signed-in-as')).toContainText('Signed in as Ann Admin')

  // Invite a member; the link is shown once.
  await page.getByLabel('Role', { exact: true }).selectOption('member')
  await page.getByRole('button', { name: 'Create link' }).click()
  const link = await page.getByLabel('Invite link (shown once)').inputValue()
  expect(link).toContain('/signup?token=')
  await signOut(page)

  // The member signs up: details, then TOTP enrolment.
  await page.goto(link)
  await page.getByLabel('Name').fill('Mia Member')
  await page.getByLabel('Email').fill('mia@example.com')
  await page.getByLabel('Password (at least 12 characters)').fill('a-long-password')
  await page.getByRole('button', { name: 'Next: two-factor code' }).click()
  await expect(page.getByTestId('totp-secret')).toBeVisible()
  await page.getByLabel('Code from the app').fill('000000')
  await page.getByRole('button', { name: 'Create account' }).click()
  await expect(page.getByRole('alert')).toBeVisible()
  await page.getByLabel('Code from the app').fill('654321')
  await page.getByRole('button', { name: 'Create account' }).click()
  await expect(page.getByRole('heading', { name: 'Tasks' })).toBeVisible()

  // No key yet: a task stops with needs_key.
  await page.getByRole('link', { name: 'New' }).first().click()
  await page.getByLabel('What should Claude do?').fill('Paginate the audit log')
  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page.getByText('Add your Claude key in My keys')).toBeVisible()
  await expect(page.getByText('Needs key').first()).toBeVisible()

  // Add a key in My keys, then retry.
  await page.getByRole('link', { name: 'Open My keys' }).click()
  await expect(page.getByRole('heading', { name: 'My keys' })).toBeVisible()
  const claude = page.getByTestId('key-claude')
  await claude.getByRole('button', { name: 'Add key' }).click()
  await claude.getByLabel('Key', { exact: true }).fill('sk-ant-api03-member-1234')
  await claude.getByRole('button', { name: 'Save key' }).click()
  await expect(claude).toContainText('…1234')
  await claude.getByRole('button', { name: 'Test' }).click()
  await expect(page.getByText('The key works.')).toBeVisible()

  await page.getByRole('link', { name: 'Tasks' }).first().click()
  await page.getByRole('link', { name: /Paginate the audit log/ }).click()
  await page.getByRole('button', { name: 'Task actions' }).click()
  await page.getByRole('menuitem', { name: 'Retry' }).click()
  await expect(page.getByText('Should the page size stay at 50')).toBeVisible({ timeout: 30_000 })
  await signOut(page)

  // The admin (break-glass login, no email) sees it on the canvas with the member's initials.
  await signIn(page, 'fleetpocket')
  await page.getByRole('link', { name: 'Canvas' }).click()
  const node = page.getByRole('region', { name: 'Paginate the audit log', exact: true })
  await expect(node).toBeVisible()
  await expect(node.getByRole('img', { name: 'Mia Member' })).toHaveText('MM')
  await signOut(page)

  // The member answers the agent's question.
  await signIn(page, 'a-long-password', 'mia@example.com')
  await page.getByRole('link', { name: /Paginate the audit log/ }).click()
  await page.getByRole('button', { name: 'Keep 50' }).click()
  await expect(page.getByText('Answered via web')).toBeVisible()
})

test('Mine | Everyone filters the task list', async ({ page }) => {
  await signIn(page, 'fleetpocket')
  await page.getByRole('link', { name: 'Settings' }).first().click()
  await page.getByRole('button', { name: 'Create a team' }).click()
  await page.getByLabel('Your name').fill('Ann Admin')
  await page.getByLabel('Your email').fill('ann@example.com')
  await page.getByRole('button', { name: 'Create team' }).click()
  await expect(page.getByRole('heading', { name: 'Team', exact: true })).toBeVisible()
  await page.getByRole('link', { name: 'Tasks' }).first().click()
  // Admins default to Everyone: the seeded tasks (no author) show.
  await expect(page.getByRole('radio', { name: 'Everyone' })).toHaveAttribute('aria-checked', 'true')
  await expect(page.getByText('Retry webhook deliveries with backoff')).toBeVisible()
  await page.getByRole('radio', { name: 'Mine' }).click()
  await expect(page.getByText('Retry webhook deliveries with backoff')).toHaveCount(0)
})
