import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const OUT = resolve('apps/gui/artifacts/CW-20260417-0087')
mkdirSync(OUT, { recursive: true })

const BASE = 'http://localhost:5175'

const browser = await chromium.launch()
const ctx = await browser.newContext({
  viewport: { width: 1280, height: 900 },
  colorScheme: 'dark',
})
const page = await ctx.newPage()

page.on('console', (m) => console.log('[page]', m.type(), m.text()))
page.on('pageerror', (e) => console.log('[err]', e.message))

// 1) /operations — Operations overview with compact stats + inline tags
await page.goto(`${BASE}/operations`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('h1:has-text("Operations")', { timeout: 15_000 })
// Wait for at least one task row (or the empty state) to render
await page
  .waitForFunction(() => document.querySelectorAll('tr[role="link"]').length > 0, {
    timeout: 15_000,
  })
  .catch(() => {})
await page.waitForTimeout(300)
await page.screenshot({
  path: `${OUT}/01-operations-overview.png`,
  fullPage: false,
})

// 2) Close-up of the table rows to see inline tags + checkbox alignment
await page.screenshot({
  path: `${OUT}/02-operations-rows-closeup.png`,
  fullPage: false,
  clip: { x: 56, y: 0, width: 1224, height: 420 },
})

// 3) Root redirect — hitting / should land on /operations
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
await page.waitForTimeout(500)
const url = page.url()
console.log('root landed on:', url)

await browser.close()
console.log('screenshots written to', OUT)
