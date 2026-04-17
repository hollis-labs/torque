import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const OUT = resolve(process.cwd(), 'ui/artifacts/CW-20260417-0024')
mkdirSync(OUT, { recursive: true })

const BASE = process.env.BASE ?? 'http://localhost:5175'
const BLOCKED_ID = process.env.BLOCKED_ID ?? 'CW-20260408-0006'
const PAUSED_ID = process.env.PAUSED_ID ?? 'CW-20260410-0001'

const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } })
const page = await ctx.newPage()

async function shot(name) {
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: false })
  console.log(`wrote ${OUT}/${name}.png`)
}

async function sleep(ms) { return new Promise((r) => setTimeout(r, ms)) }

// 1. Board with blocked rows visible
await page.goto(`${BASE}/?status=blocked,paused`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr', { timeout: 10000 })
await sleep(500)
await shot('01-board-blocked-paused')

// 2. Hover over the blocked status pill to surface tooltip
const blockedRow = page.locator(`a[href="/tasks/${BLOCKED_ID}"]`).first()
const blockedRowEl = blockedRow.locator('xpath=ancestor::tr')
const blockedPill = blockedRowEl.locator('span', { hasText: /^blocked$/i }).first()
await blockedPill.hover()
await sleep(800) // tooltip default delay
await shot('02-status-pill-tooltip')

// 3. Task detail page for the blocked task — alert callout
await page.goto(`${BASE}/tasks/${BLOCKED_ID}`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('[data-testid="blocked-reason-alert"]', { timeout: 10000 })
await sleep(400)
await shot('03-task-detail-blocked-alert')

// 4. Switch to Runs tab to show the augmented RunCard (exit_code, ended_at)
const runsTab = page.getByRole('tab', { name: /runs/i }).first()
if (await runsTab.count()) {
  await runsTab.click()
  await sleep(600)
  await shot('04-runs-tab-with-details')
}

// 5. Paused task detail — same component, yellow accent variant
await page.goto(`${BASE}/tasks/${PAUSED_ID}`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('[data-testid="blocked-reason-alert"]', { timeout: 10000 })
await sleep(400)
await shot('05-task-detail-paused-alert')

await browser.close()
