import { readFileSync } from 'node:fs'
import { expect, test, type Page } from '@playwright/test'

// The Skill library in mock mode (src/mocks/skills.ts): four diagram skills,
// pr-checklist enabled by default, Vatary enables db-schema. The mock database
// lives in page memory, so after a change the test moves by clicking links.

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

test('list → open → edit → save; a secret is refused; enable on a project; the composer and Info show it', async ({ page }) => {
  await login(page)
  // Phones reach Skills from Settings.
  await page.getByRole('link', { name: 'Settings' }).first().click()
  await page.getByRole('link', { name: /^Skills/ }).click()
  await expect(page.getByRole('heading', { name: 'Skills' })).toBeVisible()
  const list = page.getByRole('list', { name: 'Skills' })
  const checklist = list.getByRole('listitem').filter({ hasText: 'pr-checklist' })
  await expect(checklist.getByText('default', { exact: true })).toBeVisible()
  await expect(checklist.getByText('2 files · All projects')).toBeVisible()
  await expect(list.getByRole('listitem').filter({ hasText: 'db-schema' }).getByText('1 file · vatary')).toBeVisible()

  // Open and edit.
  await list.getByRole('link', { name: /infra-map/ }).click()
  await expect(page.getByRole('heading', { name: 'Skill · infra-map' })).toBeVisible()
  await page.getByLabel('Description').fill('Draw the infrastructure: services, networks and ports.')
  await page.getByRole('button', { name: 'Add file' }).click()
  await page.getByLabel('Path 1').fill('ref/legend.md')
  await page.getByLabel('Content ref/legend.md').fill('Boxes are services.')

  // The secrets scan refuses the save and the finding stays on screen.
  const body = page.getByLabel('SKILL.md')
  await body.fill(`${await body.inputValue()}\nUse key sk-ant-api03-abcdefghijklmnopqrstuvwxyz\n`)
  await page.getByRole('button', { name: 'Save skill' }).click()
  await expect(page.getByRole('alert')).toContainText('possible secrets: SKILL.md:')
  await body.fill((await body.inputValue()).replace(/\nUse key .*\n/, '\n'))
  await page.getByRole('button', { name: 'Save skill' }).click()
  await expect(page.getByText('Skill saved').first()).toBeVisible()
  await expect(page.getByRole('alert')).toBeHidden()

  // Skills is a deep screen on phones (no tab bar): back to the list, then to Settings.
  await page.goBack()
  const infra = list.getByRole('listitem').filter({ hasText: 'infra-map' })
  await expect(infra.getByText('Draw the infrastructure: services, networks and ports.')).toBeVisible()
  await expect(infra.getByText('2 files · No project')).toBeVisible()

  await page.goBack()
  await expect(page.getByRole('heading', { name: 'Settings' })).toBeVisible()

  // Enable it on Bioma; output stays PR.
  await page.getByRole('link', { name: 'Projects', exact: true }).first().click()
  await page.getByRole('link', { name: /Bioma/ }).click()
  await expect(page.getByRole('heading', { name: 'Skills', exact: true })).toBeVisible()
  await expect(page.getByRole('switch', { name: 'pr-checklist' })).toBeChecked()
  await expect(page.getByRole('switch', { name: 'pr-checklist' })).toBeDisabled()
  await expect(page.getByText('Diagrams: coming soon')).toBeVisible()
  await page.getByRole('switch', { name: 'infra-map' }).click()
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByText('Project saved').first()).toBeVisible()
  await expect(page.getByRole('switch', { name: 'infra-map' })).toBeChecked()

  await page.goBack()
  await page.getByRole('link', { name: 'Tasks', exact: true }).first().click()

  // The composer shows what a Bioma task gets; drop one, add one.
  await page.getByRole('link', { name: 'New' }).first().click()
  await page.getByRole('combobox', { name: 'Project' }).click()
  await page.getByRole('option', { name: 'Bioma' }).click()
  const chip = (slug: string) => page.getByRole('button', { name: slug, exact: true })
  await expect(chip('infra-map')).toHaveAttribute('aria-pressed', 'true')
  await expect(chip('pr-checklist')).toHaveAttribute('aria-pressed', 'true')
  await expect(chip('db-schema')).toHaveAttribute('aria-pressed', 'false')
  await chip('pr-checklist').click()
  await chip('db-schema').click()
  await expect(chip('pr-checklist')).toHaveAttribute('aria-pressed', 'false')
  await page.getByLabel(/What should .* do\?/).fill('Map the infrastructure')
  await page.getByRole('button', { name: 'Start task' }).click()
  await expect(page.getByRole('heading', { name: 'Map the infrastructure' })).toBeVisible()
  await page.getByRole('tab', { name: 'Info' }).click()
  await expect(page.getByText('db-schema, infra-map')).toBeVisible()
})

test('new, import, duplicate, export and delete', async ({ page }) => {
  await login(page)
  await page.goto('/skills')
  await page.getByRole('link', { name: 'New skill' }).click()
  await page.getByLabel('Slug').fill('release-notes')
  await page.getByLabel('Description').fill('Write release notes: from the merged PRs')
  await page.getByRole('button', { name: 'Create skill' }).click()
  await expect(page.getByRole('heading', { name: 'Skill · release-notes' })).toBeVisible()

  // Duplicate from the editor opens the copy.
  await page.getByRole('button', { name: 'Duplicate' }).click()
  await expect(page.getByRole('heading', { name: 'Skill · release-notes-copy' })).toBeVisible()
  await expect(page.getByLabel('Description')).toHaveValue('Write release notes: from the merged PRs')

  // Export downloads <slug>.zip with SKILL.md in the folder.
  const download = page.waitForEvent('download')
  await page.getByRole('button', { name: 'Export .zip' }).click()
  const file = await download
  expect(file.suggestedFilename()).toBe('release-notes-copy.zip')
  expect(readFileSync((await file.path())!).toString('latin1')).toContain('release-notes-copy/SKILL.md')

  // Delete asks first.
  await page.getByRole('button', { name: 'Delete' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: 'Delete' }).click()
  await expect(page.getByRole('heading', { name: 'Skills' })).toBeVisible()
  const list = page.getByRole('list', { name: 'Skills' })
  await expect(list.getByText('release-notes-copy')).toBeHidden()
  await expect(list.getByText('release-notes', { exact: true })).toBeVisible()

  // Import a SKILL.md.
  await page.getByTestId('skill-import').setInputFiles({
    name: 'SKILL.md',
    mimeType: 'text/markdown',
    buffer: Buffer.from('---\nname: changelog\ndescription: Keep CHANGELOG.md current.\n---\n# Changelog\n'),
  })
  await expect(page.getByRole('heading', { name: 'Skill · changelog' })).toBeVisible()
})
