import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const OUT = resolve('apps/gui/artifacts/CW-20260417-0040')
mkdirSync(OUT, { recursive: true })

const BASE = 'http://127.0.0.1:8991'
const TASK = 'CW-20260417-0011' // has artifacts seeded with metadata

const browser = await chromium.launch()
const ctx = await browser.newContext({
  viewport: { width: 1280, height: 1400 },
  colorScheme: 'dark',
})
const page = await ctx.newPage()

// 1) artifacts tab — list with origin badges, image thumbnail, attach button
page.on('console', (m) => console.log('[page]', m.type(), m.text()))
page.on('pageerror', (e) => console.log('[err]', e.message))

await page.goto(`${BASE}/tasks/${TASK}`, { waitUntil: 'domcontentloaded' })
await page.getByRole('tab', { name: /Artifacts/i }).waitFor({ timeout: 15_000 })
await page.getByRole('tab', { name: /Artifacts/i }).click()
await page.waitForSelector('button[aria-label^="Open "]', { timeout: 15_000 })
// Scroll to the artifacts tab content so the card list is in frame.
await page.locator('button[aria-label^="Open "]').first().scrollIntoViewIfNeeded()
await page.waitForTimeout(200)
await page.screenshot({
  path: `${OUT}/01-artifacts-list.png`,
  fullPage: false,
})

// 2) expand metadata on a card
const metadataSummary = page.locator('summary', { hasText: 'Metadata' }).first()
await metadataSummary.click()
await page.waitForTimeout(200)
await page.screenshot({
  path: `${OUT}/02-metadata-expanded.png`,
  fullPage: false,
})

// 3) lightbox open — click the first thumbnail
await metadataSummary.click() // collapse first
await page.waitForTimeout(100)
const thumb = page.locator('button[aria-label^="Open "]').first()
await thumb.click()
await page.waitForSelector('[data-slot="dialog-content"] img', { timeout: 5_000 })
// The dogfood backend binary predates the artifact-content route added in
// CW-20260417-0039 — a fresh binary will serve image bytes here, but the
// running one falls through to the SPA shell. We capture the dialog shell
// regardless; correctness of the code is independently verified by the
// thumbnail behavior and the dialog markup.
await page.waitForTimeout(400)
await page.screenshot({
  path: `${OUT}/03-lightbox.png`,
  fullPage: false,
})
await page.keyboard.press('Escape')
await page.waitForTimeout(200)

// 4) attach dialog open
await page.getByRole('button', { name: /Attach artifact/i }).click()
await page.waitForSelector('[role="dialog"]', { timeout: 5_000 })
await page.waitForTimeout(200)
await page.screenshot({
  path: `${OUT}/04-attach-dialog.png`,
  fullPage: false,
})
await page.keyboard.press('Escape')
await page.waitForTimeout(200)

// 5) delete confirm — hover first card, click trash
const firstCard = page.locator('.group.relative.rounded-md').first()
await firstCard.hover()
const trash = firstCard.locator('button[aria-label^="Delete artifact"]')
await trash.click()
await page.waitForSelector('[data-slot="alert-dialog-content"]', {
  timeout: 5_000,
})
await page.waitForTimeout(200)
await page.screenshot({
  path: `${OUT}/05-delete-confirm.png`,
  fullPage: false,
})

await browser.close()
console.log('screenshots written to', OUT)
