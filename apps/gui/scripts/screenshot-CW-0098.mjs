// Screenshot capture for CW-20260417-0098 (Final Ops Dashboard assembly).
// Run with: node scripts/screenshot-CW-0098.mjs
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

const BASE = process.env.BASE || 'http://localhost:5182'
const OUT = join(process.cwd(), 'artifacts/CW-20260417-0098')

async function shoot(page, name) {
  await mkdir(OUT, { recursive: true })
  const path = join(OUT, `${name}.png`)
  await page.screenshot({ path, fullPage: false })
  console.log('TORQUE_ARTIFACT:', `apps/gui/artifacts/CW-20260417-0098/${name}.png`)
}

const browser = await chromium.launch()

// Activity tab (default) at 1440px.
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(`${BASE}/dashboard`)
  await page.waitForSelector('[role="tablist"]', { timeout: 15_000 })
  await page.waitForTimeout(1500)
  await shoot(page, 'dashboard-activity-1440')
  await ctx.close()
}

// Mission Control at 1440px.
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(`${BASE}/dashboard?tab=mission-control`)
  await page.waitForSelector('[role="tablist"]', { timeout: 15_000 })
  await page.waitForTimeout(1500)
  await shoot(page, 'dashboard-mission-control-1440')
  await ctx.close()
}

// Usage at 1440px.
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(`${BASE}/dashboard?tab=usage`)
  await page.waitForSelector('[role="tablist"]', { timeout: 15_000 })
  await page.waitForTimeout(1500)
  await shoot(page, 'dashboard-usage-1440')
  await ctx.close()
}

// Responsive check at 1280px (Activity tab).
{
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 900 } })
  const page = await ctx.newPage()
  await page.goto(`${BASE}/dashboard`)
  await page.waitForSelector('[role="tablist"]', { timeout: 15_000 })
  await page.waitForTimeout(1500)
  await shoot(page, 'dashboard-activity-1280')
  await ctx.close()
}

await browser.close()
console.log('done')
