// Screenshot capture for CW-20260417-0398 (Auto/Manual filter toggle).
// Run with: BASE=http://localhost:8993 node scripts/screenshot-CW-0398.mjs
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import { createRequire } from 'node:module'

const req = createRequire(import.meta.url)
let chromium
try {
  chromium = req('playwright').chromium
} catch {
  const pwPath = process.env.PLAYWRIGHT_PATH
  if (!pwPath) {
    console.error('Set PLAYWRIGHT_PATH to the playwright package dir, or `npm install playwright`.')
    process.exit(1)
  }
  chromium = req(pwPath).chromium
}

const BASE = process.env.BASE || 'http://localhost:8993'
const OUT = join(process.cwd(), 'artifacts/CW-20260417-0398')

async function shoot(page, name) {
  await mkdir(OUT, { recursive: true })
  const path = join(OUT, `${name}.png`)
  await page.screenshot({ path, fullPage: false })
  console.log('TORQUE_ARTIFACT:', `apps/gui/artifacts/CW-20260417-0398/${name}.png`)
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })

// Default: Manual = All (no URL param).
await page.goto(`${BASE}/operations?status=backlog,todo,queued,doing,review,blocked,done`)
await page.waitForSelector('[role="radiogroup"][aria-label="Filter by manual flag"]', { timeout: 15_000 })
await page.waitForTimeout(600)
await shoot(page, '01-manual-all')

// Auto: manual=false (scheduler-eligible).
await page.getByRole('radio', { name: 'Auto' }).click()
await page.waitForTimeout(600)
await shoot(page, '02-manual-auto')

// Manual: manual=true (held for review).
await page.getByRole('radio', { name: 'Manual' }).click()
await page.waitForTimeout(600)
await shoot(page, '03-manual-manual')

await browser.close()
console.log('done')
