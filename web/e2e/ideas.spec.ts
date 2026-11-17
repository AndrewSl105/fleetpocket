import { expect, test, type Page } from '@playwright/test'

// The Ideas board in mock mode (src/mocks/ideas.ts): off by default; turning
// it on shows four seeded cards. Chromium records from a fake microphone.

test.use({
  launchOptions: { args: ['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream'] },
  permissions: ['microphone'],
})

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

async function turnOn(page: Page) {
  // Off by default: no record button in the header.
  await expect(page.getByRole('button', { name: 'Record an idea' })).toHaveCount(0)
  await page.getByRole('link', { name: 'Settings' }).first().click()
  const toggle = page.getByRole('switch', { name: 'Show the Ideas board' })
  await expect(toggle).not.toBeChecked()
  await toggle.click()
  await expect(toggle).toBeChecked()
  await page.getByRole('link', { name: 'Open the board' }).click()
  await expect(page.getByRole('heading', { name: 'Ideas' })).toBeVisible()
}

const column = (page: Page, name: string) => page.getByRole('group', { name: 'Columns' }).getByRole('button', { name: new RegExp(`^${name}`) })

test('capture by text and voice, move, start a task, archive and delete', async ({ page }) => {
  await login(page)
  await turnOn(page)

  // Phones: one column at a time with a switcher.
  await expect(column(page, 'Inbox')).toHaveAttribute('aria-pressed', 'true')
  const cards = page.getByTestId('column-inbox').getByTestId('idea-card')
  await expect(cards).toHaveCount(1)
  await column(page, 'Shaping').click()
  await expect(page.getByTestId('column-shaping').getByText('Weekly cost digest in Telegram')).toBeVisible()
  await expect(page.getByTestId('column-shaping').getByText('Voice 0:07')).toBeVisible()
  await column(page, 'In progress').click()
  await expect(page.getByTestId('column-in_progress').getByText('Task failed')).toBeAttached()
  await column(page, 'Inbox').click()

  // Text with a link: the URL becomes a link attachment, the rest the title.
  await page.getByLabel('Write an idea, paste a link or a screenshot').fill('Export the board as CSV https://example.com/spec')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(cards.filter({ hasText: 'Export the board as CSV' })).toBeVisible()
  await expect(cards.filter({ hasText: 'Export the board as CSV' }).getByText('1 link')).toBeVisible()

  // Hold to talk: press, wait, release; the transcript becomes the card.
  const hold = page.getByRole('button', { name: 'Hold to talk' })
  await hold.hover()
  await page.mouse.down()
  await expect(page.getByRole('meter', { name: 'Input level' })).toBeVisible()
  await page.waitForTimeout(1200)
  await page.mouse.up()
  await expect(page.getByText('Saved: Rewrite the onboarding flow').first()).toBeVisible()
  const voiced = cards.filter({ hasText: 'Rewrite the onboarding flow' })
  await expect(voiced.getByText(/Voice 0:0[1-9]/)).toBeVisible()

  // Move to… (the touch alternative to dragging).
  await page.getByRole('button', { name: 'Actions for Export the board as CSV' }).click()
  await page.getByRole('menuitem', { name: 'Move to…' }).click()
  await page.getByRole('dialog').getByRole('button', { name: 'Ready' }).click()
  await expect(page.getByText('Moved to Ready').first()).toBeVisible()
  await column(page, 'Ready').click()
  const ready = page.getByTestId('column-ready').getByTestId('idea-card')
  await expect(ready.filter({ hasText: 'Export the board as CSV' })).toBeVisible()

  // Start a task: the composer is prefilled and the card moves to In progress.
  await ready.filter({ hasText: 'Export the board as CSV' }).getByText('Export the board as CSV').click()
  await expect(page.getByLabel('Title')).toHaveValue('Export the board as CSV')
  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page.getByTestId('from-idea')).toContainText('Export the board as CSV')
  await expect(page.getByRole('textbox', { name: /What should/ })).toHaveValue(/Export the board as CSV[\s\S]*https:\/\/example.com\/spec/)
  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page).toHaveURL(/\/tasks\/\d+/)
  await expect(page.getByText('Started from the idea “Export the board as CSV” · card moved to In progress')).toBeVisible()

  // Back to the idea (mock state lives in the page, so no reload), then the board.
  await page.goBack()
  await expect(page.getByText(/Moved to In progress: task #\d+ started from it/)).toBeVisible()
  await page.getByRole('link', { name: /Board/ }).click()
  await column(page, 'In progress').click()
  const started = page.getByTestId('column-in_progress').getByTestId('idea-card').filter({ hasText: 'Export the board as CSV' })
  await expect(started.getByText(/Moved to In progress: task #\d+ started from it/)).toBeVisible()
  await expect(started.getByText('1 task')).toBeVisible()

  // Archive, then delete for good from the archive.
  await started.getByText('Export the board as CSV').first().click()
  await page.getByRole('button', { name: 'Archive' }).click()
  await expect(page.getByRole('heading', { name: 'Ideas' })).toBeVisible()
  await page.getByRole('link', { name: 'Archived ideas' }).click()
  const archived = page.getByTestId('archived-idea')
  await expect(archived).toHaveCount(1)
  await page.getByRole('button', { name: 'Delete for good: Export the board as CSV' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: /OK|Delete|Confirm/ }).click()
  await expect(page.getByText('No archived ideas.')).toBeVisible()
})

test('rename and add columns', async ({ page }) => {
  await login(page)
  await turnOn(page)
  await page.getByRole('button', { name: 'Columns' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByRole('button', { name: 'Remove Inbox' })).toBeDisabled()
  await dialog.getByLabel('Column 1 name').fill('Dump')
  await dialog.getByRole('button', { name: 'Add column' }).click()
  await dialog.getByLabel('Column 6 name').fill('Someday')
  await dialog.getByRole('button', { name: 'Save columns' }).click()
  await expect(column(page, 'Dump')).toBeVisible()
  await expect(column(page, 'Someday')).toBeVisible()
})

test.describe('desktop', () => {
  test.use({ viewport: { width: 1366, height: 900 }, isMobile: false, hasTouch: false })

  test('columns side by side, drag a card to another column, rail entry', async ({ page }) => {
    await login(page)
    await expect(page.getByRole('link', { name: 'Ideas' })).toHaveCount(0)
    await page.getByRole('link', { name: 'Settings' }).first().click()
    await page.getByRole('switch', { name: 'Show the Ideas board' }).click()
    // The rail gets the entry and the record button once the board is on.
    await page.getByRole('navigation', { name: 'Main' }).getByRole('link', { name: 'Ideas' }).click()
    await expect(page.getByRole('button', { name: 'Record an idea' }).first()).toBeVisible()
    const inbox = page.getByTestId('column-inbox')
    const ready = page.getByTestId('column-ready')
    await expect(inbox.getByTestId('idea-card')).toHaveCount(1)
    await expect(ready.getByText('No cards')).toBeVisible()

    const card = inbox.getByTestId('idea-card').first()
    const from = await card.boundingBox()
    const to = await ready.boundingBox()
    if (!from || !to) throw new Error('no boxes')
    await page.mouse.move(from.x + 40, from.y + 20)
    await page.mouse.down()
    await page.mouse.move(from.x + 60, from.y + 30, { steps: 5 })
    await page.mouse.move(to.x + to.width / 2, to.y + 80, { steps: 15 })
    await page.mouse.up()
    await expect(page.getByText('Moved to Ready').first()).toBeVisible()
    await expect(ready.getByText('Keyboard shortcuts for the canvas')).toBeVisible()
    await expect(inbox.getByTestId('idea-card')).toHaveCount(0)
  })
})
