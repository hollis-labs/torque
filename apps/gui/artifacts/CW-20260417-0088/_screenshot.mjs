import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const OUT = resolve('apps/gui/artifacts/CW-20260417-0088')
mkdirSync(OUT, { recursive: true })

const BASE = 'http://localhost:5175'

const browser = await chromium.launch()
const ctx = await browser.newContext({
  viewport: { width: 1280, height: 900 },
  colorScheme: 'dark',
})
const page = await ctx.newPage()

page.on('pageerror', (e) => console.log('[err]', e.message))

// 1) Ops page (BoardPage) — shows the new Rebuild UI button in the header.
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
await page.getByRole('button', { name: /Rebuild bundled frontend/i }).waitFor({ timeout: 15_000 })
await page.waitForTimeout(400)
await page.screenshot({
  path: `${OUT}/01-ops-header-with-rebuild-button.png`,
  fullPage: false,
})

// 2) Capture the first task in the list, then its detail page — proves the
// task list cursor is populated and arrow-nav is wired for any task.
const firstTaskRow = page.getByRole('link', { name: /Open task/i }).first()
await firstTaskRow.waitFor({ timeout: 10_000 })
const firstTaskId = await firstTaskRow.getAttribute('aria-label')
console.log('first task aria-label:', firstTaskId)
await firstTaskRow.click()
// Wait for the detail page — the Queue toggle button is detail-only.
await page.waitForSelector('button:has-text("Queue"), button:has-text("Unqueue")', {
  timeout: 15_000,
})
await page.waitForTimeout(400)
await page.screenshot({
  path: `${OUT}/02-task-detail-after-list-navigation.png`,
  fullPage: false,
})

// 3) Send ArrowRight to verify keyboard nav lands on the next task.
const urlBefore = page.url()
await page.keyboard.press('ArrowRight')
await page.waitForFunction((before) => window.location.href !== before, urlBefore, { timeout: 5000 })
await page.waitForTimeout(400)
await page.screenshot({
  path: `${OUT}/03-task-detail-after-arrow-right.png`,
  fullPage: false,
})

await browser.close()
console.log('screenshots written to', OUT)
