import { chromium } from 'playwright'
import { mkdirSync } from 'node:fs'
import { resolve } from 'node:path'

const OUT = resolve(process.cwd(), 'ui/artifacts/CW-20260417-0012')
mkdirSync(OUT, { recursive: true })

const BASE = process.env.BASE ?? 'http://localhost:5175'

const browser = await chromium.launch()
const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 } })
const page = await ctx.newPage()

async function shot(name) {
  await page.screenshot({ path: `${OUT}/${name}.png`, fullPage: false })
  console.log(`wrote ${OUT}/${name}.png`)
}

async function sleep(ms) { return new Promise((r) => setTimeout(r, ms)) }

// --- Board (table) menu open
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr', { timeout: 10000 })
await sleep(400)

// Click the first row's "..." button. The button has aria-label="Task actions".
const firstMenu = page.locator('[aria-label="Task actions"]').first()
await firstMenu.click()
await sleep(300)
await shot('01-table-menu-open')
await page.keyboard.press('Escape')
await sleep(200)

// --- Detail page menu open
// Pick the first review task from the table to demo Approve visibility
const reviewRow = page.locator('tr', { has: page.locator('text=review') }).first()
const link = reviewRow.locator('a').first()
await link.click()
await page.waitForURL(/\/tasks\/.+/)
await page.waitForSelector('h1', { timeout: 10000 })
await sleep(400)

// Click the menu in the detail header
const detailMenu = page.locator('[aria-label="Task actions"]').first()
await detailMenu.click()
await sleep(300)
await shot('02-detail-menu-open')
await page.keyboard.press('Escape')
await sleep(200)

// --- Apply a transition from the menu and capture the result
// Use a non-review doing task so we can "Pause" safely without changing review state
await page.goto(`${BASE}/`, { waitUntil: 'domcontentloaded' })
await page.waitForSelector('table tbody tr', { timeout: 10000 })
await sleep(300)

// Find the first row whose status badge is "doing"
const doingRow = page.locator('tr', { has: page.locator('text=doing').first() }).first()
const doingTitle = await doingRow.locator('a').first().textContent()
console.log('Pausing task:', doingTitle)

// Open its menu and click "Pause"
await doingRow.locator('[aria-label="Task actions"]').click()
await sleep(300)
await page.getByRole('menuitem', { name: 'Pause' }).click()
await sleep(800)
await shot('03-after-pause')

await browser.close()
