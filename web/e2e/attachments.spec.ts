import { expect, test } from '@playwright/test'

// A 160×100 PNG mockup.
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAKAAAABkCAIAAACO1KzYAAABAklEQVR42u3dsQmAMBRF0ezkBC6jw1rbq5WljSCSQhdQBBHC98AdIOR0eUVS1QwKXHIFgAVYgAVYgAVYgAELsAALsAALsAD/FnjbswIHGLAAC7AAC7AA6xnwNC9FdXXOrh91BhgwYMCAAQMGDBgwYMCAAQMGDBgwYMCAvwNW8LFBgAEDBgwYMGDAgAEDBhzwoQMwYMCAAQMGDBgwYMCAAQMGDBgwYMCAARsbjA2AAQMGDBiwAAswYMCAAQMGDPgtcN2uhQQYMGDAgAEDBgwYMGDAgAEDBgwYMGDAgAEDBgwYMGDAgAEDBuxzSgEWYMACLMACLMACLMCABViABViABVg3HWD39G8mgeiqAAAAAElFTkSuQmCC', 'base64')
const SHOTS = process.env.E2E_SCREENSHOTS

test.beforeEach(async ({ page }) => {
  await page.goto('/')
  await page.evaluate(() => localStorage.clear())
  await page.goto('/')
  await page.getByLabel('Password').fill('fleetpocket')
  await page.getByLabel('Authenticator code').fill('123456')
  await expect(page.getByRole('heading', { name: 'Tasks' })).toBeVisible()
})

test('attach an image and a file to a new task and see them in the timeline', async ({ page }) => {
  await page.getByRole('link', { name: 'New' }).click()
  await page.getByLabel('What should Claude do?').fill('Make the settings page look like the mock')
  await page.getByTestId('attach-input').setInputFiles({ name: 'mock.png', mimeType: 'image/png', buffer: PNG })
  await page.getByTestId('attach-file-input').setInputFiles({ name: 'notes.txt', mimeType: 'text/plain', buffer: Buffer.from('hi') })
  await expect(page.getByAltText('mock.png')).toBeVisible()
  await expect(page.getByText('notes.txt')).toBeVisible()
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/new-task.png` })

  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page.getByRole('heading', { name: 'Make the settings page look like the mock' })).toBeVisible()
  const thumb = page.getByRole('button', { name: 'Open mock.png' })
  await expect(thumb.getByRole('img')).toBeVisible()
  await expect(page.getByText('2 attachments added to the task')).toBeVisible()
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Download notes.txt' }).click()
  expect((await download).suggestedFilename()).toBe('notes.txt')
  await thumb.click()
  await expect(page.getByRole('dialog').getByRole('img', { name: 'mock.png' })).toBeVisible()
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/viewer.png` })
})

test('send a message with an image from the composer', async ({ page }) => {
  await page.goto('/tasks/5')
  await expect(page.getByRole('button', { name: 'Open retry-flow.png' })).toBeVisible()
  await page.getByTestId('attach-input').setInputFiles({ name: 'error.png', mimeType: 'image/png', buffer: PNG })
  await expect(page.getByAltText('error.png')).toBeVisible()
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/composer.png` })
  await page.getByRole('textbox', { name: 'Message for the next run…' }).fill('This is the error I get')
  await page.getByRole('button', { name: 'Send', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Open error.png' }).getByRole('img')).toBeVisible()
  await expect(page.getByAltText('error.png')).toHaveCount(1) // the preview is gone, the bubble thumbnail stays
  if (SHOTS) await page.screenshot({ path: `${SHOTS}/timeline.png` })
})
